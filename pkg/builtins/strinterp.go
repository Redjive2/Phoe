package builtins

import (
	"strings"

	"pho/pkg/core"
)

// strinterpBuiltins returns the pair of mangled builtins that back
// the `"%name"` / `"%a.b.c"` / `"%(expr)"` interpolation surface.
// Both names are mangled in pkg/core/mangle.go so user code can't
// reach them by typing them out.
//
// The syntax lower pass emits, for `"hi %who at %(time)"`:
//
//	(Strinterp "hi " (Strcoerce who) " at " (Strcoerce time))
//
// Literal chunks pass through unwrapped; only the interpolated
// expressions go through Strcoerce so a non-string value (number,
// bool, etc.) is converted before Strinterp concatenates.
func strinterpBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		core.Strinterp: global(func(ctx core.Context, argv []core.Node) core.Value {
			var b strings.Builder
			for i, node := range argv {
				v := node.Evaluate(ctx)
				if v.Kind != core.KindStr {
					return ctx.Errorf(core.ErrType, "Strinterp arg #%d is kind '%s', not 'str' — every interpolated value should already be wrapped in Strcoerce", i, v.Kind)
				}
				b.WriteString(v.Val.(string))
			}
			return core.TvStr(b.String())
		}),

		core.Strcoerce: global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "Strcoerce takes exactly 1 argument; got %d", len(argv))
			}
			// Rendering lives in core.Stringify so the Go-interop layer
			// (fmt, debug) shares the exact same representation.
			return core.TvStr(core.Stringify(argv[0].Evaluate(ctx)))
		}),
	}
}
