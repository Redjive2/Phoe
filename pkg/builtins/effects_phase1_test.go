package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Effect tracking, Phase 1: a self-mutating method names its receiver plainly
// `self` (mutability is declared in the signature's `(var Self)`; the impl
// carries no `(var …)`), binds, and mutates self in place.
func TestEffectMethodMutatesSelf(t *testing.T) {
	src := `(struct Counter n)
(let Counter.Bump! (self by) = (= self.n (+ self.n by)))
(var c Counter.{ n = 0 })
(c.Bump! 5)
(c.Bump! 3)
c.n`
	got := evalProgram(t, src)
	if got.Kind != core.KindNum || got.Val.(float64) != 8 {
		t.Fatalf("Bump! plain-self twice from 0 by 5,3 = %#v, want num 8", got)
	}
}

// A bare `fun name!` declares and calls like any other function.
func TestEffectFunDeclaresAndCalls(t *testing.T) {
	got := evalProgram(t, "(let double! (x) = (* x 2))\n(double! 21)")
	if got.Kind != core.KindNum || got.Val.(float64) != 42 {
		t.Fatalf("(double! 21) = %#v, want 42", got)
	}
}
