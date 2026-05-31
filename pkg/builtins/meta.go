package builtins

import (
	"pho/pkg/core"
	"pho/pkg/syntax"
)

// metaBuiltins returns the code-as-data and reflection builtins:
// pause, resume, inspect, and the spread marker.
func metaBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		// noop; only used as a marker for the interpreter
		"spread": global(func(ctx core.Context, argv []core.Node) core.Value {
			return core.TvNil
		}),

		"resume": global(func(ctx core.Context, argv []core.Node) core.Value {
			val := argv[0].Evaluate(ctx)
			tree := syntax.TreeifyVal(val)
			node := syntax.Derepr(tree)
			block := core.BindCallback(node)
			return block(ctx, nil)
		}),

		"pause": global(func(ctx core.Context, argv []core.Node) core.Value {
			return syntax.ListifyVal(argv[0].Evaluate(ctx))
		}),

		"inspect": global(func(ctx core.Context, argv []core.Node) core.Value {
			return core.TvStr(core.Inspect(argv[0]))
		}),
	}
}
