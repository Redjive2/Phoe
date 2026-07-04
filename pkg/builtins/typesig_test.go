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
	// Typed binding: name bound, type erased (grouped form).
	num("(let (Number n) = 5)\nn", 5)
	// fun signature no-ops; the impl binds and runs.
	num("(fun add (Number Number) Number)\n(let add (a b) = (+ a b))\n(add 3 4)", 7)
	// method signature no-ops; the impl binds and runs.
	num("(struct P x)\n(method P.show (Self) Number)\n(let P.show (self) = (* self.x 2))\n(let var p = P.{ x = 5 })\n(p.show)", 10)
	// a mutable-receiver method SIGNATURE `(var Self)` is a sig, not a
	// body-None impl that would clobber the real one (which would leave x
	// at 5) — the clause attaches to it and runs. A `(var Self)` receiver
	// requires the `=` self-mutation suffix, and the impl names self plainly.
	num("(struct P x)\n(method P.grow= ((var Self)) None)\n(let P.grow= (self) = (= self.x 10))\n(let var p = P.{ x = 5 })\n(p.grow=)\np.x", 10)
}

// A lone signature with no implementation binds nothing — calling it is
// unresolved (Phase 2 will turn this into a 'missing-implementation' lint).
func TestLoneSigBindsNothing(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(fun mul (Number Number) Number)\n(mul 2 3)"); !hasCode(codes, core.ErrUnresolved) {
		t.Errorf("a lone fun signature should leave the name unresolved; got %v", codes)
	}
}
