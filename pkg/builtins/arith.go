package builtins

import (
	"math"
	"pho/pkg/core"
)

// arithBuiltins returns the arithmetic and comparison builtins:
//
//	numeric: + - * /
//	ordering: < <= > >=
//	equality: == ~=
func arithBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"+": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) == 0 {
				return ctx.Errorf(core.ErrArity, "'+' requires at least one argument")
			}

			sum := argv[0].Evaluate(ctx)
			if sum.Kind != core.KindNum && sum.Kind != core.KindStr {
				return ctx.Errorf(core.ErrType, "'+' requires 'num' or 'str' arguments, got '%s'", sum.Kind)
			}

			for _, arg := range argv[1:] {
				v := arg.Evaluate(ctx)
				if v.Kind != sum.Kind {
					return ctx.Errorf(core.ErrType, "'+' arg type '%s' does not match first arg type '%s'", v.Kind, sum.Kind)
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
				return ctx.Errorf(core.ErrArity, "'-' requires exactly 2 arguments, got %d", len(argv))
			}
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), "-")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), "-")
			if !ok {
				return core.TvNil
			}
			return core.TvNum(a - b)
		}),

		"*": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'*' requires exactly 2 arguments, got %d", len(argv))
			}
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), "*")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), "*")
			if !ok {
				return core.TvNil
			}
			return core.TvNum(a * b)
		}),

		"/": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'/' requires exactly 2 arguments, got %d", len(argv))
			}
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), "/")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), "/")
			if !ok {
				return core.TvNil
			}
			return core.TvNum(a / b)
		}),
		
		"mod": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'mod' requires exactly 2 arguments, got %d", len(argv))
			}
			
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), "mod")
			if !ok {
				return core.TvNil
			}
			
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), "mod")
			if !ok {
				return core.TvNil
			}
			
			return core.TvNum(math.Mod(a, b))
		}),

		"~=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'~=' requires exactly 2 arguments, got %d", len(argv))
			}
			return core.TvBool(!tvalEqual(argv[0].Evaluate(ctx), argv[1].Evaluate(ctx)))
		}),

		"==": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'==' requires exactly 2 arguments, got %d", len(argv))
			}
			return core.TvBool(tvalEqual(argv[0].Evaluate(ctx), argv[1].Evaluate(ctx)))
		}),

		"<=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'<=' requires exactly 2 arguments, got %d", len(argv))
			}
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), "<=")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), "<=")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a <= b)
		}),

		">=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'>=' requires exactly 2 arguments, got %d", len(argv))
			}
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), ">=")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), ">=")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a >= b)
		}),

		"<": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'<' requires exactly 2 arguments, got %d", len(argv))
			}
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), "<")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), "<")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a < b)
		}),

		">": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'>' requires exactly 2 arguments, got %d", len(argv))
			}
			a, ok := asNum(ctx, argv[0].Evaluate(ctx), ">")
			if !ok {
				return core.TvNil
			}
			b, ok := asNum(ctx, argv[1].Evaluate(ctx), ">")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(a > b)
		}),
	}
}
