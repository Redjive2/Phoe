package core_test

import (
	"testing"

	"pho/pkg/core"
)

func TestTypeVarRepresentation(t *testing.T) {
	b := core.TypeVar("B", core.TypeNumber)

	// A type variable is set-theoretically gradual — the standard checks never
	// false-positive on it.
	if !b.IsGradual() {
		t.Error("a type variable should be gradual")
	}
	// Interned by (name, bound): the same parameter yields the same pointer; a
	// different name or bound is a different variable.
	if b != core.TypeVar("B", core.TypeNumber) {
		t.Error("same (name, bound) should intern to one type variable")
	}
	if b == core.TypeVar("C", core.TypeNumber) {
		t.Error("a different name should be a different variable")
	}
	if b == core.TypeVar("B", core.TypeString) {
		t.Error("a different bound should be a different variable")
	}
	// Reflexive: a reused (interned) variable is a subtype of itself.
	if !core.Subtype(b, b) {
		t.Error("a type variable is a subtype of itself")
	}
	// Accessors.
	if !core.IsTypeVar(b) {
		t.Error("IsTypeVar(b) should be true")
	}
	if core.IsTypeVar(core.TypeNumber) {
		t.Error("Number is not a type variable")
	}
	if bd, ok := core.TypeVarBound(b); !ok || bd != core.TypeNumber {
		t.Errorf("TypeVarBound = %v,%v; want Number,true", bd, ok)
	}
	// Renders as its parameter name.
	if b.Name() != "B" {
		t.Errorf("Name = %q; want B", b.Name())
	}
	// A nil bound defaults to the top type.
	if bd, _ := core.TypeVarBound(core.TypeVar("U", nil)); bd != core.TypeUnknown {
		t.Errorf("unbounded var bound = %v; want Unknown", bd)
	}
	// Distinct from the plain Dynamic type despite the shared gradual base.
	if b == core.TypeDynamic {
		t.Error("a type variable must be distinct from Dynamic")
	}
}
