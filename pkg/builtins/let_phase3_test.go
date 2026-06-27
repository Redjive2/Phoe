package builtins

import (
	"testing"

	"pho/pkg/core"
)

// TestLetAndReassignRuntime proves the first-class `let` / `let var` declarations
// and the prefix `(= name value)` reassignment work end-to-end through the real
// pipeline: `let` binds immutably, `let var` mutably, and `=` reassigns an
// existing binding (Doc/PlanV1/Syntax.md).
func TestLetAndReassignRuntime(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"let binds a constant", "(let x = 5)\nx", "5"},
		{"let var binds mutable state", "(let var c = 1)\nc", "1"},
		{"reassign a let var with prefix =", "(let var c = 1)\n(= c 2)\nc", "2"},
		{"prefix = mutates an existing var", "(let var n = 0)\n(= n 7)\nn", "7"},
	}
	for _, tc := range cases {
		if got := core.Stringify(evalProgram(t, tc.src)); got != tc.want {
			t.Errorf("%s: eval %q -> %q, want %q", tc.name, tc.src, got, tc.want)
		}
	}
}
