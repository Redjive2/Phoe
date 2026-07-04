package core

import "testing"

// Intern returns one canonical *Atom per name, so identical names share a
// pointer (the basis for O(1) atom equality) and distinct names don't.
func TestInternIdentity(t *testing.T) {
	a := Intern("fast")
	b := Intern("fast")
	if a != b {
		t.Fatalf("Intern(%q) returned distinct pointers %p and %p", "fast", a, b)
	}
	if a == Intern("slow") {
		t.Fatalf("distinct atom names interned to the same pointer")
	}
	// Leading zeros are significant: :01 and :1 are different atoms.
	if Intern("01") == Intern("1") {
		t.Fatalf("`01` and `1` interned to the same atom")
	}
}

// TvAtom wraps the interned atom with the atom Kind, and two calls with the
// same name are equal by pointer identity.
func TestTvAtom(t *testing.T) {
	v := TvAtom("fast")
	if v.Kind != KindAtom {
		t.Fatalf("TvAtom kind = %q, want %q", v.Kind, KindAtom)
	}
	if v.Val.(*Atom).Name() != "fast" {
		t.Fatalf("TvAtom name = %q, want %q", v.Val.(*Atom).Name(), "fast")
	}
	if v.Val != TvAtom("fast").Val {
		t.Fatalf("two TvAtom(%q) carry different *Atom pointers", "fast")
	}
}

func TestIsAtomName(t *testing.T) {
	ok := []string{"foo", "fast", "x1", "foo-bar", "done?", "01213", "1"}
	bad := []string{"", "12abc", "-foo", "foo--bar", ":foo", "?"}
	for _, s := range ok {
		if !IsAtomName(s) {
			t.Errorf("IsAtomName(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if IsAtomName(s) {
			t.Errorf("IsAtomName(%q) = true, want false", s)
		}
	}
}
