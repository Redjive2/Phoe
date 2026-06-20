package builtins

import (
	"pho/pkg/core"
)

// isKeyword reports whether n is the bare keyword leaf `kw`. The control
// forms use these as noop markers read structurally and never evaluated —
// `then`/`elif`/`else` in `if`, `in` in `foreach`, `then` in `while`/`until`.
func isKeyword(n core.Node, kw string) bool {
	lf, ok := core.AsLeaf(n)
	return ok && string(lf) == kw
}

// loopIteration runs one loop-body call inside a fresh frame. A BreakSignal
// makes it return broke=true; a ContinueSignal is swallowed; a ReturnSignal
// (or any other panic) propagates. *last receives the body's value only on a
// normal completion, so break/continue leave the previous iteration's value
// intact. bind, when non-nil, installs the per-iteration loop variable after
// the frame is pushed (foreach); the conditional loops pass nil.
//
// Defer order matters: PopFrame is deferred first so it runs LAST — after the
// recover defer has caught break/continue — keeping the stack balanced on
// every exit path.
func loopIteration(ctx core.Context, body core.Fun, last *core.Value, bind func(core.Context)) (broke bool) {
	ctx.PushFrame()
	defer ctx.PopFrame()
	defer func() {
		switch r := recover(); r.(type) {
		case nil:
		case core.BreakSignal:
			broke = true
		case core.ContinueSignal:
		default:
			panic(r)
		}
	}()
	if bind != nil {
		bind(ctx)
	}
	*last = body(ctx, nil)
	return false
}

// parseCondLoop validates the shared `(cond then body)` shape of while/until
// and returns the (unevaluated) condition node plus the body as a callback.
// ok=false means a diagnostic was already reported and the caller must abort.
func parseCondLoop(ctx core.Context, argv []core.Node, caller string) (cond core.Node, body core.Fun, ok bool) {
	if len(argv) != 3 {
		ctx.Errorf(core.ErrArity, "'%s' takes 'cond then body'; got %d argument(s)", caller, len(argv))
		return nil, nil, false
	}
	if !isKeyword(argv[1], "then") {
		ctx.Errorf(core.ErrBadForm, "'%s': expected 'then' between the condition and the body, got '%s'", caller, core.Inspect(argv[1]))
		return nil, nil, false
	}
	return argv[0], core.BindCallback(argv[2]), true
}

// ctrlBuiltins returns control-flow and boolean builtins:
// if, foreach, while, until, do, return, break, continue, and, or, ~ (not).
func ctrlBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"~": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'~' requires exactly 1 argument; got %d", len(argv))
			}
			b, ok := asBool(ctx, argv[0].Evaluate(ctx), "~")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(!b)
		}),

		"and": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'and' requires exactly 2 arguments; got %d", len(argv))
			}
			a, ok := asBool(ctx, argv[0].Evaluate(ctx), "and")
			if !ok {
				return core.TvNil
			}
			if !a {
				return core.TvBool(false)
			}
			b, ok := asBool(ctx, argv[1].Evaluate(ctx), "and")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(b)
		}),

		"or": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'or' requires exactly 2 arguments; got %d", len(argv))
			}
			a, ok := asBool(ctx, argv[0].Evaluate(ctx), "or")
			if !ok {
				return core.TvNil
			}
			if a {
				return core.TvBool(true)
			}
			b, ok := asBool(ctx, argv[1].Evaluate(ctx), "or")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(b)
		}),

		// (if cond then expr
		//  elif cond then expr     -- zero or more elif branches
		//  ...
		//  else expr)              -- optional trailing else
		//
		// `then`, `elif`, and `else` are bare keyword markers. Branches are
		// tried in order; the first whose condition is true yields its `then`
		// expression. With no match and no `else`, the result is Nil. Arms are
		// ordinary expressions evaluated in the enclosing scope, so only the
		// taken branch runs.
		"if": global(func(ctx core.Context, argv []core.Node) core.Value {
			// Parse the clause structure up front (validating the keywords),
			// then evaluate lazily so only the matched branch's expression and
			// the conditions up to it ever run.
			type branch struct{ cond, expr core.Node }
			var branches []branch
			var elseExpr core.Node
			hasElse := false

			for i, n := 0, len(argv); i < n; {
				if isKeyword(argv[i], "else") {
					if i+2 != n {
						return ctx.Errorf(core.ErrBadForm, "'if': 'else' takes exactly one expression and must come last")
					}
					elseExpr, hasElse = argv[i+1], true
					break
				}

				// A `cond then expr` clause.
				if i+3 > n {
					return ctx.Errorf(core.ErrBadForm, "'if': each branch must be written 'cond then expr'")
				}
				if !isKeyword(argv[i+1], "then") {
					return ctx.Errorf(core.ErrBadForm, "'if': expected 'then' after the condition, got '%s'", core.Inspect(argv[i+1]))
				}
				branches = append(branches, branch{cond: argv[i], expr: argv[i+2]})
				i += 3

				if i >= n {
					break
				}
				switch {
				case isKeyword(argv[i], "elif"):
					i++
					if i >= n || isKeyword(argv[i], "elif") || isKeyword(argv[i], "else") {
						return ctx.Errorf(core.ErrBadForm, "'if': 'elif' must be followed by a condition")
					}
				case isKeyword(argv[i], "else"):
					// Handled at the top of the next iteration.
				default:
					return ctx.Errorf(core.ErrBadForm, "'if': expected 'elif' or 'else' between branches, got '%s'", core.Inspect(argv[i]))
				}
			}

			for _, b := range branches {
				cond, ok := asBool(ctx, b.cond.Evaluate(ctx), "if")
				if !ok {
					return core.TvNil
				}
				if cond {
					return b.expr.Evaluate(ctx)
				}
			}
			if hasElse {
				return elseExpr.Evaluate(ctx)
			}
			return core.TvNil
		}),

		// opposite of 'if'; does not support 'elif'
		"unless": global(func(ctx core.Context, argv []core.Node) core.Value {
			isKeyword := func(n core.Node, kw string) bool {
				lf, ok := core.AsLeaf(n)
				return ok && string(lf) == kw
			}

			// Parse the clause structure up front (validating the keywords),
			// then evaluate lazily so only the matched branch's expression and
			// the conditions up to it ever run.
			type branch struct{ cond, expr core.Node }
			var branches []branch
			var elseExpr core.Node
			hasElse := false

			for i, n := 0, len(argv); i < n; {
				if isKeyword(argv[i], "else") {
					if i+2 != n {
						return ctx.Errorf(core.ErrBadForm, "'unless': 'else' takes exactly one expression and must come last")
					}
					elseExpr, hasElse = argv[i+1], true
					break
				}

				// A `cond then expr` clause.
				if i+3 > n {
					return ctx.Errorf(core.ErrBadForm, "'unless': each branch must be written '<condition> then <expression>'")
				}
				if !isKeyword(argv[i+1], "then") {
					return ctx.Errorf(core.ErrBadForm, "'unless': expected 'then' after the condition, got '%s'", core.Inspect(argv[i+1]))
				}
				branches = append(branches, branch{cond: argv[i], expr: argv[i+2]})
				i += 3

				if i >= n {
					break
				}
				switch {
				case isKeyword(argv[i], "elif"):
					return ctx.Errorf(core.ErrBadForm, "'unless': 'elif' is not supported")
				case isKeyword(argv[i], "else"):
					// Handled at the top of the next iteration.
				default:
					return ctx.Errorf(core.ErrBadForm, "'unless': expected 'else' between branches, got '%s'", core.Inspect(argv[i]))
				}
			}

			for _, b := range branches {
				cond, ok := asBool(ctx, b.cond.Evaluate(ctx), "unless")
				if !ok {
					return core.TvNil
				}
				// The opposite of `if`: a branch is taken when its condition is
				// FALSE, so `(unless c then x else y)` runs x when c is false.
				if !cond {
					return b.expr.Evaluate(ctx)
				}
			}
			if hasElse {
				return elseExpr.Evaluate(ctx)
			}
			return core.TvNil
		}),

		// (foreach name in collection body) — iterate over an array's
		// elements, a string's runes, or a dict's keys. `in` is a bare noop
		// keyword. The loop variable is a per-iteration constant. foreach is
		// iteration only — for a conditional loop use `while`/`until`.
		"foreach": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 4 {
				return ctx.Errorf(core.ErrArity, "'foreach' takes 'name in collection body'; got %d argument(s)", len(argv))
			}
			nameNode, ok := core.AsLeaf(argv[0])
			if !ok {
				return ctx.Errorf(core.ErrBadForm, "'foreach': the loop variable must be a bare identifier, got '%s'", core.Inspect(argv[0]))
			}
			if !isKeyword(argv[1], "in") {
				return ctx.Errorf(core.ErrBadForm, "'foreach': expected 'in' between the loop variable and the collection, got '%s'", core.Inspect(argv[1]))
			}
			name := string(nameNode)
			body := core.BindCallback(argv[3])
			col := argv[2].Evaluate(ctx)

			last := core.TvNil
			// Each iteration runs in its own frame (so the loop variable
			// doesn't leak and a body `var` doesn't collide across iters),
			// with the loop variable installed as a per-iteration constant.
			iterate := func(elem core.Value) bool {
				return loopIteration(ctx, body, &last, func(c core.Context) {
					c.Env.Stack[0][name] = core.StackEntry{Val: elem, IsConstant: true}
				})
			}

			switch col.Kind {
			case core.KindArray:
				for _, elem := range *col.Val.(*[]core.Value) {
					if iterate(elem) {
						break
					}
				}
			case core.KindStr:
				for _, r := range col.Val.(string) {
					if iterate(core.TvChr(r)) {
						break
					}
				}
			case core.KindDict:
				// Dicts iterate yielding keys, matching Python's
				// `for k in d:` convention. Order is unspecified —
				// Go's map iteration is randomized.
				for k := range *col.Val.(*map[core.Value]core.Value) {
					if iterate(k) {
						break
					}
				}
			default:
				return ctx.Errorf(core.ErrType, "'foreach' cannot iterate over a value of kind '%s'", col.Kind)
			}
			return last
		}),

		// (while cond then body) — run body while cond stays true. `then` is
		// a bare noop keyword; cond is re-evaluated before each iteration.
		"while": global(func(ctx core.Context, argv []core.Node) core.Value {
			cond, body, ok := parseCondLoop(ctx, argv, "while")
			if !ok {
				return core.TvNil
			}
			last := core.TvNil
			for {
				b, ok := asBool(ctx, cond.Evaluate(ctx), "while")
				if !ok {
					return core.TvNil
				}
				if !b {
					break
				}
				if loopIteration(ctx, body, &last, nil) {
					break
				}
			}
			return last
		}),

		// (until cond then body) — run body until cond becomes true (i.e.
		// while it is false). The mirror image of `while`.
		"until": global(func(ctx core.Context, argv []core.Node) core.Value {
			cond, body, ok := parseCondLoop(ctx, argv, "until")
			if !ok {
				return core.TvNil
			}
			last := core.TvNil
			for {
				b, ok := asBool(ctx, cond.Evaluate(ctx), "until")
				if !ok {
					return core.TvNil
				}
				if b {
					break
				}
				if loopIteration(ctx, body, &last, nil) {
					break
				}
			}
			return last
		}),

		// The sequencing primitive behind `do` notation. Users never write
		// this name directly — the lower pass produces it from a bare `do`
		// in a form (see splitDoForm) — so it lives under the mangled
		// core.Do key, hidden like the dot accessor.
		core.Do: global(func(ctx core.Context, argv []core.Node) core.Value {
			value := core.TvNil

			for _, node := range argv {
				value = node.Evaluate(ctx)
			}

			return value
		}),

		// Non-local control flow: each of these panics with a typed
		// signal that the appropriate site recovers — `return` is
		// caught by BindFun/BindMethod, `break`/`continue` by `for`.
		// LoadPackage installs a top-level recover so escaped signals
		// at script scope become an error rather than crashing the
		// host. The linter rejects them outside their valid contexts.

		"return": global(func(ctx core.Context, argv []core.Node) core.Value {
			switch len(argv) {
			case 0:
				panic(core.ReturnSignal{Value: core.TvNil})
			case 1:
				panic(core.ReturnSignal{Value: argv[0].Evaluate(ctx)})
			}
			return ctx.Errorf(core.ErrArity, "'return' takes 0 or 1 arguments; got %d", len(argv))
		}),

		"break": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 0 {
				return ctx.Errorf(core.ErrArity, "'break' takes no arguments; got %d", len(argv))
			}
			panic(core.BreakSignal{})
		}),

		"continue": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 0 {
				return ctx.Errorf(core.ErrArity, "'continue' takes no arguments; got %d", len(argv))
			}
			panic(core.ContinueSignal{})
		}),
	}
}
