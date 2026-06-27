package syntax

import "testing"

// TestLetAndAssignForms pins the first-class `let` / `let var` declaration forms
// and the prefix `(= name value)` reassignment: none are rewritten at parse time
// any more — `let` is a real builtin (handled by the runtime + linter) and `=`
// is prefix-only (Doc/PlanV1/Syntax.md). The outer pair of parens in each `want`
// is Lower's top-level form wrapper.
func TestLetAndAssignForms(t *testing.T) {
	cases := []struct{ src, want string }{
		// let / let var pass through unchanged — first-class, not desugared.
		{"(let x = 1)", "((let x = 1))"},
		{"(let var x = 1)", "((let var x = 1))"},
		// Multiple bindings.
		{"(let a = 1 b = 2)", "((let a = 1 b = 2))"},
		{"(let var a = 1 b = 2)", "((let var a = 1 b = 2))"},
		// Reassignment is prefix `=`, including a dot target — left as written.
		{"(= x 1)", "((= x 1))"},
		{"(= obj.#field 1)", "((= obj.#field 1))"},
		// Ordinary calls are untouched.
		{"(f x y)", "((f x y))"},
	}
	for _, c := range cases {
		if got := lowerInspect(c.src); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, got, c.want)
		}
	}
}
