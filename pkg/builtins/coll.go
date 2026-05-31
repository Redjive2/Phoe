package builtins

import (
	"fmt"

	"pho/pkg/core"
)

// collBuiltins returns collection-construction and -access builtins:
// slice, map, get, has, len, append, drop, range. (Slicing and indexed
// reads also flow through the core.Dot accessor; these are the
// constructor / size / mutation primitives.)
func collBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"get": global(func(ctx core.Context, argv []core.Node) core.Value {
			col := argv[0].Evaluate(ctx)
			switch col.Kind {
			case core.KindDict:
				dict := *col.Val.(*map[core.Value]core.Value)
				key := argv[1].Evaluate(ctx)

				val, found := dict[key]
				if found {
					return val
				}
			case core.KindArray:
				array := *col.Val.(*[]core.Value)
				idx := int(argv[1].Evaluate(ctx).Val.(float64))

				if idx < len(array) && idx > 0 {
					return array[idx]
				}
			case core.KindStr:
				str := col.Val.(string)
				idx := int(argv[1].Evaluate(ctx).Val.(float64))

				if idx < len(str) && idx > 0 {
					return core.TvChr(rune(str[idx]))
				}

			}
			return core.TvNil
		}),

		"has": global(func(ctx core.Context, argv []core.Node) core.Value {
			col := argv[0].Evaluate(ctx)
			switch col.Kind {
			case core.KindDict:
				dict := *col.Val.(*map[core.Value]core.Value)
				key := argv[1].Evaluate(ctx)

				_, found := dict[key]
				if found {
					return core.TvBool(true)
				}
			case core.KindArray:
				array := *col.Val.(*[]core.Value)
				idx := int(argv[1].Evaluate(ctx).Val.(float64))

				if idx < len(array) && idx > 0 {
					return core.TvBool(true)
				}
			case core.KindStr:
				str := col.Val.(string)
				idx := int(argv[1].Evaluate(ctx).Val.(float64))

				if idx < len(str) && idx > 0 {
					return core.TvBool(true)
				}

			}
			return core.TvBool(false)
		}),

		"drop": global(func(ctx core.Context, argv []core.Node) core.Value {
			array := argv[0].Evaluate(ctx)

			return core.TvSlice((*array.Val.(*[]core.Value))[int(argv[1].Evaluate(ctx).Val.(float64)):])
		}),

		"append": global(func(ctx core.Context, argv []core.Node) core.Value {
			var (
				arrayVal = argv[0].Evaluate(ctx)
				array    = *arrayVal.Val.(*[]core.Value)
			)

			for _, arg := range argv[1:] {
				array = append(array, arg.Evaluate(ctx))
			}

			return core.TvSlice(array)
		}),

		"len": global(func(ctx core.Context, argv []core.Node) core.Value {
			length := len(*argv[0].Evaluate(ctx).Val.(*[]core.Value))
			return core.TvNum(float64(length))
		}),

		"slice": global(func(ctx core.Context, argv []core.Node) core.Value {
			var result []core.Value

			for _, entry := range argv {
				result = append(result, entry.Evaluate(ctx))
			}

			return core.TvSlice(result)
		}),

		"map": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv)%2 != 0 {
				fmt.Println("(ERR) 'map' requires an even number of arguments (key/value pairs); got '" + fmt.Sprint(len(argv)) + "' @ 'builtins.map'.")
				return core.TvNil
			}

			result := map[core.Value]core.Value{}

			for i := 0; i < len(argv); i += 2 {
				key := argv[i].Evaluate(ctx)
				val := argv[i+1].Evaluate(ctx)

				result[key] = val
			}

			return core.TvDict(result)
		}),

		"range": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) == 0 || len(argv) > 2 {
				fmt.Println("(ERR) 'range' requires either 1 or 2 arguments; got ", fmt.Sprint(len(argv)))
			}

			var (
				min int
				max int
			)

			if len(argv) == 1 {
				min = 0
				max = int(argv[0].Evaluate(ctx).Val.(float64))
			} else {
				min = int(argv[0].Evaluate(ctx).Val.(float64))
				max = int(argv[1].Evaluate(ctx).Val.(float64))
			}

			var (
				size   = max - min
				result = make([]core.Tval, size)
			)

			for i := 0; i < size; i++ {
				result[i] = core.TvNum(float64(i + min))
			}

			return core.TvSlice(result)
		}),
	}
}
