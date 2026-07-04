package builtins

import (
	"testing"

	"pho/pkg/core"
)

// A macro declared with (macro ~name (params) body) is invoked with the `~`
// prefix. Its body receives the QUOTED arguments and returns code as data,
// which the call site resumes — so (~twice n) expands to (+ n n).
func TestMacroDeclareAndCall(t *testing.T) {
	src := "(macro ~twice (e) ['+' e e])\n(let var n = 5)\n(~twice n)"
	v := evalProgram(t, src)
	if v.Kind != core.KindNum || v.Val.(float64) != 10 {
		t.Fatalf("(~twice n) = %v (kind %s), want 10", v.Val, v.Kind)
	}
}

// The `~` prefix is the macro-vs-function boundary at runtime: a macro called
// without `~` isn't callable (it's KindMacro, which the evaluator won't
// invoke), and a function called with `~` reaches Macrocall, which rejects a
// non-macro.
func TestMacroCallSyntaxEnforced(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(macro ~m (e) e)\n(m 1)"); !hasCode(codes, core.ErrNotCallable) {
		t.Errorf("calling a macro without '~' should be not-callable, got %v", codes)
	}
	if _, codes := evalProgramDiag(t, "(let f (e) = e)\n(~f 1)"); !hasCode(codes, core.ErrNotCallable) {
		t.Errorf("calling a function with '~' should error, got %v", codes)
	}
}

// A macro missing its `~` in the declaration is rejected.
func TestMacroDeclRequiresBang(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(macro m (e) e)"); !hasCode(codes, core.ErrBadForm) {
		t.Errorf("macro declared without '~' should be a bad-form error, got %v", codes)
	}
}
