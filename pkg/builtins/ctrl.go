package builtins

import (
	"fmt"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// ctrlBuiltins returns control-flow and boolean builtins:
// if, for, do, return, break, continue, and, or, ~ (logical not).
func ctrlBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"~": global(func(ctx core.Context, argv []core.Node) core.Value {
			b, ok := asBool(argv[0].Evaluate(ctx), "~")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(!b)
		}),

		"and": global(func(ctx core.Context, argv []core.Node) core.Value {
			a, ok := asBool(argv[0].Evaluate(ctx), "and")
			if !ok {
				return core.TvNil
			}
			if !a {
				return core.TvBool(false)
			}
			b, ok := asBool(argv[1].Evaluate(ctx), "and")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(b)
		}),

		"or": global(func(ctx core.Context, argv []core.Node) core.Value {
			a, ok := asBool(argv[0].Evaluate(ctx), "or")
			if !ok {
				return core.TvNil
			}
			if a {
				return core.TvBool(true)
			}
			b, ok := asBool(argv[1].Evaluate(ctx), "or")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(b)
		}),

		"if": global(func(ctx core.Context, argv []core.Node) core.Value {
			result := core.TvNil

			cond, ok := asBool(argv[0].Evaluate(ctx), "if")
			if !ok {
				return core.TvNil
			}

			if cond {
				result = core.BindCallback(syntax.Derepr(argv[1].(core.Branch)[1]))(ctx, nil)
			} else if len(argv) == 3 {
				result = core.BindCallback(syntax.Derepr(argv[2].(core.Branch)[1]))(ctx, nil)
			}

			return result
		}),

		"for": global(func(ctx core.Context, argv []core.Node) core.Value {
			lastVal := core.TvNil

			// (for 'elem list &body)
			// (for &cond &body)

			if len(argv) < 2 {
				fmt.Println("(ERR) 'for' requires 2 arguments (condition and body) or 3 arguments (identifier, collection, body) @ 'builtins.for'.")
				return core.TvNil
			}

			// callBody runs one iteration of the body. A BreakSignal
			// returns broke=true (caller exits the outer Go loop); a
			// ContinueSignal returns with continued=true (caller skips
			// to the next iteration); a ReturnSignal — or any other
			// panic — re-propagates, since `for` doesn't own those.
			callBody := func(fn core.Fun) (broke bool) {
				defer func() {
					switch r := recover(); r.(type) {
					case nil:
					case core.BreakSignal:
						broke = true
					case core.ContinueSignal:
						// nothing to do — the deferred return path
						// will hand control back to the outer Go loop,
						// which moves on to the next iter naturally.
					default:
						panic(r)
					}
				}()
				lastVal = fn(ctx, nil)
				return false
			}

			if len(argv) == 2 {
				condBlock, ok1 := argv[0].(core.Branch)
				bodyBlock, ok2 := argv[1].(core.Branch)
				if !ok1 || !ok2 || len(condBlock) < 2 || len(bodyBlock) < 2 {
					fmt.Println("(ERR) 'for' arguments must be block expressions ('&...') @ 'builtins.for'.")
					return core.TvNil
				}

				cond := core.BindCallback(syntax.Derepr(condBlock[1]))
				bodyFunc := core.BindCallback(syntax.Derepr(bodyBlock[1]))

				for {
					b, ok := asBool(cond(ctx, nil), "for")
					if !ok {
						return core.TvNil
					}
					if !b {
						break
					}
					if callBody(bodyFunc) {
						break
					}
				}

				return lastVal
			}

			if len(argv) == 3 {
				// (for 'elementName collection &body)
				identNode, ok := syntax.Derepr(argv[0]).(core.Leaf)
				if !ok {
					fmt.Println("(ERR) 'for' first argument must be a quoted identifier ('name) @ 'builtins.for'.")
					return core.TvNil
				}
				ident := string(identNode)

				bodyBlock, ok := argv[2].(core.Branch)
				if !ok || len(bodyBlock) < 2 {
					fmt.Println("(ERR) 'for' body must be a block expression ('&...') @ 'builtins.for'.")
					return core.TvNil
				}
				bodyFunc := core.BindCallback(syntax.Derepr(bodyBlock[1]))

				col := argv[1].Evaluate(ctx)

				// Each iteration runs in its own pushed frame so the loop
				// variable doesn't leak past the loop and a `var` inside
				// the body doesn't collide between iterations. The frame
				// is popped via defer so a runtime error in the body
				// doesn't leave the stack imbalanced.
				//
				// callBody runs as the *innermost* call here, so its
				// break/continue recover catches the signal before the
				// PushFrame's matching PopFrame defer runs — the frame
				// gets cleaned up correctly on every exit path.
				iterate := func(elem core.Value) (broke bool) {
					ctx.PushFrame()
					defer ctx.PopFrame()
					// Loop variable is a per-iteration constant: the next
					// iteration will overwrite it anyway, and the LSP
					// marks it DefConst, so reassigning it is rejected
					// by Set rather than silently shadowed.
					ctx.Env.Stack[0][ident] = core.StackEntry{Val: elem, IsConstant: true}
					return callBody(bodyFunc)
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
					fmt.Println("(ERR) 'for' cannot iterate over a value of kind '" + col.Kind + "' @ 'builtins.for'.")
					return core.TvNil
				}

				return lastVal
			}

			fmt.Println("(ERR) 'for' must be supplied 2 or 3 arguments. Too many provided @ 'builtins.for'")

			return core.TvNil
		}),

		"do": global(func(ctx core.Context, argv []core.Node) core.Value {
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
			fmt.Println("(ERR) 'return' takes 0 or 1 arguments; got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.return'.")
			return core.TvNil
		}),

		"break": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 0 {
				fmt.Println("(ERR) 'break' takes no arguments; got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.break'.")
				return core.TvNil
			}
			panic(core.BreakSignal{})
		}),

		"continue": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 0 {
				fmt.Println("(ERR) 'continue' takes no arguments; got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.continue'.")
				return core.TvNil
			}
			panic(core.ContinueSignal{})
		}),
	}
}
