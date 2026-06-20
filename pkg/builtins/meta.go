package builtins

import (
	"pho/pkg/core"
	"pho/pkg/syntax"
)

// macroName recovers the macro's name from the lowered `(head 'args)`
// form `resume` is handed — i.e. the head identifier of `(name! ...)`.
// Returns "" when `resume` was invoked directly on a value (not via macro
// sugar), so the diagnostic can label the expansion generically.
func macroName(arg core.Node) string {
	if br, ok := core.AsBranch(arg); ok && len(br) > 0 {
		if lf, ok := core.AsLeaf(br[0]); ok {
			return string(lf)
		}
	}
	return ""
}

// resumeCode treeifies a code value, renders it with spans for
// expansion-aware diagnostics, and evaluates it in a fresh (hygienic) scope.
// Shared by the `resume` builtin and the Macrocall sugar.
//
// Generated code has no source of its own, so an error inside it would
// otherwise only show the macro call site. SynthSpans renders the generated
// tree and wraps its forms with spans into that text; evaluating the wrapped
// tree under an expansion context lets diagnostics caret the offending
// generated form. The fresh frame gives macro hygiene — bindings the
// expansion introduces stay local — and pops even if the body raises.
func resumeCode(ctx core.Context, name string, code core.Value) core.Value {
	node := syntax.Derepr(syntax.TreeifyVal(code))
	wrapped, text := core.SynthSpans(node)
	ectx := ctx.WithExpansion(name, text)
	ectx.PushFrame()
	defer ectx.PopFrame()
	return core.BindCallback(wrapped)(ectx, nil)
}

// metaBuiltins returns the code-as-data and reflection builtins:
// pause, resume, inspect, and the spread / optional markers.
func metaBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		// noop; only used as a marker for the interpreter
		"spread": global(func(ctx core.Context, argv []core.Node) core.Value {
			return core.TvNil
		}),

		// noop; only a parameter-list marker (parsed by parseArgList) for
		// an omittable trailing parameter that defaults to Nil.
		"optional": global(func(ctx core.Context, argv []core.Node) core.Value {
			return core.TvNil
		}),

		"resume": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'resume' requires exactly 1 argument; got %d", len(argv))
			}
			return resumeCode(ctx, macroName(argv[0]), argv[0].Evaluate(ctx))
		}),

		// Macrocall backs the `(name! arg ...)` macro-call sugar: it resolves
		// name to a macro, runs the macro body with the QUOTED argument nodes
		// (argv[1:]) — exactly as the old `(resume (name 'a 'b))` did — and
		// resumes the code the body returns. Refusing anything that isn't a
		// macro is what gives the `!` syntax meaning: a plain function called
		// with `!` lands here and errors.
		core.Macrocall: global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) < 1 {
				return ctx.Errorf(core.ErrArity, "macro call is missing its macro")
			}
			macroVal := argv[0].Evaluate(ctx)
			fn, ok := macroVal.Val.(core.Fun)
			if macroVal.Kind != core.KindMacro || !ok {
				return ctx.Errorf(core.ErrNotCallable,
					"'%s' is not a macro (it's a %s) — call it without the '!'", core.Inspect(argv[0]), macroVal.Kind)
			}
			return resumeCode(ctx, core.Inspect(argv[0]), fn(ctx, argv[1:]))
		}),

		"pause": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'pause' requires exactly 1 argument; got %d", len(argv))
			}
			return syntax.ListifyVal(argv[0].Evaluate(ctx))
		}),

		"inspect": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'inspect' requires exactly 1 argument; got %d", len(argv))
			}
			return core.TvStr(core.Inspect(argv[0]))
		}),

		"identity": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'identity' requires exactly 1 argument; got %d", len(argv))
			}
			return argv[0].Evaluate(ctx)
		}),
	}
}
