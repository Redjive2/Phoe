package builtins

import "testing"

func TestProbeMacroNoPause(t *testing.T) {
	// macro body returns the code array directly (no pause)
	v := evalProgram(t, `(macro ~add (x) ['+' x x])
(~add 5)`)
	t.Logf("~add 5 = kind=%s val=%v", v.Kind, v.Val)
}

func TestProbeMacroPlain(t *testing.T) {
	v := evalProgram(t, `(macro ~inc (x) ['+' x '1'])
(~inc 5)`)
	t.Logf("~inc 5 = kind=%s val=%v", v.Kind, v.Val)
}
