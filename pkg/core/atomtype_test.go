package core

import "testing"

// Atom singleton types turn the flat Atom primitive into precise enums: a
// specific atom :ok is its own type, (Or :ok :error) is a tagged union, and the
// bare Atom type is the union of ALL atom singletons. These exercise the set
// algebra (Or/And/Not), subtyping, and runtime membership directly — no
// builtins or annotations involved.
func TestAtomSingletonMembership(t *testing.T) {
	ok := AtomSingleton("ok")
	err := AtomSingleton("error")
	result := ok.Or(err) // (Or :ok :error)

	cases := []struct {
		v    Tval
		ty   *PhoType
		want bool
	}{
		{TvAtom("ok"), ok, true},
		{TvAtom("error"), ok, false},
		{TvAtom("ok"), err, false},
		{TvAtom("ok"), result, true},
		{TvAtom("error"), result, true},
		{TvAtom("other"), result, false},
		{TvAtom("ok"), TypeAtom, true},    // every atom inhabits bare Atom
		{TvAtom("error"), TypeAtom, true}, //
		{TvNum(1), ok, false},             // a non-atom never inhabits a singleton
		{TvStr("ok"), ok, false},          // the string "ok" is NOT the atom :ok
		{TvAtom("ok"), TypeUnknown, true}, // ⊤ contains every atom
	}
	for _, c := range cases {
		if got := c.ty.Contains(c.v); got != c.want {
			t.Errorf("%s.Contains(%s) = %v, want %v", c.ty.Name(), Stringify(c.v), got, c.want)
		}
	}
}

func TestAtomSingletonSubtype(t *testing.T) {
	ok := AtomSingleton("ok")
	err := AtomSingleton("error")
	result := ok.Or(err)

	yes := [][2]*PhoType{
		{ok, ok},
		{ok, result},            // a member ⊆ the union
		{err, result},           //
		{ok, TypeAtom},          // a singleton ⊆ bare Atom
		{result, TypeAtom},      // an enum ⊆ bare Atom
		{ok, TypeUnknown},       // everything ⊆ ⊤
		{ok, ok.Or(TypeNumber)}, // ⊆ a mixed union
	}
	for _, p := range yes {
		if !Subtype(p[0], p[1]) {
			t.Errorf("want %s <: %s", p[0].Name(), p[1].Name())
		}
	}
	no := [][2]*PhoType{
		{ok, err},        // distinct singletons are unrelated
		{result, ok},     // the union ⊄ one member
		{TypeAtom, ok},   // bare Atom ⊄ a singleton
		{ok, TypeNumber}, // an atom is not a number
		{TypeNumber, ok}, //
	}
	for _, p := range no {
		if Subtype(p[0], p[1]) {
			t.Errorf("want %s ⊄ %s", p[0].Name(), p[1].Name())
		}
	}
}

// The intersection of two distinct singletons is empty (None), and a singleton
// intersected with bare Atom is itself — the basis for exact enum narrowing.
func TestAtomSingletonAlgebra(t *testing.T) {
	ok := AtomSingleton("ok")
	err := AtomSingleton("error")
	result := ok.Or(err)

	if got := ok.And(err); !got.IsEmpty() {
		t.Errorf(":ok ∧ :error = %s, want None", got.Name())
	}
	if got := ok.And(TypeAtom); got != ok {
		t.Errorf(":ok ∧ Atom = %s, want :ok", got.Name())
	}
	if got := ok.And(result); got != ok {
		t.Errorf(":ok ∧ (Or :ok :error) = %s, want :ok", got.Name())
	}
	// Enum narrowing: (Or :ok :error) ∧ :ok = :ok (the THEN-branch refinement).
	if got := result.And(ok); got != ok {
		t.Errorf("(Or :ok :error) ∧ :ok = %s, want :ok", got.Name())
	}
	// Interning: structurally-equal singletons share a pointer.
	if AtomSingleton("ok") != ok {
		t.Errorf(":ok is not interned to a single *PhoType")
	}
	// A singleton is a strict subset of bare Atom (not equal).
	if ok == TypeAtom {
		t.Errorf(":ok must not be the same type as bare Atom")
	}
}

// The display rendering distinguishes a singleton, an enum union, and bare Atom.
func TestAtomSingletonRender(t *testing.T) {
	if got := AtomSingleton("ok").Name(); got != ":ok" {
		t.Errorf("singleton render = %q, want \":ok\"", got)
	}
	enum := AtomSingleton("error").Or(AtomSingleton("ok"))
	if got := enum.Name(); got != ":error | :ok" {
		t.Errorf("enum render = %q, want \":error | :ok\"", got)
	}
	if got := TypeAtom.Name(); got != "Atom" {
		t.Errorf("bare atom render = %q, want \"Atom\"", got)
	}
	mixed := AtomSingleton("ok").Or(TypeNumber)
	if got := mixed.Name(); got != "Number | :ok" {
		t.Errorf("mixed render = %q, want \"Number | :ok\"", got)
	}
}
