package builtins

import (
	"fmt"

	"pho/pkg/core"
)

// arithBuiltins returns the arithmetic and comparison builtins:
//   numeric: + - * /
//   ordering: < <= > >=
//   equality: == ~=
func arithBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"+": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) == 0 {
				fmt.Println("(ERR) '+' requires at least one argument @ 'builtins.+'.")
				return core.TvNil
			}

			sum := argv[0].Evaluate(ctx)
			if sum.Kind != core.KindNum && sum.Kind != core.KindStr {
				fmt.Println("(ERR) '+' requires 'num' or 'str' arguments, got '" + sum.Kind + "' @ 'builtins.+'.")
				return core.TvNil
			}

			for _, arg := range argv[1:] {
				v := arg.Evaluate(ctx)
				if v.Kind != sum.Kind {
					fmt.Println("(ERR) '+' arg type '" + v.Kind + "' does not match first arg type '" + sum.Kind + "' @ 'builtins.+'.")
					return core.TvNil
				}
				if sum.Kind == core.KindNum {
					sum.Val = sum.Val.(float64) + v.Val.(float64)
				} else {
					sum.Val = sum.Val.(string) + v.Val.(string)
				}
			}

			return sum
		}),

		"-": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '-' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.-'.")
				return core.TvNil
			}
			a, ok := asNum(argv[0].Evaluate(ctx), "-")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(argv[1].Evaluate(ctx), "-")
			if !ok {
				return core.TvNil
			}
			return core.TvNum(a - b)
		}),

		"*": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '*' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.*'.")
				return core.TvNil
			}
			a, ok := asNum(argv[0].Evaluate(ctx), "*")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(argv[1].Evaluate(ctx), "*")
			if !ok {
				return core.TvNil
			}
			return core.TvNum(a * b)
		}),

		"/": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '/' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins./'.")
				return core.TvNil
			}
			a, ok := asNum(argv[0].Evaluate(ctx), "/")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(argv[1].Evaluate(ctx), "/")
			if !ok {
				return core.TvNil
			}
			return core.TvNum(a / b)
		}),

		"~=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '~=' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.~='.")
				return core.TvNil
			}
			return core.TvBool(!tvalEqual(argv[0].Evaluate(ctx), argv[1].Evaluate(ctx)))
		}),

		"==": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '==' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.=='.")
				return core.TvNil
			}
			return core.TvBool(tvalEqual(argv[0].Evaluate(ctx), argv[1].Evaluate(ctx)))
		}),

		"<=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '<=' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.<='.")
				return core.TvNil
			}
			a, ok := asNum(argv[0].Evaluate(ctx), "<=")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(argv[1].Evaluate(ctx), "<=")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a <= b)
		}),

		">=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '>=' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.>='.")
				return core.TvNil
			}
			a, ok := asNum(argv[0].Evaluate(ctx), ">=")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(argv[1].Evaluate(ctx), ">=")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a >= b)
		}),

		"<": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '<' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.<'.")
				return core.TvNil
			}
			a, ok := asNum(argv[0].Evaluate(ctx), "<")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(argv[1].Evaluate(ctx), "<")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a < b)
		}),

		">": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				fmt.Println("(ERR) '>' requires exactly 2 arguments, got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.>'.")
				return core.TvNil
			}
			a, ok := asNum(argv[0].Evaluate(ctx), ">")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(argv[1].Evaluate(ctx), ">")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a > b)
		}),
	}
}
