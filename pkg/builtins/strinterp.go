package builtins

import (
	"fmt"
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
					fmt.Println("(ERR) Strinterp arg #" + fmt.Sprint(i) +
						" is kind '" + v.Kind + "', not 'str' — every interpolated value should already be wrapped in Strcoerce @ 'builtins.Strinterp'.")
					return core.TvNil
				}
				b.WriteString(v.Val.(string))
			}
			return core.TvStr(b.String())
		}),

		core.Strcoerce: global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 1 {
				fmt.Println("(ERR) Strcoerce takes exactly 1 argument; got '" +
					fmt.Sprint(len(argv)) + "' @ 'builtins.Strcoerce'.")
				return core.TvNil
			}
			return core.TvStr(stringifyValue(argv[0].Evaluate(ctx)))
		}),
	}
}

// stringifyValue returns the string representation of a Value used by
// Strcoerce. Picks reasonable defaults for each Kind:
//
//   - str:  passed through verbatim (no requoting).
//   - num:  Go's default float formatting; whole numbers print
//           without a trailing ".0" (matches the rest of the language
//           and Go's %v).
//   - bool: "True" / "False" — matches the source-syntax atoms, not
//           Go's "true" / "false".
//   - nil:  "Nil".
//   - chr:  the rune as a one-rune string.
//   - array/dict: bracketed forms with recursive stringification,
//           mirroring the source-syntax sugar.
//   - fun/method/etc.: angle-bracketed type tag — these don't have a
//           natural source representation; better a stable placeholder
//           than something that pretends to round-trip.
func stringifyValue(v core.Value) string {
	switch v.Kind {
	case core.KindStr:
		return v.Val.(string)
	case core.KindNum:
		return fmt.Sprintf("%v", v.Val.(float64))
	case core.KindBool:
		if v.Val.(bool) {
			return "True"
		}
		return "False"
	case core.KindNil:
		return "Nil"
	case core.KindChr:
		return string(v.Val.(rune))
	case core.KindArray:
		items := *v.Val.(*[]core.Value)
		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = stringifyValue(item)
		}
		return "[" + strings.Join(parts, " ") + "]"
	case core.KindDict:
		items := *v.Val.(*map[core.Value]core.Value)
		parts := make([]string, 0, 2*len(items))
		for k, val := range items {
			parts = append(parts, stringifyValue(k), stringifyValue(val))
		}
		return "{" + strings.Join(parts, " ") + "}"
	}
	return "<" + v.Kind + ">"
}
