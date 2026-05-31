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
func ListifyVal(val core.Value) core.Value {
	if val.Kind == core.KindStr {
		str := fmt.Sprint(val.Val)
		return core.TvStr("\"" + str + "\"")
	}

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

// TreeifyVal converts a runtime value back into an AST node. Used by
// `resume` to recover an executable tree from a value produced by
// `pause` / a macro return.
func TreeifyVal(val core.Value) core.Node {
	if str, ok := val.Val.(string); ok {
		return core.Leaf(str)
	} else if val.Kind == core.KindStr {
		return core.Leaf("\"" + fmt.Sprint(val.Val) + "\"")
	}

	list := *val.Val.(*[]core.Value)
	branch := make(core.Branch, len(list)+1)
	branch[0] = core.Leaf("list")

	for i := range list {
		branch[i+1] = TreeifyVal(list[i])
	}

	return branch
}

// Derepr is the inverse of the listify pass: strips the leading "slice"
// head added by quotation and unquotes string-literal leaves back into
// bare identifiers. Used by builtins (fun, method, var, =, etc.) to
// recover argument trees from quoted forms.
func Derepr(node core.Node) core.Node {
	if branch, ok := node.(core.Branch); ok {
		if len(branch) == 0 {
			return core.Branch{}
		}

		result := make(core.Branch, len(branch)-1)
		for i := 0; i < len(branch)-1; i++ {
			result[i] = Derepr(branch[i+1])
		}
		return result
	}

	lf := node.(core.Leaf)
	if lf[0] == '"' {
		lf = lf[1 : len(lf)-1]
	}
	return lf
}
