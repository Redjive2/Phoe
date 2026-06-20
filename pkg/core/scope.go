package core

import "fmt"

// Lexical scoping model.
//
// All code shares one Env.Stack (innermost frame at index 0), but a function
// body must not see its *caller's* frames — only the frames of the scope it
// was *defined* in. Each function-call boundary therefore pushes a tcontext
// (see types.go) recording:
//
//   - Hidden:    how many frames at the bottom of env.Stack belong to the
//     caller and are invisible. The visible window is
//     env.Stack[:len(env.Stack)-Hidden], which naturally grows as
//     blocks/loops push frames inside the body.
//   - DefFrames: the frames that were lexically visible at the definition
//     site, shared by reference. Writes through Set propagate to the
//     defining scope while it is alive, and the maps outlive the stack, so
//     closures keep working after the defining function returns.
//
// Blocks (`&body` in if/for) do not push a context at all — they run in
// the scope they were written in.

// visibleStack returns the env.Stack frames the current context may see:
// everything pushed since the innermost function-call boundary.
func (ctx Context) visibleStack() []map[string]tStackEntry {
	env := ctx.Env
	if len(env.CtxStack) == 0 {
		return env.Stack
	}
	return env.Stack[:len(env.Stack)-env.CtxStack[0].Hidden]
}

// defFrames returns the definition-site frames of the innermost function
// context (nil at the top level).
func (ctx Context) defFrames() []map[string]tStackEntry {
	if len(ctx.Env.CtxStack) == 0 {
		return nil
	}
	return ctx.Env.CtxStack[0].DefFrames
}

// LexicalFrames snapshots the full scope chain visible at ctx: the visible
// stack window followed by the enclosing definition-site frames. BindFun /
// BindMethod call this at *definition* time and carry the result into every
// call as the body's DefFrames.
func (ctx Context) LexicalFrames() []map[string]tStackEntry {
	frames := append([]map[string]tStackEntry{}, ctx.visibleStack()...)
	return append(frames, ctx.defFrames()...)
}

// Declare installs a new binding in the innermost frame. A declaration may
// shadow a binding from an enclosing scope (an outer block, the file/package
// level, or a closure capture) — only two things block it: a name already
// bound in the *same* (innermost) frame, which is a redeclaration, and a
// builtin global, which stays un-shadowable. Returns false in those cases.
func (ctx Context) Declare(targetIdent string, targetValue Tval, isConst bool) bool {
	if _, found := ctx.Env.Stack[0][targetIdent]; found {
		return false
	}

	if _, found := (*ctx.Env.Globals)[targetIdent]; found {
		return false
	}

	ctx.Env.Stack[0][targetIdent] = tStackEntry{targetValue, isConst}
	return true
}

// Rebind is Declare without the same-scope-redeclaration block: it replaces
// any existing binding of the same name in the innermost frame. var/const
// use it so a name can be rebound in place — `(const 'x 1) (const 'x 2)` —
// giving fresh immutable bindings instead of var + '=' mutation. Builtins
// remain un-shadowable (the only case that returns false). An enclosing-
// scope binding is still shadowed, never mutated, since only Stack[0] is
// written.
func (ctx Context) Rebind(targetIdent string, targetValue Tval, isConst bool) bool {
	if _, found := (*ctx.Env.Globals)[targetIdent]; found {
		return false
	}

	ctx.Env.Stack[0][targetIdent] = tStackEntry{targetValue, isConst}
	return true
}

// Resolve resolves an identifier to a value in the current scope. Search
// order: visible stack frames, then the definition-site frames, then
// globals, then the active file's imports.
func (ctx Context) Resolve(targetIdent string) (Tval, bool) {
	if debugBind && targetIdent == "x" {
		fmt.Printf("RESOLVE x: visible=%d def=%d ctxStack=%d\n", len(ctx.visibleStack()), len(ctx.defFrames()), len(ctx.Env.CtxStack))
		if len(ctx.Env.CtxStack) > 0 {
			fmt.Printf("  Hidden=%d totalStack=%d\n", ctx.Env.CtxStack[0].Hidden, len(ctx.Env.Stack))
		}
	}
	for _, frame := range ctx.visibleStack() {
		if val, found := frame[targetIdent]; found {
			return val.Val, true
		}
	}

	for fi, frame := range ctx.defFrames() {
		if val, found := frame[targetIdent]; found {
			if debugBind && targetIdent == "x" {
				fmt.Printf("  FOUND x in defFrame[%d] = %v kind=%s\n", fi, val.Val.Val, val.Val.Kind)
			}
			return val.Val, true
		}
		if debugBind && targetIdent == "x" {
			ks := []string{}
			for k := range frame {
				ks = append(ks, k)
			}
			fmt.Printf("  defFrame[%d] (in Resolve) keys=%v\n", fi, ks)
		}
	}

	if val, found := (*ctx.Env.Globals)[targetIdent]; found {
		return val.Val, true
	}

	if ctx.File != nil {
		if mod, found := ctx.File.Imports[targetIdent]; found {
			return mod, true
		}
	}

	return TvNil, false
}

// SetResult is the outcome of Context.Set. Set itself stays silent:
// the call site knows the assignment form being evaluated and owns the
// diagnostic.
type SetResult int

const (
	SetOK      SetResult = iota
	SetMissing           // no visible binding with that name
	SetConst             // the binding exists but is constant
)

// Set updates an existing binding, searching the same chain as Resolve.
func (ctx Context) Set(targetIdent string, newVal Tval) SetResult {
	setIn := func(frame map[string]tStackEntry) (res SetResult, done bool) {
		val, found := frame[targetIdent]
		if !found {
			return SetMissing, false
		}
		if val.IsConstant {
			return SetConst, true
		}
		frame[targetIdent] = tStackEntry{newVal, false}
		return SetOK, true
	}

	for _, frame := range ctx.visibleStack() {
		if res, done := setIn(frame); done {
			return res
		}
	}

	for _, frame := range ctx.defFrames() {
		if res, done := setIn(frame); done {
			return res
		}
	}

	if res, done := setIn(*ctx.Env.Globals); done {
		return res
	}

	return SetMissing
}

// PushFunContext enters a function body: only the frame just pushed for the
// arguments is visible on env.Stack — everything beneath belongs to the
// caller and is hidden. defFrames is the definition-site scope chain
// captured by LexicalFrames when the function was defined.
func (ctx Context) PushFunContext(defFrames []map[string]tStackEntry) {
	env := ctx.Env
	env.CtxStack = append([]tcontext{{
		DefFrames: defFrames,
		Hidden:    len(env.Stack) - 1,
	}}, env.CtxStack...)
}

func (ctx Context) PopContext() {
	ctx.Env.CtxStack = ctx.Env.CtxStack[1:]
}

func (ctx Context) PushFrame() {
	ctx.Env.Stack = append([]map[string]tStackEntry{{}}, ctx.Env.Stack...)
}

func (ctx Context) PopFrame() {
	ctx.Env.Stack = ctx.Env.Stack[1:]
}
