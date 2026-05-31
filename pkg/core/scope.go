package core

import (
	"fmt"
	"regexp"
)

// Declare installs a new binding in the innermost frame. Returns false if
// the name is already in use anywhere visible.
func (ctx Context) Declare(targetIdent string, targetValue Tval, isConst bool) bool {
	env := ctx.Env

	if len(env.CtxStack) > 0 {
		for i := range env.CtxStack[0].MaxStackDepth {
			if _, found := env.Stack[i][targetIdent]; found {
				return false
			}
		}
	} else {
		if _, found := env.Stack[0][targetIdent]; found {
			return false
		}
	}

	for _, c := range env.CtxStack {
		if _, found := c.Captures[targetIdent]; found {
			return false
		}
	}

	if _, found := (*env.Globals)[targetIdent]; found {
		return false
	}

	env.Stack[0][targetIdent] = tStackEntry{targetValue, isConst}
	return true
}

// Resolve resolves an identifier to a value in the current scope. Search
// order: stack frames bounded by the current closure context, then globals,
// then the active file's imports.
func (ctx Context) Resolve(targetIdent string) (Tval, bool) {
	env := ctx.Env

	if len(env.CtxStack) > 0 {
		for i := range env.CtxStack[0].MaxStackDepth {
			if val, found := env.Stack[i][targetIdent]; found {
				return val.Val, true
			}
		}
	} else {
		if val, found := env.Stack[0][targetIdent]; found {
			return val.Val, true
		}
	}

	if val, found := (*env.Globals)[targetIdent]; found {
		return val.Val, true
	}

	if ctx.File != nil {
		if mod, found := ctx.File.Imports[targetIdent]; found {
			return mod, true
		}
	}

	return TvNil, false
}

// Set updates an existing binding. Returns false if the name doesn't exist
// or refers to a constant.
func (ctx Context) Set(targetIdent string, newVal Tval) bool {
	env := ctx.Env

	if len(env.CtxStack) > 0 {
		for i := range env.CtxStack[0].MaxStackDepth {
			if val, found := env.Stack[i][targetIdent]; found {
				if val.IsConstant {
					fmt.Println("(ERR) cannot set constant value '" + targetIdent + "' passed @ 'core.Context.Set'")
					return false
				}

				env.Stack[i][targetIdent] = tStackEntry{newVal, false}
				return true
			}
		}
	} else {
		if val, found := env.Stack[0][targetIdent]; found {
			if val.IsConstant {
				fmt.Println("(ERR) cannot set constant value '" + targetIdent + "' passed @ 'core.Context.Set'")
				return false
			}

			env.Stack[0][targetIdent] = tStackEntry{newVal, false}
			return true
		}
	}

	for _, c := range env.CtxStack {
		if captureFunc, found := c.Captures[targetIdent]; found {
			captureFunc(newVal, true)
			return true
		}
	}

	if val, found := (*env.Globals)[targetIdent]; found {
		if val.IsConstant {
			fmt.Println("(ERR) cannot set constant value '" + targetIdent + "' passed @ 'core.Context.Set'")
			return false
		}

		(*env.Globals)[targetIdent] = tStackEntry{newVal, false}
		return true
	}

	return false
}

func findIdentifiers(code ttnode) []string {
	if lf, ok := code.(ttleaf); ok {
		str := string(lf)

		if regexp.MustCompile("^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9\\-]*[a-zA-Z0-9]))$").MatchString(str) {
			return []string{str}
		}

		return []string{}
	}

	var (
		result []string
		branch = code.(ttbranch)
	)

	for _, node := range branch {
		result = append(result, findIdentifiers(node)...)
	}

	return result
}

// capture builds the closure-capture slot for an identifier. The returned
// closure reads/writes through ctx so callers don't have to thread it.
func capture(ctx Context, targetIdent string) func(Tval, bool) Tval {
	return func(newVal Tval, doEdit bool) Tval {
		if doEdit {
			ctx.Set(targetIdent, newVal)
		}

		result, _ := ctx.Resolve(targetIdent)
		return result
	}
}

// PushContext records the visible stack depth at the time a function/block
// is entered so that nested closures can't leak references below their
// definition site.
func (ctx Context) PushContext(code ttnode) {
	env := ctx.Env

	result := tcontext{
		Captures:      map[string]func(Tval, bool) Tval{},
		MaxStackDepth: len(env.Stack),
	}

	for _, ident := range findIdentifiers(code) {
		if _, found := ctx.Resolve(ident); found {
			result.Captures[ident] = capture(ctx, ident)
		}
	}

	env.CtxStack = append([]tcontext{result}, env.CtxStack...)
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
