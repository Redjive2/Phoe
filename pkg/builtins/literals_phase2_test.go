package builtins

import (
	"testing"

	"pho/pkg/core"
)

// TestNoneTrueFalseLiterals proves the value literals `none`/`true`/`false`
// evaluate to their runtime values and render lowercase. The capitalized forms
// Nil/True/False are no longer values — a bare one is an undefined identifier;
// only the lowercase spellings are valid references.
func TestNoneTrueFalseLiterals(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"none", "none"},
		{"true", "true"},
		{"false", "false"},
	}
	for _, tc := range cases {
		if got := core.Stringify(evalProgram(t, tc.src)); got != tc.want {
			t.Errorf("eval %q -> %q, want %q", tc.src, got, tc.want)
		}
	}
}
