package lint

import "testing"

// Effect tracking, Phase 1: `name!` declarations/calls and a `(var self)`
// receiver are recognized syntax — they must lint clean (not unresolved names
// or malformed parameter patterns). Effect ENFORCEMENT (the `!`/effect-set
// checks) is a later phase; this only guards that the syntax parses cleanly.
func TestEffectSyntaxLintsClean(t *testing.T) {
	diags := analyze(t, `(struct Counter n)
(let Counter.Bump! ((var self) by) = (= self.n (+ self.n by)))
(let double! (x) = (* x 2))
(let caller () = (double! 3))
`)
	for _, code := range []string{"unresolved-identifier", "bad-form-shape", "bad-form-arity"} {
		if hasDiag(diags, code) {
			t.Fatalf("effect syntax must lint clean, got %q: %#v", code, diags)
		}
	}
}

// `double!` declared above resolves at its call site — the trailing `!` is part
// of the name, so the reference matches the declaration.
func TestEffectNameResolves(t *testing.T) {
	diags := analyze(t, "(let double! (x) = (* x 2))\n(var r (double! 4))\n")
	if hasDiag(diags, "unresolved-identifier") {
		t.Fatalf("call to declared 'double!' must resolve, got %#v", diags)
	}
}
