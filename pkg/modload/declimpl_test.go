package modload

import (
	"strings"
	"testing"
)

// The decl/impl split makes `(= name (params) body)` a function/method
// IMPLEMENTATION — a definition permitted at a library's top level — while the
// 3-child `(= target value)` stays a reassignment (a side effect, rejected).

func TestDeclImplIsLibraryForm(t *testing.T) {
	forms := parseForms(t, "(= f (x) x)\n(= Owner.m (self) self)\n(= x 5)\n(= Owner.n (self) (self.f))")
	cases := []struct {
		i    int
		want bool
		desc string
	}{
		{0, true, "(= f (x) x) function impl"},
		{1, true, "(= Owner.m (self) self) method impl"},
		{2, false, "(= x 5) reassignment (side effect)"},
		{3, true, "(= Owner.n (self) (self.f)) method impl"},
	}
	for _, c := range cases {
		if got := isLibraryForm(forms[c.i].form); got != c.want {
			t.Errorf("isLibraryForm(%s) = %v, want %v", c.desc, got, c.want)
		}
	}
}

// A `(= …)` impl is a pure definition: it lifts above the side-effecting `let`
// bindings, exactly like `fun`/`method` do.
func TestDeclImplLiftedAboveInits(t *testing.T) {
	src := `(let current = 5)
(= greet (name) name)
(struct Point x y)
(= Point.sum (self) (+ self.x self.y))
(let var counter = 0)`
	got := heads(liftDefinitions(parseForms(t, src)))
	// Definitions (=, struct, =) first in source order, then the let bindings.
	want := []string{"=", "struct", "=", "let", "let"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("lift order:\n got  %v\n want %v", got, want)
	}
}
