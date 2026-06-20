package builtins

import (
	"testing"

	"pho/pkg/core"
)

// `unless` is the opposite of `if`: a branch is taken when its condition is
// FALSE. It supports a trailing `else` but not `elif`. These run the full
// lex→parse→lower→eval pipeline (see evalProgram).
func TestUnless(t *testing.T) {
	cases := []struct {
		src  string
		want interface{}
	}{
		{"(unless False then 1)", float64(1)},        // false → take the branch
		{"(unless True then 1)", nil},                // true → Nil (no else)
		{"(unless True then 1 else 2)", float64(2)},  // true → else
		{"(unless False then 1 else 2)", float64(1)}, // false → then
	}
	for _, c := range cases {
		if got := evalProgram(t, c.src).Val; got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}

	// `elif` is rejected — unless has at most one condition.
	if _, codes := evalProgramDiag(t, "(unless False then 1 elif True then 2)"); !hasCode(codes, core.ErrBadForm) {
		t.Errorf("unless with elif should be a bad-form error, got %v", codes)
	}
}
