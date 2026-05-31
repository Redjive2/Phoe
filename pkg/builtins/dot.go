package builtins

import (
	"fmt"
	"math"
	"regexp"
	"unicode"

	"pho/pkg/core"
	"pho/pkg/goop"
)

// dotBuiltins returns a single entry: the mangled core.Dot accessor.
//
// The `a.b` surface syntax is rewritten by the parser into (core.Dot a b) where
// `core.Dot` is the randomized internal name from mangle.go. This builtin
// dispatches on the kind of the left-hand-side:
//
//   dict     — key lookup
//   array    — integer index, or [a:b] / [:b] / [a:] / [:] slice forms
//   str      — index into rune
//   instance — field access (with privacy check) or method dispatch
//   package  — uppercase export lookup
//   gopackage— Go-side method binding (returned as a wrapper core.Fun)
//   num      — fractional-decimal hack (e.g. 12 . 5 -> 12.5)
func dotBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		core.Dot: global(func(ctx core.Context, argv []core.Node) core.Value {
			col := argv[0].Evaluate(ctx)

			switch col.Kind {
			case core.KindDict:
				dict := *col.Val.(*map[core.Value]core.Value)
				key := argv[1].Evaluate(ctx)

				val, found := dict[key]
				if found {
					return val
				}

				return core.TvNil
			case core.KindArray:
				array := *col.Val.(*[]core.Value)

				if br, ok := argv[1].(core.Branch); ok && br[0] == core.Leaf("slice") {
					var (
						lhs int
						rhs int
					)

					// myList.[: b]
					if br[1] == core.Leaf(":") {
						lhs = 0
						rhs = int(br[2].Evaluate(ctx).Val.(float64))
						// myList.[a : b]
					} else if len(br) == 4 && br[2] == core.Leaf(":") {
						lhs = int(br[1].Evaluate(ctx).Val.(float64))
						rhs = int(br[3].Evaluate(ctx).Val.(float64))
						// myList.[a :]
					} else if len(br) == 3 && br[2] == core.Leaf(":") {
						lhs = int(br[1].Evaluate(ctx).Val.(float64))
						rhs = len(array)
					} else if len(br) == 2 && br[1] == core.Leaf(":") {
						lhs = 0
						rhs = len(array)
					} else {
						fmt.Println("(ERR): Invalid slicing syntax passed @ 'builtins.internal.dot'.")
						return core.TvNil
					}

					return core.TvSlice(array[lhs:rhs])
				}

				idx := int(argv[1].Evaluate(ctx).Val.(float64))

				if idx >= 0 && idx < len(array) {
					return array[idx]
				}

				return core.TvNil
			case core.KindStr:
				str := col.Val.(string)
				idx := int(argv[1].Evaluate(ctx).Val.(float64))

				if idx >= 0 && idx < len(str) {
					return core.TvChr(rune(str[idx]))
				}

				return core.TvNil
			case core.KindInstance:
				inst := col.Val.(*core.Instance)

				if lf, ok := argv[1].(core.Leaf); ok {
					ident := string(lf)

					if !regexp.MustCompile("^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))$").MatchString(ident) {
						fmt.Println("(ERR): Cannot index struct instance with non-identifier key '" + ident + "' @ 'builtins.internal.dot'.")
						return core.TvNil
					}

					if val, found := inst.Fields[ident]; found {
						if unicode.IsLower(rune(ident[0])) && !inst.Privileged {
							fmt.Println("(ERR): Cannot index struct instance with private key '" + ident + "' @ 'builtins.internal.dot'.")
							return core.TvNil
						}

						return val
					}

					// Resolve the method in the struct's origin env, not the
					// caller's env (the method's name might not be visible at
					// the call site).
					originCtx := ctx.WithEnv(inst.Struct.Origin)

					val, found := originCtx.Resolve(ident)
					if !found {
						fmt.Println("(ERR): Could not resolve method or field '" + ident + "' on struct instance @ 'builtins.internal.dot'.")
						return core.TvNil
					}
					if val.Kind != core.KindMethod {
						fmt.Println("(ERR): Could not find method or field '" + ident + "' on struct instance @ 'builtins.internal.dot'.")
						return core.TvNil
					}

					method := val.Val.(core.Method)

					if unicode.IsLower(rune(ident[0])) && !inst.Privileged {
						fmt.Println("(ERR): Cannot index struct instance with private key '" + ident + "' @ 'builtins.internal.dot'.")
						return core.TvNil
					}

					return core.TvFun(func(ctx core.Context, argv []core.Node) core.Value {
						env := ctx.Env
						env.InstStack = append([]core.Value{col}, env.InstStack...)
						defer func() {
							env.InstStack = env.InstStack[1:]
						}()
						return method.Fun(ctx, argv)
					})
				}

				fmt.Println("(ERR): Cannot dynamically index struct instance with expression '" + core.Inspect(argv[1]) + "' @ 'builtins.internal.dot'.")
				return core.TvNil

			case core.KindPackage:
				// (math.Add 1 2) -> ((read math Add) 1 2)
				pkg := col.Val.(*core.Package)

				if _, ok := argv[1].(core.Branch); ok {
					fmt.Println("(ERR): Package accessors must be unqualified identifiers: expected identifier, got call '" + core.Inspect(argv[0]) + "' @ 'builtins.internals.dot'.")
					return core.TvNil
				}

				if val, found := pkg.Exports[string(argv[1].(core.Leaf))]; found {
					if val.Kind == core.KindFun {
						return core.TvFun(val.Val.(core.Fun))
					}

					if val.Kind == core.KindConstructor {
						return core.TvFun(val.Val.(core.Constructor).Constructor)
					}

					fmt.Println("(ERR): export '" + string(argv[1].(core.Leaf)) + "' of kind '" + val.Kind + "' is not callable @ 'builtins.internal.dot'.")
					return core.TvNil
				}

				fmt.Println("(ERR): package '" + pkg.Path + "' has no exported member '" + string(argv[1].(core.Leaf)) + "' @ 'builtins.internal.dot'.")
				return core.TvNil
			case core.KindGoPackage:
				// (math.Add 1 2) -> ((read math Add) 1 2)
				gopkg := col.Val.(*goop.PhoModule)

				if _, ok := argv[1].(core.Branch); ok {
					fmt.Println("(ERR): Go package accessors must be unqualified identifiers: expected identifier, got call '" + core.Inspect(argv[0]) + "' @ 'builtins.internals.dot'.")
					return core.TvNil
				}

				funcName := string(argv[1].(core.Leaf))

				return core.TvFun(func(ctx core.Context, callArgv []core.Node) core.Value {
					args := core.DistributeSpreadExpressions(ctx, callArgv)

					return core.TvUnknown(goop.Call(gopkg, funcName, args))
				})
			case core.KindNum:
				rhs := argv[1].Evaluate(ctx)

				if rhs.Kind != core.KindNum {
					panic("uh oh. failed to transform a decimal @ 'builtins.internals.dot'")
				}

				var (
					lhs     = col.Val.(float64)
					n       = rhs.Val.(float64)
					digits  = len(fmt.Sprint(n))
					decimal = n / math.Pow(10, float64(digits))
				)

				// For a negative integer-part like `-5.5`, lhs evaluates to
				// -5 and the fractional part should subtract from it, not add.
				if lhs < 0 {
					return core.TvNum(lhs - decimal)
				}
				return core.TvNum(lhs + decimal)
			}

			fmt.Println("(ERR): Cannot index a value of type '" + col.Kind + "' @ 'builtins.internal.dot'.")
			return core.TvNil
		}),
	}
}
