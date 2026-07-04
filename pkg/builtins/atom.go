package builtins

import "pho/pkg/core"

// atomBuiltins returns the atom predicate, constructor, and accessor.
// Names are all `atom`-prefixed (valid Pho identifiers — `->` would lex as
// separate tokens) so they group together in completion:
//
//	(atom? x)         -> True if x is an atom
//	(atom "foo")      -> :foo   (errors if "foo" isn't a legal atom form)
//	(atom-name :foo)  -> "foo"
//
// `atom` mirrors the bare-word constructor convention of `slice` / `map`.
func atomBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"atom?": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'atom?' requires exactly 1 argument; got %d", len(argv))
			}
			return core.TvBool(argv[0].Evaluate(ctx).Kind == core.KindAtom)
		}),

		"atom": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'atom' requires exactly 1 argument; got %d", len(argv))
			}
			v := argv[0].Evaluate(ctx)
			if v.Kind != core.KindStr {
				return ctx.Errorf(core.ErrType, "'atom' expected a 'str' argument, got '%s'", v.Kind)
			}
			s := v.Val.(string)
			if !core.IsAtomName(s) {
				return ctx.Errorf(core.ErrBadLiteral, "'atom': '%s' is not a legal atom (must be an identifier or digits)", s)
			}
			return core.TvAtom(s)
		}),

		"atom-name": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'atom-name' requires exactly 1 argument; got %d", len(argv))
			}
			v := argv[0].Evaluate(ctx)
			if v.Kind != core.KindAtom {
				return ctx.Errorf(core.ErrType, "'atom-name' expected an 'atom' argument, got '%s'", v.Kind)
			}
			return core.TvStr(v.Val.(*core.Atom).Name())
		}),
	}
}
