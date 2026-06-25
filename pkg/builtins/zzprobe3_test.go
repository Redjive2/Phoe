package builtins

import "testing"

func TestProbeNestedMacro(t *testing.T) {
	v := evalProgram(t, `(macro ~dbl (x) ['+' x x])
(macro ~quad (x) ['~dbl' ['~dbl' x]])
(~quad 3)`)
	t.Logf("~quad 3 = kind=%s val=%v", v.Kind, v.Val)
}

func TestProbeMacroHygiene(t *testing.T) {
	// macro introduces a binding; should not leak to caller
	v := evalProgram(t, `(macro ~letx () ['const' 'tmp' '99'])
(~letx)
tmp`)
	t.Logf("after macro, tmp kind=%s val=%v", v.Kind, v.Val)
}
