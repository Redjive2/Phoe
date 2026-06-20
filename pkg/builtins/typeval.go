package builtins

import "pho/pkg/core"

// typeBuiltins installs the first-class type values and the runtime type
// operations of the set-theoretic gradual type system (Stage A1):
//
//	Number String List Dict Boolean Char Atom Function NilT Type Unknown None
//	    -- type VALUES (KindType), usable in (typeof …)/Is?/subtype? and, later,
//	       in --@ annotations. Capitalized: they install in Globals and are
//	       never re-exported by a module nor shadowable.
//	(typeof x)       -- the most-precise type of x, e.g. (typeof "hi") == String
//	(Is? x T)        -- True iff x inhabits type T  (membership; prefix form —
//	                    the (x.Is? T) method form arrives with object-model dispatch)
//	(subtype? S T)   -- True iff every value of type S is a value of type T
//
// The nil type is bound as `NilT` (printing as "Nil"): the bare leaf `Nil` is
// intercepted by the evaluator as the nil literal before name resolution, so a
// `Nil` binding would be unreachable.
func typeBuiltins() map[string]core.StackEntry {
	konst := func(t *core.PhoType) core.StackEntry {
		return core.StackEntry{Val: core.TvType(t), IsConstant: true}
	}

	return map[string]core.StackEntry{
		"Number":   konst(core.TypeNumber),
		"String":   konst(core.TypeString),
		"List":     konst(core.TypeList),
		"Dict":     konst(core.TypeDict),
		"Boolean":  konst(core.TypeBoolean),
		"Char":     konst(core.TypeChar),
		"Atom":     konst(core.TypeAtom),
		"Function": konst(core.TypeFunction),
		"NilT":     konst(core.TypeNil),
		"Type":     konst(core.TypeType),
		"Unknown":  konst(core.TypeUnknown),
		"None":     konst(core.TypeNone),

		"typeof": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'typeof' requires exactly 1 argument; got %d", len(argv))
			}
			return core.TvType(core.TvTypeOf(argv[0].Evaluate(ctx)))
		}),

		"Is?": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'Is?' requires exactly 2 arguments (value, type); got %d", len(argv))
			}
			v := argv[0].Evaluate(ctx)
			t, ok := asType(ctx, argv[1].Evaluate(ctx), "Is?")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(t.Contains(v))
		}),

		"subtype?": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'subtype?' requires exactly 2 arguments (sub, super); got %d", len(argv))
			}
			s, ok := asType(ctx, argv[0].Evaluate(ctx), "subtype?")
			if !ok {
				return core.TvNil
			}
			t, ok := asType(ctx, argv[1].Evaluate(ctx), "subtype?")
			if !ok {
				return core.TvNil
			}
			return core.TvBool(core.Subtype(s, t))
		}),
	}
}

// asType asserts that v is a type value, reporting a diagnostic otherwise.
func asType(ctx core.Context, v core.Value, caller string) (*core.PhoType, bool) {
	if v.Kind != core.KindType {
		ctx.Errorf(core.ErrType, "'%s' expected a type, got '%s'", caller, v.Kind)
		return nil, false
	}
	return v.Val.(*core.PhoType), true
}
