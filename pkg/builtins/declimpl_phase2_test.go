package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Decl/impl split (Doc/PlanV1/DeclImplSplit.md), Phase 2 — TOLERANT runtime. The `=`
// builtin is arity-overloaded: (let name (params) = body) / (let Owner.name (params) = body)
// bind a function/method IMPLEMENTATION; (= target value) stays reassignment. The old
// (fun name (a b) body) impl form keeps working during the tolerant window.
func TestDeclImplRuntime(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"= fun impl", "(let add (a b) = (+ a b))\n(add 3 4)", "7"},
		{"fun sig erased + = impl", "(fun add (Number Number) Number)\n(let add (a b) = (+ a b))\n(add 3 4)", "7"},
		{"= method impl", "(struct P a b)\n(let P.sum (self) = (+ self.a self.b))\n(let p = P.{ a = 3 b = 4 })\n(p.sum)", "7"},
		{"= recursion", "(let fact (n) = (if (<= n 1) then 1 else (* n (fact (- n 1)))))\n(fact 5)", "120"},
		{"old fun impl tolerated", "(let addy (a b) = (+ a b))\n(addy 3 4)", "7"},
		{"2-arg = reassign", "(let var x = 1)\n(= x 5)\nx", "5"},
	}
	for _, tc := range cases {
		if got := core.Stringify(evalProgram(t, tc.src)); got != tc.want {
			t.Errorf("%s: %q -> %q, want %q", tc.name, tc.src, got, tc.want)
		}
	}
}
