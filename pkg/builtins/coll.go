package builtins

import (
	"pho/pkg/core"
)

// maxRangeSize bounds how many elements (range …) may allocate at once, so a
// single large numeric argument can't exhaust the host's memory. ~16M elements
// is far beyond any realistic loop bound while keeping the worst-case alloc to
// a few hundred MB.
const maxRangeSize = 1 << 24

// collBuiltins returns collection-construction and -access builtins:
// slice, map, get, has, len, append, drop, range. (Slicing and indexed
// reads also flow through the core.Dot accessor; these are the
// constructor / size / mutation primitives.)
func collBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		// (list? x) — True when x is a list (array). Lets code distinguish a
		// nested list from a scalar element, e.g. core.Flatten deciding
		// whether to splice or keep an element.
		"list?": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'list?' requires exactly 1 argument; got %d", len(argv))
			}
			return core.TvBool(argv[0].Evaluate(ctx).Kind == core.KindArray)
		}),

		"get": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'get' requires exactly 2 arguments (collection, key); got %d", len(argv))
			}

			col := argv[0].Evaluate(ctx)
			switch col.Kind {
			case core.KindDict:
				dict := *col.Val.(*map[core.Value]core.Value)
				key := argv[1].Evaluate(ctx)
				if !scalarKey(ctx, key, "get") {
					return core.TvNil
				}

				val, found := dict[key]
				if found {
					return val
				}
			case core.KindArray:
				array := *col.Val.(*[]core.Value)
				idx, ok := asNum(ctx, argv[1].Evaluate(ctx), "get")
				if !ok {
					return core.TvNil
				}

				if i, ok := numIndex(idx, len(array)); ok {
					return array[i]
				}
			case core.KindStr:
				str := col.Val.(string)
				idx, ok := asNum(ctx, argv[1].Evaluate(ctx), "get")
				if !ok {
					return core.TvNil
				}

				if i, ok := numIndex(idx, strLen(str)); ok {
					if r, rok := strRuneAt(str, i); rok {
						return core.TvChr(r)
					}
				}
			default:
				return ctx.Errorf(core.ErrType, "'get' cannot index a value of kind '%s'", col.Kind)
			}
			return core.TvNil
		}),

		"has": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'has' requires exactly 2 arguments (collection, key); got %d", len(argv))
			}

			col := argv[0].Evaluate(ctx)
			switch col.Kind {
			case core.KindDict:
				dict := *col.Val.(*map[core.Value]core.Value)
				key := argv[1].Evaluate(ctx)
				if !scalarKey(ctx, key, "has") {
					return core.TvNil
				}

				_, found := dict[key]
				return core.TvBool(found)
			case core.KindArray:
				array := *col.Val.(*[]core.Value)
				idx, ok := asNum(ctx, argv[1].Evaluate(ctx), "has")
				if !ok {
					return core.TvNil
				}

				_, in := numIndex(idx, len(array))
				return core.TvBool(in)
			case core.KindStr:
				str := col.Val.(string)
				idx, ok := asNum(ctx, argv[1].Evaluate(ctx), "has")
				if !ok {
					return core.TvNil
				}

				_, in := numIndex(idx, strLen(str))
				return core.TvBool(in)
			}
			return ctx.Errorf(core.ErrType, "'has' cannot index a value of kind '%s'", col.Kind)
		}),

		"drop": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'drop' requires exactly 2 arguments (array, count); got %d", len(argv))
			}

			arrayVal := argv[0].Evaluate(ctx)
			arrayPtr, ok := arrayVal.Val.(*[]core.Value)
			if !ok {
				return ctx.Errorf(core.ErrType, "'drop' expects an array, got kind '%s'", arrayVal.Kind)
			}

			nf, ok := asNum(ctx, argv[1].Evaluate(ctx), "drop")
			if !ok {
				return core.TvNil
			}
			n, ok := asCount(ctx, nf, "drop")
			if !ok {
				return core.TvNil
			}
			if n < 0 || n > len(*arrayPtr) {
				return ctx.Errorf(core.ErrIndexRange, "'drop' count %d out of range for array of length %d", n, len(*arrayPtr))
			}

			// Copy so the result doesn't alias the input's backing array
			// (a later append to one must not mutate the other).
			rest := append([]core.Value{}, (*arrayPtr)[n:]...)
			return core.TvSlice(rest)
		}),

		"append": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) < 1 {
				return ctx.Errorf(core.ErrArity, "'append' requires at least 1 argument (array, items...); got 0")
			}

			arrayVal := argv[0].Evaluate(ctx)
			arrayPtr, ok := arrayVal.Val.(*[]core.Value)
			if !ok {
				return ctx.Errorf(core.ErrType, "'append' expects an array, got kind '%s'", arrayVal.Kind)
			}

			// Copy so the result doesn't alias the input's backing array.
			array := make([]core.Value, len(*arrayPtr), len(*arrayPtr)+len(argv)-1)
			copy(array, *arrayPtr)

			for _, arg := range argv[1:] {
				array = append(array, arg.Evaluate(ctx))
			}

			return core.TvSlice(array)
		}),

		"len": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'len' requires exactly 1 argument; got %d", len(argv))
			}

			col := argv[0].Evaluate(ctx)
			switch col.Kind {
			case core.KindArray:
				return core.TvNum(float64(len(*col.Val.(*[]core.Value))))
			case core.KindStr:
				return core.TvNum(float64(strLen(col.Val.(string))))
			case core.KindDict:
				return core.TvNum(float64(len(*col.Val.(*map[core.Value]core.Value))))
			}
			return ctx.Errorf(core.ErrType, "'len' cannot measure a value of kind '%s'", col.Kind)
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
				return ctx.Errorf(core.ErrArity, "'map' requires an even number of arguments (key/value pairs); got %d", len(argv))
			}

			result := map[core.Value]core.Value{}

			for i := 0; i < len(argv); i += 2 {
				key := argv[i].Evaluate(ctx)
				if !scalarKey(ctx, key, "map") {
					return core.TvNil
				}
				val := argv[i+1].Evaluate(ctx)

				result[key] = val
			}

			return core.TvDict(result)
		}),

		"keyof": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'keyof' requires 1 argument; got %d", len(argv))
			}

			coll := argv[0].Evaluate(ctx)

			switch coll.Kind {
			case core.KindArray: // return a list of all indices `[0 1 2 3 4 ... len(coll.Val.([]core.Tval))]`
				size := len(*coll.Val.(*[]core.Tval))
				result := make([]core.Tval, size)

				for i := range size {
					result[i] = core.TvNum(float64(i))
				}

				return core.TvSlice(result)

			case core.KindDict: // return a list of all keys
				size := len(*coll.Val.(*map[core.Value]core.Value))
				result := make([]core.Tval, size)

				i := 0
				for key := range *coll.Val.(*map[core.Value]core.Value) {
					result[i] = key
					i++
				}

				return core.TvSlice(result)
			}

			ctx.Errorf(core.ErrType, "'keyof' expected array or dict, got %s", coll.Kind)
			return core.TvNil
		}),

		"range": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) == 0 || len(argv) > 2 {
				return ctx.Errorf(core.ErrArity, "'range' requires either 1 or 2 arguments; got %d", len(argv))
			}

			var (
				min int
				max int
			)

			if len(argv) == 1 {
				nf, ok := asNum(ctx, argv[0].Evaluate(ctx), "range")
				if !ok {
					return core.TvNil
				}
				n, ok := asCount(ctx, nf, "range")
				if !ok {
					return core.TvNil
				}
				min, max = 0, n
			} else {
				af, ok1 := asNum(ctx, argv[0].Evaluate(ctx), "range")
				bf, ok2 := asNum(ctx, argv[1].Evaluate(ctx), "range")
				if !ok1 || !ok2 {
					return core.TvNil
				}
				a, ok1 := asCount(ctx, af, "range")
				b, ok2 := asCount(ctx, bf, "range")
				if !ok1 || !ok2 {
					return core.TvNil
				}
				min, max = a, b
			}

			// An empty window is a valid request — (range 0) in a loop
			// header shouldn't error, just produce nothing.
			size := max - min
			if size < 0 {
				size = 0
			}

			// Cap the allocation: a single large number must not let user code
			// OOM-kill the host. The makeslice panic for an enormous size is
			// recovered, but a merely-huge valid size would exhaust memory first.
			if size > maxRangeSize {
				return ctx.Errorf(core.ErrIndexRange, "'range' size %d exceeds the maximum of %d", size, maxRangeSize)
			}

			result := make([]core.Tval, size)

			for i := 0; i < size; i++ {
				result[i] = core.TvNum(float64(i + min))
			}

			return core.TvSlice(result)
		}),
	}
}
