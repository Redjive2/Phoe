package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Note: macro-expansion hygiene (each expansion runs in its own frame) is
// exercised by the macro tests; the former `resume`-based case here was removed
// with the `resume` builtin.

// Phase B1 (lower-scope shadowing): a declaration may shadow a binding from
// an enclosing scope. A const in a function body shadows an outer const of
// the same name, and the outer binding is unaffected.
func TestDeclareShadowsEnclosing(t *testing.T) {
	inner := evalProgram(t, "(let y = 10)\n(let f () = (identity do (let y = 20) y))\n(f)")
	if inner.Kind != core.KindNum || inner.Val != float64(20) {
		t.Fatalf("shadowed y inside f = %#v, want 20", inner)
	}
	outer := evalProgram(t, "(let y = 10)\n(let f () = (identity do (let y = 20) y))\n(f)\ny")
	if outer.Kind != core.KindNum || outer.Val != float64(10) {
		t.Fatalf("outer y after f = %#v, want 10 (shadow must not mutate the enclosing binding)", outer)
	}
}

// Phase B2 (same-scope rebind): var/const may re-bind a name in place — a
// fresh binding (reducing var + '=' mutation), and re-const can read the
// prior value while rebinding.
func TestRebindSameScope(t *testing.T) {
	if got := evalProgram(t, "(let x = 1)\n(let x = (+ x 10))\nx"); got.Kind != core.KindNum || got.Val != float64(11) {
		t.Fatalf("re-const x = %#v, want 11", got)
	}
	if got := evalProgram(t, "(let var y = 1)\n(let var y = 2)\ny"); got.Kind != core.KindNum || got.Val != float64(2) {
		t.Fatalf("re-var y = %#v, want 2", got)
	}
}
