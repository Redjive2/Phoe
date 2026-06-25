package core

import "testing"

// A trait's identity is per-construction (distinct types), but SUBTYPING is
// structural: trait A ⊆ trait B iff A requires at least B's (non-defaulted)
// members. Extends folds the supertrait's requirements into A, so it is just a
// bigger requirement set.
func TestTraitSubtype(t *testing.T) {
	mFG := &TraitInfo{Methods: map[string]TraitMethod{
		"F": {Arity: 0}, "G": {Arity: 0},
	}}
	mF := &TraitInfo{Methods: map[string]TraitMethod{"F": {Arity: 0}}}
	fg := TraitType(mFG)
	f := TraitType(mF)

	if !Subtype(fg, f) {
		t.Error("{F,G} <: {F} (more requirements ⇒ narrower)")
	}
	if Subtype(f, fg) {
		t.Error("{F} ⊄ {F,G} (missing requirement G)")
	}
	if !Subtype(fg, fg) {
		t.Error("a trait is a subtype of itself")
	}
	// A defaulted requirement in the supertrait need not be required by the sub.
	withDefault := TraitType(&TraitInfo{Methods: map[string]TraitMethod{
		"F": {Arity: 0}, "H": {Arity: 0, Default: func(ctx Context, a []ttnode) Tval { return TvNil }},
	}})
	if !Subtype(f, withDefault) {
		t.Error("{F} <: {F, H=default} — H is auto-filled so not required of the sub")
	}
	// Traits relate to ⊤/⊥ but not to unrelated primitives.
	if !Subtype(f, TypeUnknown) || !Subtype(TypeNone, f) {
		t.Error("trait <: Unknown and None <: trait")
	}
	if Subtype(f, TypeNumber) || Subtype(TypeNumber, f) {
		t.Error("a trait and Number are unrelated")
	}
	if f.IsEmpty() {
		t.Error("a trait is inhabited (not empty)")
	}
	if got := f.Name(); got != "(Trait f)" {
		t.Errorf("render = %q, want (Trait F)", got)
	}
}
