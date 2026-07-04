package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Decl/impl split — the NEW property delegate form: get/set as parenthesized
// (get (params) body) / (set (params) body) sub-forms, TOLERANT with the old flat
// form. (Doc/PlanV1/DeclImplSplit.md)
func TestDeclImplPropertyRuntime(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"struct getter", "(struct Box x)\n(property Box.dbl (get (self) (* self.x 2)))\n(let b = Box.{ x = 5 })\nb.dbl", "10"},
		{"struct setter", "(struct Box x)\n(property Box.dbl (get (self) (* self.x 2)) (set (self v) (= self.x v)))\n(let var b = Box.{ x = 5 })\n(= b.dbl 7)\nb.dbl", "14"},
		{"free-standing getter", "(let var backing = 3)\n(property tally (get () backing) (set (v) (= backing v)))\ntally", "3"},
		{"free-standing setter", "(let var backing = 3)\n(property tally (get () backing) (set (v) (= backing v)))\n(= tally 9)\ntally", "9"},
		{"typed free-standing target", "(let var bt = 4)\n(property (Number temp) (get () bt) (set (v) (= bt v)))\ntemp", "4"},
	}
	for _, tc := range cases {
		if got := core.Stringify(evalProgram(t, tc.src)); got != tc.want {
			t.Errorf("%s: %q -> %q, want %q", tc.name, tc.src, got, tc.want)
		}
	}
}
