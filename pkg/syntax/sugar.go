package syntax

import (
	"fmt"

	"pho/pkg/core"
)

// Runtime helpers for the quote/macro system. The four sugar passes
// that legacy syntax.Parse used to run (CompressBlock/Dot/Macro/Code +
// ListifyTree) are gone — pkg/syntax/lower.go does the equivalent
// transforms in one pass on PNodes. The functions kept here operate on
// already-lowered trees or on runtime values, and are still called by
// the builtins (decl.go, ctrl.go, meta.go).

// ListifyVal is the runtime counterpart of the legacy ListifyTree pass:
// converts an array-of-values back into the (slice "stringified" ...)
// shape that the quote system uses, recursively. Called by `pause` to
// re-quote macro arguments before handing them to `resume`.
//
// In quote-land every scalar is the string of its source text (the
// parser's listify pass wraps leaves the same way), so a num 1 becomes
// "1", True becomes "True", and a real string keeps its quotes — each
// round-trips through `resume` back to the original value.
func ListifyVal(val core.Value) core.Value {
	switch val.Kind {
	case core.KindStr:
		str := fmt.Sprint(val.Val)
		return core.TvStr("\"" + str + "\"")
	case core.KindChr:
		return core.TvStr("`" + string(val.Val.(rune)) + "`")
	case core.KindArray:
		var (
			list    = *val.Val.(*[]core.Value)
			newList = make([]core.Value, len(list)+1)
		)

		newList[0] = core.TvStr("slice")

		for i := range list {
			newList[i+1] = ListifyVal(list[i])
		}

		return core.TvSlice(newList)
	}

	return core.TvStr(core.Stringify(val))
}

// TreeifyVal converts a runtime value back into an AST node. Used by
// `resume` to recover an executable tree from a value produced by
// `pause` / a macro return. Scalars become their source-text leaf, so
// non-quoted values round-trip the same way ListifyVal's do.
func TreeifyVal(val core.Value) core.Node {
	if str, ok := val.Val.(string); ok {
		// Add one quote level, mirroring ListifyVal, so Derepr's single
		// strip recovers the value rather than over-stripping it: an
		// identifier "x" → leaf "x" → x, but a string literal whose value
		// already carries quotes (`"x"`) → leaf ""x"" → "x". Without the
		// extra level a string literal in a resumed macro expansion would
		// collapse into a bare identifier (and then fail to resolve).
		return core.Leaf("\"" + str + "\"")
	}

	if val.Kind == core.KindChr {
		// Mirror ListifyVal's char branch: a char must round-trip as the
		// backtick literal `c`, not the bare rune text — otherwise Stringify
		// yields the leaf `c` (a bare identifier) and a char flowing through
		// resume / a macro expansion resolves as an undefined variable.
		return core.Leaf("`" + string(val.Val.(rune)) + "`")
	}

	if val.Kind != core.KindArray {
		return core.Leaf(core.Stringify(val))
	}

	list := *val.Val.(*[]core.Value)
	branch := make(core.Branch, len(list)+1)
	// "slice" is the array-literal head the rest of the quote system uses
	// (see ListifyVal above and the parser's listify pass); "list" is not a
	// builtin, so resuming a paused branch headed by it would not evaluate.
	branch[0] = core.Leaf("slice")

	for i := range list {
		branch[i+1] = TreeifyVal(list[i])
	}

	return branch
}

// Derepr is the inverse of the listify pass: strips the leading "slice"
// head added by quotation and unquotes string-literal leaves back into
// bare identifiers. Used by builtins (fun, method, var, =, etc.) to
// recover argument trees from quoted forms.
//
// Span wrappers (stamped onto quoted forms by listifyP) transfer onto
// the reconstructed tree, so a fun/method body recovered from its
// quoted form carries the same positions as directly-lowered code.
func Derepr(node core.Node) core.Node {
	if sp, ok := core.SpanOf(node); ok {
		return core.WithSpan(Derepr(core.Strip(node)), sp)
	}
	if branch, ok := node.(core.Branch); ok {
		if len(branch) == 0 {
			return core.Branch{}
		}

		result := make(core.Branch, len(branch)-1)
		for i := 0; i < len(branch)-1; i++ {
			result[i] = Derepr(branch[i+1])
		}
		// Apply `do` notation to call forms recovered from quoted data
		// (resume, macros), mirroring the parser's splitDoForm: a bare `do`
		// recovered here would otherwise stay an unresolved identifier
		// instead of sequencing via core.Do. Slice/map literals (the
		// [..]/{..} sugar) aren't call forms, so — like the parser — leave
		// them alone.
		if len(result) > 0 {
			if head, ok := core.AsLeaf(result[0]); !ok || (string(head) != "slice" && string(head) != "map") {
				result = splitDoNode(result)
			}
		}
		return result
	}

	lf := node.(core.Leaf)
	if len(lf) >= 2 && lf[0] == '"' && lf[len(lf)-1] == '"' {
		lf = lf[1 : len(lf)-1]
	}
	// A leading quote sigil marks a quoted symbol carried as macro data —
	// the idiomatic data form, e.g. "'name" for `'name`, which keeps the
	// quote level *visible* in the array (a `'name` element is one level
	// above a bare `name` identifier). Re-wrap it as a string literal: a
	// bare `name` stays the identifier `name`, but `'name` resolves to the
	// symbol "name" — what fun/var/=/method want as a name, and what a
	// value position sees as the string. Matches what `'name` produces in
	// source; one Derepr peels one level, so `''x` round-trips too.
	if len(lf) >= 1 && lf[0] == '\'' {
		return core.Leaf("\"" + string(lf[1:]) + "\"")
	}
	return lf
}

// isDoBoundaryNode is the core.Node counterpart of isDoBoundary
// (pkg/syntax/lower.go): a bare `elif`/`else` keyword leaf that terminates a
// `do`-arm's capture, so a macro-generated if/unless splits its arms the same
// way source does.
func isDoBoundaryNode(n core.Node) bool {
	lf, ok := core.AsLeaf(n)
	return ok && (string(lf) == "elif" || string(lf) == "else")
}

// splitDoNode is the runtime counterpart of splitDoForm (pkg/syntax/lower.go):
// it applies `do` notation to a call form reconstructed from quoted data, so
// a `do` recovered by resume or a macro sequences via the mangled core.Do
// primitive instead of staying a bare `do` identifier that resolves to
// nothing. A bare `do` element captures the siblings after it into one
// (core.Do …) call; at head position it is renamed in place, so `(do x y)`
// becomes `(core.Do x y)` directly rather than a call on the block's result.
//
// Like splitDoForm, the capture is context-aware: a non-head `do` stops at the
// first `elif`/`else` boundary so each if/unless arm becomes its own block,
// and everything from the boundary onward is rescanned for further arms.
// Callers exclude slice/map literals, which the parser doesn't do-split. An
// already-mangled core.Do head is left untouched, so a paused-then-resumed
// form isn't double-split.
func splitDoNode(children core.Branch) core.Branch {
	for i, c := range children {
		if lf, ok := core.AsLeaf(c); !ok || string(lf) != "do" {
			continue
		}

		// Head `do`: rename in place — the form IS the (core.Do …) call. A
		// leading `do` is a standalone block, so it captures to the end.
		if i == 0 {
			rest := children[i+1:]
			out := make(core.Branch, 0, len(rest)+1)
			out = append(out, core.Leaf(core.Do))
			out = append(out, rest...)
			return out
		}

		// Non-head `do`: capture siblings up to the first boundary keyword
		// into a (core.Do …) sub-call.
		endIdx := len(children)
		for j := i + 1; j < len(children); j++ {
			if isDoBoundaryNode(children[j]) {
				endIdx = j
				break
			}
		}
		rest := children[i+1 : endIdx]
		tail := children[endIdx:]

		doForm := make(core.Branch, 0, len(rest)+1)
		doForm = append(doForm, core.Leaf(core.Do))
		doForm = append(doForm, rest...)

		// The boundary keyword and the rest stay siblings; rescan them so a
		// later arm's `do` (the elif/else branch) splits too.
		out := make(core.Branch, 0, i+1+len(tail))
		out = append(out, children[:i]...)
		out = append(out, doForm)
		out = append(out, splitDoNode(tail)...)
		return out
	}
	return children
}
