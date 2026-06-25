package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Inline type-signature syntax — Phase 1 runtime: the SIGNATURE forms are
// recognized and ERASED (the interpreter binds nothing), so a `(fun name
// (Types) Ret)` / `(method R.N (Self) Ret)` sig is a no-op and the
// implementation form does the real binding. See Doc/PlanV1/TypeSignatures.md.

func TestTypeSigErasedAtRuntime(t *testing.T) {
	num := func(src string, want float64) {
		t.Helper()
		v := evalProgram(t, src)
		if v.Kind != core.KindNum || v.Val.(float64) != want {
			t.Errorf("eval(%q) = %v (%s), want %v", src, v.Val, v.Kind, want)
		}
	}
	// Typed binding: name bound, type erased.
	num("(const (Number n) 5)\nn", 5)
	// fun signature no-ops; the impl binds and runs.
	num("(fun add (Number Number) Number)\n(fun add (a b) (+ a b))\n(add 3 4)", 7)
	// method signature no-ops; the impl binds and runs.
	num("(struct P X)\n(method P.Show (Self) Number)\n(method P.Show (self) (* self.X 2))\n(var p P.{ X 5 })\n(p.Show)", 10)
}

// A lone signature with no implementation binds nothing — calling it is
// unresolved (Phase 2 will turn this into a 'missing-implementation' lint).
func TestLoneSigBindsNothing(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(fun mul (Number Number) Number)\n(mul 2 3)"); !hasCode(codes, core.ErrUnresolved) {
		t.Errorf("a lone fun signature should leave the name unresolved; got %v", codes)
	}
}
