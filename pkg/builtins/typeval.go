package builtins

import "pho/pkg/core"

// typeBuiltins installs the first-class type values and the runtime type
// operations of the set-theoretic gradual type system (Stage A1):
//
//	Number String List Map Boolean Char Atom Function NilT Type Unknown None
//	    -- type VALUES (KindType), usable in (x.Is? T)/subtype? and in --@
//	       annotations. Capitalized: they install in Globals and are never
//	       re-exported by a module nor shadowable.
//	(x.Is? T)        -- True iff x inhabits type T (membership). This is the ONLY
//	                    membership surface: a universal Go-native method on the
//	                    top type Unknown (see unknownIsMethod / builtinmod.go),
//	                    so the dot form is the only way to test a type — there is
//	                    deliberately no prefix `Is?` builtin.
//	(subtype? S T)   -- True iff every value of type S is a value of type T
//
// There is deliberately no `typeof`: in a set-theoretic system a value inhabits
// many types, so a single "the type of x" is misleading — ask membership
// questions with x.Is?/subtype? instead. (core.TvTypeOf stays internal, backing
// dispatch.)
//
// The nil type is bound as `NilT` (printing as "Nil"): the bare leaf `Nil` is
// intercepted by the evaluator as the nil literal before name resolution, so a
// `Nil` binding would be unreachable.
func typeBuiltins() map[string]core.StackEntry {
	konst := func(t *core.PhoType) core.StackEntry {
		return core.StackEntry{Val: core.TvType(t), IsConstant: true}
	}

	return map[string]core.StackEntry{
		"Number":     konst(core.TypeNumber),
		"String":     konst(core.TypeString),
		"List":       konst(core.TypeList),
		"Map":        konst(core.TypeDict),
		"Boolean":    konst(core.TypeBoolean),
		"Char":       konst(core.TypeChar),
		"Atom":       konst(core.TypeAtom),
		"Function":   konst(core.TypeFunction),
		"NilT":       konst(core.TypeNil),
		"Type":       konst(core.TypeType),
		"Unknown":    konst(core.TypeUnknown),
		"None":       konst(core.TypeNone),
		"Collection": konst(core.TypeCollection),
		"Dynamic":    konst(core.TypeDynamic),
		// Struct is the open-record base; `Struct.{ X Number Y Number }` (parsed
		// to a call (Struct "X" Number …)) refines it into a structural type.
		"Struct": konst(core.TypeStruct),
		// Trait is a structural, implicit interface — see trait.go. The
		// lowercase `trait` declares a NAMED one: `(trait Name member…)`.
		"Trait": global(traitBuiltin),
		"trait": global(traitNamedBuiltin),

		// Set-theoretic connectives. Prefix forms (not infix `|`/`~`) because a
		// type expression is evaluated Pho and multi-char operators don't lex
		// atomically. Variadic Or/And fold from the identity (None / Unknown).
		"Or": global(func(ctx core.Context, argv []core.Node) core.Value {
			acc := core.TypeNone
			for _, a := range argv {
				t, ok := asType(ctx, a.Evaluate(ctx), "Or")
				if !ok {
					return core.TvNil
				}
				acc = acc.Or(t)
			}
			return core.TvType(acc)
		}),

		"And": global(func(ctx core.Context, argv []core.Node) core.Value {
			acc := core.TypeUnknown
			for _, a := range argv {
				t, ok := asType(ctx, a.Evaluate(ctx), "And")
				if !ok {
					return core.TvNil
				}
				acc = acc.And(t)
			}
			return core.TvType(acc)
		}),

		"Not": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'Not' requires exactly 1 argument; got %d", len(argv))
			}
			t, ok := asType(ctx, argv[0].Evaluate(ctx), "Not")
			if !ok {
				return core.TvNil
			}
			return core.TvType(t.Not())
		}),

		"Diff": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'Diff' requires exactly 2 arguments (a, b); got %d", len(argv))
			}
			a, ok := asType(ctx, argv[0].Evaluate(ctx), "Diff")
			if !ok {
				return core.TvNil
			}
			b, ok := asType(ctx, argv[1].Evaluate(ctx), "Diff")
			if !ok {
				return core.TvNil
			}
			return core.TvType(a.Diff(b))
		}),

		// (Fun [P1 P2 …] R) — a function type. The first argument is a list of
		// parameter types, the second the result type. Subtyping is
		// contravariant in parameters, covariant in the result; runtime
		// membership only checks "is a function" (a closure has no signature
		// witness).
		"Fun": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'Fun' takes (Fun [params…] result); got %d argument(s)", len(argv))
			}
			pv := argv[0].Evaluate(ctx)
			if pv.Kind != core.KindArray {
				return ctx.Errorf(core.ErrType, "'Fun' first argument must be a list of parameter types, got '%s'", pv.Kind)
			}
			var params []*core.PhoType
			for _, el := range *pv.Val.(*[]core.Value) {
				t, ok := asType(ctx, el, "Fun")
				if !ok {
					return core.TvNil
				}
				params = append(params, t)
			}
			res, ok := asType(ctx, argv[1].Evaluate(ctx), "Fun")
			if !ok {
				return core.TvNil
			}
			return core.TvType(core.ArrowType(params, res))
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

// unknownIsMethod implements `(x.Is? T)` — the universal type-membership test —
// as a Go-native method on the top type Unknown (registered in builtinmod.go).
// It is the ONLY membership surface: there is no prefix `Is?` builtin, so a
// type can only be tested through the dot form. self is the receiver on the
// instance stack; the single argument is the type. Backed by core.Contains.
func unknownIsMethod(ctx core.Context, argv []core.Node) core.Value {
	if len(ctx.Env.InstStack) == 0 {
		return ctx.Errorf(core.ErrType, "'Is?' is a method; call it as (value.Is? T)")
	}
	if len(argv) != 1 {
		return ctx.Errorf(core.ErrArity, "'Is?' takes exactly 1 argument (the type); got %d", len(argv))
	}
	self := ctx.Env.InstStack[0]
	t, ok := asType(ctx, argv[0].Evaluate(ctx), "Is?")
	if !ok {
		return core.TvNil
	}
	// A trait checks structural satisfaction against the object-model member
	// tables (Context-aware); every other type uses pure value membership.
	if _, isTrait := core.TraitOf(t); isTrait {
		return core.TvBool(core.TraitSatisfiedBy(ctx, t, self))
	}
	return core.TvBool(t.Contains(self))
}

// asType asserts that v is usable as a type, reporting a diagnostic otherwise.
// A LITERAL value in a type position (atom, number, string, or bool) is coerced
// to its SINGLETON type, so a literal doubles as an enum member — `(x.Is? :ok)`,
// `(Or :ok :error)`, `(n.Is? 5)`, `(Or "GET" "POST")`, `(b.Is? True)`.
func asType(ctx core.Context, v core.Value, caller string) (*core.PhoType, bool) {
	switch v.Kind {
	case core.KindType:
		return v.Val.(*core.PhoType), true
	case core.KindAtom:
		return core.AtomSingleton(v.Val.(*core.Atom).Name()), true
	case core.KindNum:
		return core.NumSingleton(v.Val.(float64)), true
	case core.KindStr:
		return core.StrSingleton(v.Val.(string)), true
	case core.KindBool:
		return core.BoolSingleton(v.Val.(bool)), true
	case core.KindNil:
		// `Nil` is the sole value of NilT, so the nil literal in a type position
		// is the nil type — making `(x.Is? Nil)` the natural nil test.
		return core.TypeNil, true
	}
	ctx.Errorf(core.ErrType, "'%s' expected a type, got '%s'", caller, v.Kind)
	return nil, false
}
