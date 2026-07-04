package builtins

import (
	"fmt"
	"strings"

	"pho/pkg/core"
)

// Clause-based implementations (Features.md §1, §2, §9).
//
// A `(fun name (T…) R)` signature registers an Overload on the name's FunDecl
// (core/fundecl.go); each adjacent `(let name (patterns) [where guard] = body)`
// clause compiles here and appends to that overload. The first clause installs
// a dispatcher — a plain core.Fun that selects the overload by arity + runtime
// argument types (most-specific wins), applies signature `else` defaults, and
// tries clauses in declaration order.

// ---------------------------------------------------------------------------
// Signature evaluation
// ---------------------------------------------------------------------------

// evalTypeNode evaluates a signature type expression to a PhoType. A
// `fun`-headed node (a function TYPE in a sig slot) coarsens to TypeFunction —
// evaluating it would create a lambda instead.
func evalTypeNode(ctx core.Context, node core.Node) (*core.PhoType, bool) {
	if br, ok := core.AsBranch(node); ok && len(br) > 0 {
		if h, ok := core.AsLeaf(br[0]); ok && string(h) == "fun" {
			return core.TypeFunction, true
		}
	}
	v := node.Evaluate(ctx)
	if v.Kind != core.KindType {
		ctx.Errorf(core.ErrType, "signature slot '%s' is not a type (kind '%s')", core.Inspect(node), v.Kind)
		return nil, false
	}
	return v.Val.(*core.PhoType), true
}

// evalSigParams evaluates a signature's parameter list into SigParams,
// enforcing the ordering grammar required* (optional…)* (spread)? and the
// modifier shapes (var/spread/optional/const, plus `optional T else DEFAULT`).
func evalSigParams(ctx core.Context, params core.Node, caller string) ([]core.SigParam, bool) {
	br, ok := core.AsBranch(params)
	if !ok {
		ctx.Errorf(core.ErrBadForm, "'%s' signature expects a parameter-type list", caller)
		return nil, false
	}
	out := make([]core.SigParam, 0, len(br))
	sawOptional := false
	for i, p := range br {
		sp := core.SigParam{Mode: core.ParamRequired}
		typeNode := p

		if pb, isBr := core.AsBranch(p); isBr && len(pb) >= 2 {
			if h, ok := core.AsLeaf(pb[0]); ok {
				switch string(h) {
				case "var":
					sp.Mode = core.ParamVar
					typeNode = pb[1]
					// A signature's `(var …)` marks the mutable RECEIVER only —
					// it must be `(var Self)`. A value parameter type cannot be
					// mutable (Effects.md).
					if leaf, ok := core.AsLeaf(pb[1]); !ok || string(leaf) != "Self" {
						ctx.Errorf(core.ErrBadForm, "'%s': (var …) is only valid for the receiver — write (var Self); a value parameter type cannot be mutable", caller)
						return nil, false
					}
				case "spread":
					sp.Mode = core.ParamSpread
					typeNode = pb[1]
					if i != len(br)-1 {
						ctx.Errorf(core.ErrBadForm, "'%s': (spread T) must be the final parameter", caller)
						return nil, false
					}
				case "const":
					sp.Mode = core.ParamConst
					typeNode = pb[1]
				case "optional":
					sp.Mode = core.ParamOptional
					typeNode = pb[1]
					// (optional T else DEFAULT) — 4 children with `else` at [2].
					if len(pb) == 4 {
						if kw, ok := core.AsLeaf(pb[2]); !ok || string(kw) != "else" {
							ctx.Errorf(core.ErrBadForm, "'%s': a defaulted optional is written (optional Type else default)", caller)
							return nil, false
						}
						sp.Default = pb[3]
					} else if len(pb) != 2 {
						ctx.Errorf(core.ErrBadForm, "'%s': a defaulted optional is written (optional Type else default)", caller)
						return nil, false
					}
				}
			}
		}

		if sp.Mode == core.ParamRequired || sp.Mode == core.ParamVar || sp.Mode == core.ParamConst {
			if sawOptional {
				ctx.Errorf(core.ErrBadForm, "'%s': a required parameter cannot follow an optional one", caller)
				return nil, false
			}
		}
		if sp.Mode == core.ParamOptional {
			sawOptional = true
		}

		t, ok := evalTypeNode(ctx, typeNode)
		if !ok {
			return nil, false
		}
		sp.Type = t
		out = append(out, sp)
	}
	return out, true
}

// withSelfType runs fn with `Self` bound to the receiver type in a fresh
// frame, so method signatures like `(method P.shift (Self Number) Number)`
// can name their receiver type without it being a global.
func withSelfType(ctx core.Context, recvVal core.Value, fn func(core.Context) core.Value) core.Value {
	ctx.PushFrame()
	defer ctx.PopFrame()
	ctx.Declare("Self", recvVal, true)
	return fn(ctx)
}

// registerSig evaluates and stores one signature as a new Overload under key.
// A duplicate (identical param types + modes) is a redeclaration error.
func registerSig(ctx core.Context, key, name string, params, ret core.Node, static bool, caller string) core.Value {
	sig, ok := evalSigParams(ctx, params, caller)
	if !ok {
		return core.TvNil
	}
	result, ok := evalTypeNode(ctx, ret)
	if !ok {
		return core.TvNil
	}

	// A `(var Self)` receiver declares the method mutates self, so its name must
	// carry the self-mutation suffix `=` (Effects.md). A mutable receiver on a
	// non-`=` method is a contract error.
	if len(sig) > 0 && sig[0].Mode == core.ParamVar && !core.IsSelfEffectName(name) {
		return ctx.Errorf(core.ErrBadForm, "'%s': a '(var Self)' receiver mutates self — the method name must end in '=' (e.g. '%s=')", name, name)
	}

	fd := ctx.FunDeclFor(key, name)
	for _, ov := range fd.Overloads {
		if sameSig(ov.Params, sig) {
			return ctx.Errorf(core.ErrRedeclare, "'%s' already declares this signature", name)
		}
	}

	required, max := 0, len(sig)
	for i, sp := range sig {
		switch sp.Mode {
		case core.ParamSpread:
			max = -1
		case core.ParamOptional:
		default:
			if required == i { // still in the leading required run
				required = i + 1
			}
		}
	}

	fd.Overloads = append(fd.Overloads, &core.Overload{
		Params:   sig,
		Result:   result,
		Required: required,
		Max:      max,
		DefCtx:   ctx,
		Static:   static,
	})
	return core.TvNil
}

func sameSig(a, b []core.SigParam) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].Mode != b[i].Mode {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Clause compilation
// ---------------------------------------------------------------------------

// compiledClause carries the pieces Try closes over.
type compiledClause struct {
	pats       []*pattern
	spreadName string
	binders    []string
	// varNames are the `(var name)` parameters — the caller-mutable ones. The
	// body's final value for each is read back after it runs so a whole-value
	// `(= name v)` reassignment can propagate to the caller (see runBody).
	varNames []string
	// recvVarName is the receiver binder's name when this is a method clause
	// whose receiver is `(var self)` — the `=`-suffix self-mutation case. Empty
	// otherwise. When set, try records the receiver's final value as a rebind
	// for the dot accessor to write back to the receiver lvalue.
	recvVarName string
	guardFun    core.Fun // nil when unguarded
	// runBody runs the clause body and reports the final values of varNames,
	// so try can propagate `(var self)` receiver reassignments. See
	// core.BindClauseBody.
	runBody func(core.Context, []core.Node) (core.Value, map[string]core.Value)
	method  bool // receiver in slot 0 — grant instance privilege
}

// compileClause parses a clause's parameter patterns and compiles its guard
// and body into a core.Clause. isMethod marks slot 0 as the receiver. sigSpread
// is true when the governing signature declares a trailing `(spread T)`: the
// clause's final parameter then collects the rest even when written as a plain
// binder — the signature is authoritative, so `(spread name)` in the clause is
// optional, not required (mirrors `(var Self)`; see Doc/PlanV1/DeclImplSplit.md).
func compileClause(ctx core.Context, name string, params core.Node, guard, body core.Node, isMethod, sigSpread bool) (core.Clause, bool) {
	br, ok := core.AsBranch(params)
	if !ok {
		ctx.Errorf(core.ErrBadForm, "'let' clause for '%s' expects a parameter list", name)
		return core.Clause{}, false
	}

	binders := newBinderSet()
	cc := &compiledClause{method: isMethod}

	for i, p := range br {
		// When the signature declares a trailing spread, the clause's final
		// parameter is the rest-binder — a plain name `nums` or an explicit
		// `(spread nums)`, either way. The sig decides; the clause needn't repeat.
		if sigSpread && i == len(br)-1 {
			nm, ok := spreadBinderName(ctx, p, name)
			if !ok {
				return core.Clause{}, false
			}
			if !claimBinder(ctx, nm, binders) {
				return core.Clause{}, false
			}
			cc.spreadName = nm
			continue
		}
		// (spread name) — trailing rest binder.
		if pb, isBr := core.AsBranch(p); isBr && len(pb) == 2 {
			if h, ok := core.AsLeaf(pb[0]); ok && (string(h) == "spread" || string(h) == "var") {
				nameLeaf, okLeaf := core.AsLeaf(pb[1])
				if !okLeaf || !isBinderName(string(nameLeaf)) {
					ctx.Errorf(core.ErrBadForm, "'%s' pattern needs a plain name, got '%s'", h, core.Inspect(pb[1]))
					return core.Clause{}, false
				}
				if string(h) == "spread" {
					if i != len(br)-1 {
						ctx.Errorf(core.ErrBadForm, "(spread %s) must be the final parameter", nameLeaf)
						return core.Clause{}, false
					}
					if !claimBinder(ctx, string(nameLeaf), binders) {
						return core.Clause{}, false
					}
					cc.spreadName = string(nameLeaf)
					continue
				}
				// (var …) is a SIGNATURE-only construct: an implementation names
				// its receiver PLAINLY (`self`) or matches it with a pattern, and
				// declares mutability in the signature's `(var Self)`. A `=`-named
				// method writes its receiver back to the caller (see below) — the
				// impl needs no `(var self)` marker (Effects.md).
				ctx.Errorf(core.ErrBadForm, "'%s': (var %s) is not allowed in an implementation — name the receiver plainly 'self'; declare mutability in the signature's '(var Self)'", name, nameLeaf)
				return core.Clause{}, false
			}
		}

		pat, ok := parsePattern(ctx, p, binders)
		if !ok {
			return core.Clause{}, false
		}
		cc.pats = append(cc.pats, pat)
	}

	cc.binders = binders.order

	// A `=`-named method mutates its receiver: `self`'s final value propagates
	// back to the caller (a value receiver rebinds; a struct receiver already
	// shares the pointer, so the write-back is a harmless no-op). The receiver
	// is slot 0's plain `self` binder — mutability is declared in the sig's
	// `(var Self)`; the impl carries no marker (Effects.md).
	if isMethod && core.IsSelfEffectName(name) && len(cc.pats) > 0 &&
		cc.pats[0].kind == patBind && cc.pats[0].name == "self" {
		cc.recvVarName = "self"
		cc.varNames = append(cc.varNames, "self")
	}

	cc.runBody = core.BindClauseBody(name, body, cc.binders, cc.varNames, ctx)
	if guard != nil {
		cc.guardFun = core.BindFun(name+" where", guard, cc.binders, nil, ctx)
	}

	catchAll := cc.guardFun == nil && isCatchAll(cc.pats)
	return core.Clause{
		Try:      cc.try,
		CatchAll: catchAll,
		Guarded:  cc.guardFun != nil,
		Arity:    len(cc.pats),
		Spread:   cc.spreadName != "",
		Desc:     patternsDescribe(cc.pats),
	}, true
}

// spreadBinderName extracts the rest-binder name from a clause's trailing
// parameter when the signature declares a spread: it accepts a plain leaf
// binder (`nums`) or the explicit `(spread nums)` wrapper. Anything else — a
// literal, a type test, a destructure — is rejected: the spread slot binds the
// collected tail, it can't pattern-match it.
func spreadBinderName(ctx core.Context, p core.Node, fnName string) (string, bool) {
	if leaf, ok := core.AsLeaf(p); ok {
		if isBinderName(string(leaf)) {
			return string(leaf), true
		}
		ctx.Errorf(core.ErrBadForm, "the trailing spread parameter of '%s' must be a plain name, got '%s'", fnName, core.Inspect(p))
		return "", false
	}
	if pb, ok := core.AsBranch(p); ok && len(pb) == 2 {
		if h, ok := core.AsLeaf(pb[0]); ok && string(h) == "spread" {
			if nl, ok := core.AsLeaf(pb[1]); ok && isBinderName(string(nl)) {
				return string(nl), true
			}
		}
	}
	ctx.Errorf(core.ErrBadForm, "the trailing spread parameter of '%s' must be a plain name or (spread name), got '%s'", fnName, core.Inspect(p))
	return "", false
}

// try matches one clause: patterns → bindings → guard → body.
func (cc *compiledClause) try(ctx core.Context, args []core.Value) (core.Value, bool) {
	fixed := args
	var rest []core.Value
	if cc.spreadName != "" {
		if len(args) < len(cc.pats) {
			return core.TvNil, false
		}
		fixed, rest = args[:len(cc.pats)], args[len(cc.pats):]
	} else if len(args) != len(cc.pats) {
		return core.TvNil, false
	}

	// A method clause runs with receiver privilege — grant it around the
	// whole match+guard+body so `Self.{ #field = … }` destructuring and the
	// body's `self.#x` reads both work (mirrors BindMethod's grant).
	if cc.method && len(args) > 0 {
		if inst, ok := args[0].Val.(*core.Instance); ok {
			was := inst.Privileged
			inst.Privileged = true
			defer func() { inst.Privileged = was }()
		}
	}

	binds := map[string]core.Value{}
	for i, p := range cc.pats {
		if !matchPattern(ctx, p, fixed[i], binds, cc.method) {
			return core.TvNil, false
		}
	}
	if cc.spreadName != "" {
		restCopy := append([]core.Value{}, rest...)
		binds[cc.spreadName] = core.TvSlice(restCopy)
	}

	argv := make([]core.Node, len(cc.binders))
	for i, n := range cc.binders {
		argv[i] = core.Lit(binds[n])
	}

	if cc.guardFun != nil {
		g := cc.guardFun(ctx, argv)
		if g.Kind != core.KindBool {
			ctx.Errorf(core.ErrType, "a 'where' guard must yield a boolean, got kind '%s'", g.Kind)
			return core.TvNil, true // stop dispatch — a broken guard must be loud
		}
		if !g.Val.(bool) {
			return core.TvNil, false
		}
	}

	result, finals := cc.runBody(ctx, argv)

	// A `(var self)` method whose body reassigned self to a new whole value:
	// report it so the dot accessor writes the new value back to the receiver
	// lvalue. In-place field mutation needs no help — it shares the pointer —
	// but a rebind is invisible to the caller without this (Effects.md).
	if cc.recvVarName != "" {
		if fv, ok := finals[cc.recvVarName]; ok {
			ctx.RecordRecvRebind(fv)
		}
	}

	return result, true
}

// containsStr reports whether s appears in xs.
func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// applySigDefaults normalizes the provided args against the overload's params:
// missing/`none` optionals take their `else` default (evaluated in the
// declaring context — sig params are types, so defaults are closed
// expressions), plain missing optionals become none.
func applySigDefaults(ctx core.Context, ov *core.Overload, args []core.Value) []core.Value {
	if ov.Params == nil {
		return args
	}
	fixed := len(ov.Params)
	if ov.Max == -1 {
		fixed-- // trailing spread param consumes the tail
	}
	out := make([]core.Value, 0, len(args))
	for i := 0; i < fixed; i++ {
		var v core.Value = core.TvNil
		if i < len(args) {
			v = args[i]
		}
		if sp := ov.Params[i]; sp.Mode == core.ParamOptional && sp.Default != nil && v.Kind == core.KindNil {
			v = sp.Default.Evaluate(ov.DefCtx)
		}
		out = append(out, v)
	}
	if len(args) > fixed {
		out = append(out, args[fixed:]...)
	}
	return out
}

// overloadAccepts reports whether the provided args fit the overload's arity
// and parameter types.
func overloadAccepts(ov *core.Overload, args []core.Value) bool {
	if ov.Params == nil {
		return true // implicit overload: clauses check their own arity
	}
	if len(args) < ov.Required {
		return false
	}
	if ov.Max != -1 && len(args) > ov.Max {
		return false
	}
	fixed := len(ov.Params)
	var spreadType *core.PhoType
	if ov.Max == -1 {
		fixed--
		spreadType = ov.Params[fixed].Type
	}
	for i, a := range args {
		if i < fixed {
			// An OPTIONAL slot always admits `none` — it coalesces to the
			// `else` default (or stays none) before the clause sees it.
			if ov.Params[i].Mode == core.ParamOptional && a.Kind == core.KindNil {
				continue
			}
			if t := ov.Params[i].Type; t != nil && !t.Contains(a) {
				return false
			}
		} else if spreadType != nil && !spreadType.Contains(a) {
			return false
		}
	}
	return true
}

// moreSpecific reports whether a's param tuple is at least as specific as b's
// everywhere and strictly more specific somewhere (§9 most-specific-type).
func moreSpecific(a, b *core.Overload) bool {
	if a.Params == nil || b.Params == nil || len(a.Params) != len(b.Params) {
		return false
	}
	strict := false
	for i := range a.Params {
		at, bt := a.Params[i].Type, b.Params[i].Type
		if !core.Subtype(at, bt) {
			return false
		}
		if !core.Subtype(bt, at) {
			strict = true
		}
	}
	return strict
}

// selectOverload picks the overload for args: filter by arity+types, then
// most-specific. Reports and returns nil on zero or ambiguous candidates.
func selectOverload(ctx core.Context, fd *core.FunDecl, args []core.Value) *core.Overload {
	var cands []*core.Overload
	for _, ov := range fd.Overloads {
		if overloadAccepts(ov, args) {
			cands = append(cands, ov)
		}
	}
	switch len(cands) {
	case 0:
		ctx.Errorf(core.ErrType, "no signature of '%s' accepts %s", fd.Name, describeArgs(args))
		return nil
	case 1:
		return cands[0]
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if moreSpecific(c, best) {
			best = c
		}
	}
	for _, c := range cands {
		if c != best && !moreSpecific(best, c) {
			ctx.Errorf(core.ErrType, "call of '%s' with %s is ambiguous between %d signatures", fd.Name, describeArgs(args), len(cands))
			return nil
		}
	}
	return best
}

func describeArgs(args []core.Value) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = string(a.Kind)
	}
	return "(" + strings.Join(parts, " ") + ")"
}

// dispatchCall runs the full call path shared by every dispatcher variant.
func dispatchCall(ctx core.Context, fd *core.FunDecl, args []core.Value) core.Value {
	ov := selectOverload(ctx, fd, args)
	if ov == nil {
		return core.TvNil
	}
	args = applySigDefaults(ctx, ov, args)
	for i := range ov.Clauses {
		if v, matched := ov.Clauses[i].Try(ctx, args); matched {
			return v
		}
	}
	return ctx.Errorf(core.ErrType, "no implementation of '%s' matches %s — %s", fd.Name, describeArgs(args), clauseSummary(ov))
}

func clauseSummary(ov *core.Overload) string {
	descs := make([]string, len(ov.Clauses))
	for i, c := range ov.Clauses {
		descs[i] = c.Desc
	}
	return fmt.Sprintf("clauses tried: %s", strings.Join(descs, ", "))
}

// ---------------------------------------------------------------------------
// select — the match expression (Features.md §3)
// ---------------------------------------------------------------------------

// selectBuiltins registers `(select value case PATTERN -> RESULT …)`: the
// scrutinee evaluates once; the first case whose pattern matches binds its
// names in a fresh frame and evaluates its RESULT (a `do` result stops at the
// next `case` — the context-aware do boundary in pkg/syntax). No matching
// case is a runtime error; the linter's exhaustiveness rules require a
// catch-all when coverage is undecidable.
func selectBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"select": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) < 5 || (len(argv)-1)%4 != 0 {
				return ctx.Errorf(core.ErrArity, "'select' is written (select value case pattern -> result …)")
			}
			val := argv[0].Evaluate(ctx)

			for i := 1; i+3 < len(argv); i += 4 {
				if kw, ok := core.AsLeaf(argv[i]); !ok || string(kw) != "case" {
					return ctx.Errorf(core.ErrBadForm, "'select' expected 'case', got '%s'", core.Inspect(argv[i]))
				}
				if arrow, ok := core.AsLeaf(argv[i+2]); !ok || string(arrow) != "->" {
					return ctx.Errorf(core.ErrBadForm, "'select' case needs '->' between its pattern and result")
				}
				pat, ok := parsePattern(ctx, argv[i+1], newBinderSet())
				if !ok {
					return core.TvNil
				}
				binds := map[string]core.Value{}
				if !matchPattern(ctx, pat, val, binds, false) {
					continue
				}
				result := argv[i+3]
				// A `(var x)` case binder is reassignable in the arm; a plain
				// binder is const (mirrors `let` destructuring).
				mutable := map[string]bool{}
				for _, b := range patternBinders(pat) {
					mutable[b.name] = b.mutable
				}
				return func() core.Value {
					ctx.PushFrame()
					defer ctx.PopFrame() // panic-safe: a (return) unwinds through
					for name, v := range binds {
						ctx.Declare(name, v, !mutable[name])
					}
					return result.Evaluate(ctx)
				}()
			}
			return ctx.Errorf(core.ErrType, "no case of 'select' matches the value %s", core.Inspect(core.Lit(val)))
		}),
	}
}

// ---------------------------------------------------------------------------
// Clause declaration — the `let` impl form
// ---------------------------------------------------------------------------

// isClauseForm reports whether a `let` invocation is an implementation clause
// `(let [Owner.]name p1 p2 … [where guard] = body)` — a name/receiver, then ≥1
// FLAT parameter, then a top-level `=` (optionally preceded by `where guard`).
// Value bindings have no parameter before the first `=`: `(let x = v)`,
// `(let var x = v)`, the typed `(let (Type x) = v)`, and multi `(let a = 1 b = 2)`.
// The truly 0-parameter function impl `(let name = body)` is a value-binding
// SHAPE disambiguated by isZeroArgClause (a matching signature), not here.
func isClauseForm(argv []core.Node) bool {
	if len(argv) < 4 {
		return false
	}
	// A leading `var` modifier, or a Type in the name slot, is a value binding.
	if kw, ok := core.AsLeaf(argv[0]); ok && string(kw) == "var" {
		return false
	}
	if isTypeNode(argv[0]) {
		return false
	}
	// The first slot after the name must be a real parameter, not the `=`/`where`
	// marker (that shape is the 0-parameter value binding).
	if lf, ok := core.AsLeaf(argv[1]); ok && (string(lf) == "=" || string(lf) == "where") {
		return false
	}
	// A top-level `=`/`where` must follow the parameters.
	for i := 2; i < len(argv); i++ {
		if lf, ok := core.AsLeaf(argv[i]); ok && (string(lf) == "=" || string(lf) == "where") {
			return true
		}
	}
	return false
}

// isZeroArgClause reports whether `(let name = body)` — a value-binding SHAPE —
// is really a 0-argument FUNCTION implementation: the name is a bare identifier
// that already carries a `(fun name -> R)` signature. Without a signature it is
// an ordinary value binding (evaluated once). Methods always take a receiver, so
// only free functions can be 0-arg.
func isZeroArgClause(ctx core.Context, argv []core.Node) bool {
	if len(argv) != 3 {
		return false
	}
	if lf, ok := core.AsLeaf(argv[1]); !ok || string(lf) != "=" {
		return false
	}
	leaf, ok := core.AsLeaf(argv[0])
	if !ok || isTypeNode(argv[0]) {
		return false
	}
	return ctx.LookupFunDecl(string(leaf)) != nil
}

// defineClause declares one implementation clause from a `let` form's argument
// list: [target, p1 p2 …, ("where" guard)?, "=", body]. The flat parameters are
// gathered into a synthetic branch so the rest of the clause pipeline
// (compileClause) reads them exactly as it read the old `(params)` list.
func defineClause(ctx core.Context, argv []core.Node) core.Value {
	recv, methodName, named, ok := methodTarget(ctx, argv[0])
	if !ok {
		return core.TvNil
	}

	// Locate the `=` marker (splits params/guard from body) and an optional
	// `where` (splits params from the guard) at the top level.
	eqIdx, whereIdx := -1, -1
	for i := 1; i < len(argv); i++ {
		if lf, ok := core.AsLeaf(argv[i]); ok {
			if string(lf) == "where" && whereIdx < 0 {
				whereIdx = i
			}
			if string(lf) == "=" {
				eqIdx = i
				break
			}
		}
	}
	if eqIdx < 0 {
		return ctx.Errorf(core.ErrBadForm, "an implementation clause is written (let name params… [where guard] = body); missing '=' before the body")
	}
	if eqIdx != len(argv)-2 {
		return ctx.Errorf(core.ErrArity, "an implementation clause takes exactly one body expression")
	}
	var guard core.Node
	paramEnd := eqIdx
	if whereIdx >= 0 {
		if whereIdx+2 != eqIdx {
			return ctx.Errorf(core.ErrArity, "a guarded clause is written (let name params… where guard = body) — exactly one guard expression before '='")
		}
		guard = argv[whereIdx+1]
		paramEnd = whereIdx
	}
	params := core.Branch(argv[1:paramEnd])
	body := argv[eqIdx+1]

	if !named {
		return defineFunClause(ctx, argv[0], params, guard, body)
	}
	return defineMethodClause(ctx, recv, methodName, params, guard, body)
}

// defineFunClause attaches a clause to a free function's FunDecl, installing
// the dispatcher binding on the first clause.
func defineFunClause(ctx core.Context, nameNode, params core.Node, guard, body core.Node) core.Value {
	funName, ok := declName(ctx, nameNode, "let", "name")
	if !ok {
		return core.TvNil
	}

	fd := ctx.LookupFunDecl(funName)
	if fd == nil || len(fd.Overloads) == 0 {
		// No signature. Libraries require one; a script gets an implicit
		// pattern-dispatched overload (the linter infers/enforces the sig).
		if ctx.File != nil && ctx.File.Mode == core.ModeLibrary {
			return ctx.Errorf(core.ErrBadForm, "function '%s' needs a signature: declare (fun %s (Types…) Result) before its clauses", funName, funName)
		}
		fd = ctx.FunDeclFor(funName, funName)
		fd.Implicit = true
		fd.Overloads = append(fd.Overloads, &core.Overload{DefCtx: ctx, Max: -2})
	}
	ov := fd.Latest()

	cl, ok := compileClause(ctx, funName, params, guard, body, false, ov.Max == -1)
	if !ok {
		return core.TvNil
	}
	if !clauseFitsSig(ctx, ov, cl, funName) {
		return core.TvNil
	}
	ov.Clauses = append(ov.Clauses, cl)

	if !fd.Installed {
		if !ctx.Declare(funName, core.TvFun(clauseDispatchFun(fd)), true) {
			return ctx.Errorf(core.ErrRedeclare, "cannot declare function '%s': name already in use", funName)
		}
		fd.Installed = true
	}
	return core.TvNil
}

// defineMethodClause attaches a clause to a method's FunDecl — instance
// (struct / union / extension) or static — installing the dispatcher into the
// right table on the first clause.
func defineMethodClause(ctx core.Context, recv core.Node, methodName string, params core.Node, guard, body core.Node) core.Value {
	ownerKey := core.Inspect(recv)
	instKey := ownerKey + "." + methodName
	staticKey := ownerKey + "/" + methodName

	fd := ctx.LookupFunDecl(instKey)
	static := false
	if sfd := ctx.LookupFunDecl(staticKey); sfd != nil {
		if fd != nil {
			return ctx.Errorf(core.ErrBadForm, "'%s.%s' has both an instance and a static declaration — the clause is ambiguous", ownerKey, methodName)
		}
		fd, static = sfd, true
	}

	recvVal := recv.Evaluate(ctx)
	if recvVal.Kind != core.KindType {
		return ctx.Errorf(core.ErrType, "'let' method receiver must be a type or struct, got kind '%s'", recvVal.Kind)
	}
	recvType := recvVal.Val.(*core.PhoType)

	if fd == nil || len(fd.Overloads) == 0 {
		if ctx.File != nil && ctx.File.Mode == core.ModeLibrary {
			return ctx.Errorf(core.ErrBadForm, "method '%s.%s' needs a signature: declare (method %s.%s (Self …) Result) before its clauses", ownerKey, methodName, ownerKey, methodName)
		}
		fd = ctx.FunDeclFor(instKey, methodName)
		fd.Implicit = true
		fd.Overloads = append(fd.Overloads, &core.Overload{DefCtx: ctx, Max: -2})
	}
	ov := fd.Latest()

	label := recvType.Name() + "." + methodName
	cl, ok := compileClause(ctx, label, params, guard, body, !static, ov.Max == -1)
	if !ok {
		return core.TvNil
	}
	if !clauseFitsSig(ctx, ov, cl, label) {
		return core.TvNil
	}
	ov.Clauses = append(ov.Clauses, cl)

	if fd.Installed {
		return core.TvNil
	}
	fd.Installed = true

	if static {
		sdata, isStruct := core.StructOf(recvType)
		if !isStruct {
			return ctx.Errorf(core.ErrType, "'static method' receiver '%s' is not a struct", ownerKey)
		}
		sdata.StaticMethods[methodName] = clauseDispatchStatic(fd)
		return core.TvNil
	}

	dispatcher := clauseDispatchMethod(fd)
	if sdata, isStruct := core.StructOf(recvType); isStruct {
		sdata.Methods[methodName] = dispatcher
		return core.TvNil
	}
	typeKey := recvType.TypeKey()
	if typeKey == "" {
		// Finite-union receiver (e.g. Collection = String|List|Map): attach
		// the dispatcher to every concrete member.
		keys := recvType.MemberKeys()
		if len(keys) == 0 {
			return ctx.Errorf(core.ErrType, "'let' cannot attach a method to the type '%s'", recvType.Name())
		}
		for _, k := range keys {
			if !ctx.AddMethod(k, methodName, dispatcher, false, isExportedMember(methodName)) {
				return ctx.Errorf(core.ErrRedeclare, "method '%s' for a member of '%s' is already declared in this module", methodName, recvType.Name())
			}
		}
		return core.TvNil
	}
	if !ctx.AddMethod(typeKey, methodName, dispatcher, false, isExportedMember(methodName)) {
		return ctx.Errorf(core.ErrRedeclare, "method '%s.%s' is already declared in this module", recvType.Name(), methodName)
	}
	return core.TvNil
}

// clauseFitsSig validates a clause's arity against its overload's signature:
// the fixed pattern count must equal the sig's fixed param count, and a
// spread sig requires a spread binder (and vice versa).
func clauseFitsSig(ctx core.Context, ov *core.Overload, cl core.Clause, name string) bool {
	if ov.Params == nil {
		return true // implicit overload — clauses stand alone
	}
	fixed := len(ov.Params)
	sigSpread := ov.Max == -1
	if sigSpread {
		fixed--
	}
	if cl.Arity != fixed {
		ctx.Errorf(core.ErrArity, "clause of '%s' has %d parameters; its signature declares %d", name, cl.Arity, fixed)
		return false
	}
	if sigSpread != cl.Spread {
		if sigSpread {
			ctx.Errorf(core.ErrBadForm, "clause of '%s' is missing the trailing (spread name) its signature declares", name)
		} else {
			ctx.Errorf(core.ErrBadForm, "clause of '%s' has a (spread name) its signature does not declare", name)
		}
		return false
	}
	return true
}

// clauseDispatchFun is the dispatcher bound for a free function.
func clauseDispatchFun(fd *core.FunDecl) core.Fun {
	return func(ctx core.Context, argv []core.Node) core.Value {
		args, ok := core.DistributeSpreadExpressions(ctx, core.Branch(argv))
		if !ok {
			return core.TvNil
		}
		return dispatchCall(ctx, fd, args)
	}
}

// clauseDispatchMethod is the dispatcher stored in a method table: the
// receiver comes from the instance stack (pushed by the dot accessor) and
// becomes clause slot 0.
func clauseDispatchMethod(fd *core.FunDecl) core.Fun {
	return func(ctx core.Context, argv []core.Node) core.Value {
		if len(ctx.Env.InstStack) == 0 {
			return ctx.Errorf(core.ErrNoReceiver, "method '%s' called without a receiver instance", fd.Name)
		}
		recv := ctx.Env.InstStack[0]
		args, ok := core.DistributeSpreadExpressions(ctx, core.Branch(argv))
		if !ok {
			return core.TvNil
		}
		return dispatchCall(ctx, fd, append([]core.Value{recv}, args...))
	}
}

// clauseDispatchStatic is clauseDispatchMethod for a `static method`: the
// receiver is the TYPE value and is NOT a clause slot (static sig params are
// all real arguments); clause bodies that need the type bind it themselves.
func clauseDispatchStatic(fd *core.FunDecl) core.Fun {
	return func(ctx core.Context, argv []core.Node) core.Value {
		args, ok := core.DistributeSpreadExpressions(ctx, core.Branch(argv))
		if !ok {
			return core.TvNil
		}
		return dispatchCall(ctx, fd, args)
	}
}
