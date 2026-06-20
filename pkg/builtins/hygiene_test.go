package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Phase A (macro hygiene): each macro expansion runs in its own scope, so a
// binding it introduces stays local. Two expansions declaring the same name
// in one scope therefore don't collide — the second still evaluates. The
// resumed forms use bare `do` (resume re-applies do notation).
func TestResumeExpansionScoped(t *testing.T) {
	exp := func(n string) string {
		return "(resume '(identity do (var x " + n + ") x))"
	}
	got := evalProgram(t, exp("1")+"\n"+exp("2"))
	if got.Kind != core.KindNum || got.Val != float64(2) {
		t.Fatalf("second expansion = %#v, want num 2 (expansions must be independently scoped)", got)
	}
}

// Phase B1 (lower-scope shadowing): a declaration may shadow a binding from
// an enclosing scope. A const in a function body shadows an outer const of
// the same name, and the outer binding is unaffected.
func TestDeclareShadowsEnclosing(t *testing.T) {
	inner := evalProgram(t, "(const y 10)\n(fun f () (identity do (const y 20) y))\n(f)")
	if inner.Kind != core.KindNum || inner.Val != float64(20) {
		t.Fatalf("shadowed y inside f = %#v, want 20", inner)
	}
	outer := evalProgram(t, "(const y 10)\n(fun f () (identity do (const y 20) y))\n(f)\ny")
	if outer.Kind != core.KindNum || outer.Val != float64(10) {
		t.Fatalf("outer y after f = %#v, want 10 (shadow must not mutate the enclosing binding)", outer)
	}
}

// Phase B2 (same-scope rebind): var/const may re-bind a name in place — a
// fresh binding (reducing var + '=' mutation), and re-const can read the
// prior value while rebinding.
func TestRebindSameScope(t *testing.T) {
	if got := evalProgram(t, "(const x 1)\n(const x (+ x 10))\nx"); got.Kind != core.KindNum || got.Val != float64(11) {
		t.Fatalf("re-const x = %#v, want 11", got)
	}
	if got := evalProgram(t, "(var y 1)\n(var y 2)\ny"); got.Kind != core.KindNum || got.Val != float64(2) {
		t.Fatalf("re-var y = %#v, want 2", got)
	}
}
