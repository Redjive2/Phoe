package builtins

import (
	"testing"

	"pho/pkg/core"
)

// TestNoneTrueFalseLiterals proves the new lowercase literal spellings
// (`none`/`true`/`false`) evaluate to the same runtime values as the
// capitalized forms, which stay accepted during the syntax migration. The
// expected strings use core.Stringify's still-capitalized rendering — the
// inspect/Stringify switch happens at the hard cutover, not in Phase 2
// (Doc/PlanV1/Syntax.md).
func TestNoneTrueFalseLiterals(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"none", "Nil"},
		{"true", "True"},
		{"false", "False"},
		// Capitalized forms still work transitionally.
		{"Nil", "Nil"},
		{"True", "True"},
		{"False", "False"},
	}
	for _, tc := range cases {
		if got := core.Stringify(evalProgram(t, tc.src)); got != tc.want {
			t.Errorf("eval %q -> %q, want %q", tc.src, got, tc.want)
		}
	}
}
