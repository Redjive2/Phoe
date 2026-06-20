package builtins

import (
	"pho/pkg/syntax"
	"testing"
)

func TestProbePauseInternal(t *testing.T) {
	// What does pause produce?
	v := evalProgram(t, `(pause (slice "+" 1 2))`)
	t.Logf("pause result kind=%s val=%v", v.Kind, v.Val)
	// Now treeify it as resume would
	node := syntax.Derepr(syntax.TreeifyVal(v))
	t.Logf("derepr(treeify(pause)) = %#v", node)
}
