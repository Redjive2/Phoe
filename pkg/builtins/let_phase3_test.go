package builtins

import (
	"testing"

	"pho/pkg/core"
)

// TestLetAndReassignRuntime proves the new `let` / `let var` declarations and
// the infix `(name = value)` reassignment work end-to-end through the real
// pipeline — they desugar (at parse time) to const/var and prefix `=`, which
// the runtime already evaluates (Doc/PlanV1/Syntax.md, Phase 3).
func TestLetAndReassignRuntime(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"let binds a constant", "(let x = 5)\nx", "5"},
		{"let var binds mutable state", "(let var c = 1)\nc", "1"},
		{"reassign a let var with infix =", "(let var c = 1)\n(c = 2)\nc", "2"},
		{"infix = mutates an existing var", "(var n 0)\n(n = 7)\nn", "7"},
	}
	for _, tc := range cases {
		if got := core.Stringify(evalProgram(t, tc.src)); got != tc.want {
			t.Errorf("%s: eval %q -> %q, want %q", tc.name, tc.src, got, tc.want)
		}
	}
}
