package syntax

import "testing"

// TestLetAndAssignDesugar pins the parse-time rewrites that make `let`/`let var`
// and the infix `(name = value)` form normalize to the existing const/var and
// prefix-`=` shapes (Doc/PlanV1/Syntax.md, Phase 3). The outer pair of parens
// in each `want` is Lower's top-level form wrapper.
func TestLetAndAssignDesugar(t *testing.T) {
	cases := []struct{ src, want string }{
		// let -> const, let var -> var.
		{"(let x = 1)", "((const x 1))"},
		{"(let var x = 1)", "((var x 1))"},
		// Multiple bindings.
		{"(let a = 1 b = 2)", "((const a 1 b 2))"},
		{"(let var a = 1 b = 2)", "((var a 1 b 2))"},
		// Infix assignment -> prefix `=`, including a dot target.
		{"(x = 1)", "((= x 1))"},
		{"(obj.#field = 1)", "((= obj.#field 1))"},
		// Already-prefix `=` and ordinary calls are left untouched.
		{"(= x 1)", "((= x 1))"},
		{"(f x y)", "((f x y))"},
	}
	for _, c := range cases {
		if got := lowerInspect(c.src); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, got, c.want)
		}
	}
}
