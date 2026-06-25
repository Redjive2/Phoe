package builtins

import "pho/pkg/core"

// Traits are structural, implicit interface TYPES (see Doc/PlanV1/Traits.md).
// Two surface forms build the same trait type:
//
//	(Trait [(extends…)] member…)        — an anonymous trait value
//	(trait Name [(extends…)] member…)   — a NAMED trait, bound to Name
//
// The extends-list is OPTIONAL: a leading parenthesized group whose head is NOT
// a member keyword (method/property/static) is the extends-list (use `()` or
// omit it for none); otherwise every argument is a member. Each member is a
// `(method Self.Name (self args…) [body])`, `(property Self.Name get [impl] [set
// [impl]])`, or `(static method/property …)` form. `Self` is the receiver
// placeholder; a method/property body is its auto-injected DEFAULT.

// traitBuiltin implements the anonymous `(Trait …)` form. Bind the result with
// `(type Name (Trait …))`, or use the named `(trait Name …)` form directly.
func traitBuiltin(ctx core.Context, argv []core.Node) core.Value {
	extends, members := splitTraitArgs(argv)
	info, ok := buildTraitInfo(ctx, extends, members)
	if !ok {
		return core.TvNil
	}
	return core.TvType(core.TraitType(info))
}

// traitNamedBuiltin implements `(trait Name [(extends…)] member…)` — the
// `(type Name (Trait …))` shorthand: it builds the trait and binds Name to it.
func traitNamedBuiltin(ctx core.Context, argv []core.Node) core.Value {
	if len(argv) < 1 {
		return ctx.Errorf(core.ErrArity, "'trait' needs a name: (trait Name member…)")
	}
	name, ok := declName(ctx, argv[0], "trait", "name")
	if !ok {
		return core.TvNil
	}
	extends, members := splitTraitArgs(argv[1:])
	info, ok := buildTraitInfo(ctx, extends, members)
	if !ok {
		return core.TvNil
	}
	if !ctx.Rebind(name, core.TvType(core.TraitType(info)), true) {
		return ctx.Errorf(core.ErrRedeclare, "'trait' cannot shadow the builtin '%s'", name)
	}
	return core.TvNil
}

// splitTraitArgs separates an optional leading extends-list from the member
// forms. The extends-list is a parenthesized group whose head is NOT a member
// keyword — so `()` and `(Drawable …)` are extends, while `(method …)` is a
// member.
func splitTraitArgs(argv []core.Node) (extends []core.Node, members []core.Node) {
	if len(argv) > 0 {
		if br, ok := core.AsBranch(argv[0]); ok && !isTraitMemberForm(br) {
			refs := make([]core.Node, len(br))
			for i := range br {
				refs[i] = br[i]
			}
			return refs, argv[1:]
		}
	}
	return nil, argv
}

// isTraitMemberForm reports whether a parenthesized group is a trait MEMBER
// (a method/property/static declaration) rather than the extends-list.
func isTraitMemberForm(br core.Branch) bool {
	if len(br) == 0 {
		return false
	}
	head, _ := core.AsLeaf(br[0])
	switch string(head) {
	case "method", "property", "static":
		return true
	}
	return false
}

// buildTraitInfo flattens the extended traits' requirements and adds each
// member declaration, returning the trait's requirement set.
func buildTraitInfo(ctx core.Context, extends []core.Node, members []core.Node) (*core.TraitInfo, bool) {
	info := &core.TraitInfo{
		Methods:    map[string]core.TraitMethod{},
		Properties: map[string]core.TraitProperty{},
	}
	for _, ref := range extends {
		tv := ref.Evaluate(ctx)
		if tv.Kind != core.KindType {
			ctx.Errorf(core.ErrType, "a trait can only extend other traits")
			return nil, false
		}
		ti, isTrait := core.TraitOf(tv.Val.(*core.PhoType))
		if !isTrait {
			ctx.Errorf(core.ErrType, "a trait can only extend other traits, got '%s'", tv.Val.(*core.PhoType).Name())
			return nil, false
		}
		for k, v := range ti.Methods {
			info.Methods[k] = v
		}
		for k, v := range ti.Properties {
			info.Properties[k] = v
		}
	}
	for _, sub := range members {
		if !addTraitMember(ctx, info, sub) {
			return nil, false
		}
	}
	return info, true
}

// addTraitMember parses one trait member form into the requirement set. A
// `static method`/`static property` member is recognized and validated, but its
// requirement is not yet ENFORCED (it parses cleanly so a trait can declare
// type-level members; enforcement lands with static-requirement checking).
func addTraitMember(ctx core.Context, info *core.TraitInfo, sub core.Node) bool {
	br, ok := core.AsBranch(sub)
	if !ok || len(br) < 2 {
		ctx.Errorf(core.ErrBadForm, "trait members must be (method …), (property …), or (static …) forms")
		return false
	}
	head, _ := core.AsLeaf(br[0])
	switch string(head) {
	case "method":
		_, name, named, ok := methodTarget(ctx, br[1])
		if !ok {
			return false
		}
		if !named {
			ctx.Errorf(core.ErrBadForm, "a trait method needs a 'Self.Name' receiver")
			return false
		}
		argList, ok := parseArgList(ctx, br[2], "trait method")
		if !ok || len(argList) == 0 {
			ctx.Errorf(core.ErrBadForm, "trait method '%s' needs a self receiver", name)
			return false
		}
		m := core.TraitMethod{Arity: len(argList) - 1}
		if len(br) >= 4 { // a body ⇒ default implementation
			m.Default = core.BindMethod("Self."+name, br[3], argList, ctx)
		}
		info.Methods[name] = m
		return true

	case "property":
		_, name, named, ok := methodTarget(ctx, br[1])
		if !ok {
			return false
		}
		if !named {
			ctx.Errorf(core.ErrBadForm, "a trait property needs a 'Self.Name' receiver")
			return false
		}
		p := core.TraitProperty{}
		for i := 2; i < len(br); {
			kw, _ := core.AsLeaf(br[i])
			switch string(kw) {
			case "get":
				p.Get = true
				i++
				if i < len(br) {
					if fn, ok := traitImplFun(ctx, br[i]); ok {
						p.GetDefault = fn
						i++
					}
				}
			case "set":
				p.Set = true
				i++
				if i < len(br) {
					if fn, ok := traitImplFun(ctx, br[i]); ok {
						p.SetDefault = fn
						i++
					}
				}
			default:
				i++
			}
		}
		info.Properties[name] = p
		return true

	case "static":
		// (static method Self.Name …) / (static property Self.Name …) — a
		// type-level requirement. Validate that the inner form names a member,
		// then accept it (enforcement deferred).
		if inner, ok := core.AsLeaf(br[1]); ok {
			switch string(inner) {
			case "method", "property":
				return true
			}
		}
		ctx.Errorf(core.ErrBadForm, "a 'static' trait member must be 'static method …' or 'static property …'")
		return false

	default:
		ctx.Errorf(core.ErrBadForm, "trait members must be (method …), (property …), or (static …), got '%s'", string(head))
		return false
	}
}

// traitImplFun parses a `(method <recv> (args) body)` default-implementation
// form into its callable, or ok=false if form is not such a method. The
// receiver (`Self`) is a placeholder and is not evaluated.
func traitImplFun(ctx core.Context, form core.Node) (core.Fun, bool) {
	br, ok := core.AsBranch(form)
	if !ok || len(br) < 4 {
		return nil, false
	}
	if head, ok := core.AsLeaf(br[0]); !ok || string(head) != "method" {
		return nil, false
	}
	argList, ok := parseArgList(ctx, br[2], "trait default")
	if !ok || len(argList) == 0 {
		return nil, false
	}
	return core.BindMethod("trait-default", br[3], argList, ctx), true
}
