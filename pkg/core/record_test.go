package core

import "testing"

// instOf builds a struct instance of st with the given fields, for record
// membership tests (records match by field SHAPE, not nominal identity).
func instOf(st *tstruct, fields map[string]Tval) Tval {
	return Tval{&tinstance{Struct: st, Fields: fields}, KindInstance}
}

// An open record type `{x Number …}` matches any struct instance that has at
// least the named fields, each inhabiting its bound type — regardless of the
// instance's nominal struct. Width + covariant depth subtyping.
func TestRecordMembershipAndSubtype(t *testing.T) {
	a, b := &tstruct{}, &tstruct{} // two DISTINCT nominal structs
	recX := RecordType(map[string]*PhoType{"x": TypeNumber})
	recXY := RecordType(map[string]*PhoType{"x": TypeNumber, "y": TypeNumber})

	// Membership is structural: both nominal structs match by shape.
	if !recX.Contains(instOf(a, map[string]Tval{"x": TvNum(1)})) {
		t.Error("struct a{x:1} should inhabit {x Number}")
	}
	if !recX.Contains(instOf(b, map[string]Tval{"x": TvNum(2), "y": TvNum(3)})) {
		t.Error("struct b{x:2,y:3} should inhabit {x Number} (width)")
	}
	if recX.Contains(instOf(a, map[string]Tval{"x": TvStr("s")})) {
		t.Error("x is a string ⇒ not {x Number}")
	}
	if recX.Contains(instOf(a, map[string]Tval{"y": TvNum(1)})) {
		t.Error("missing field x ⇒ not {x Number}")
	}
	if recX.Contains(TvNum(5)) {
		t.Error("a non-struct never inhabits a record")
	}

	// Subtyping: width + covariant depth.
	if !Subtype(recXY, recX) {
		t.Error("[x -> n y -> n] <: [x -> n] (width)")
	}
	if Subtype(recX, recXY) {
		t.Error("{x N} ⊄ {x N y N} (missing field)")
	}
	recX5 := RecordType(map[string]*PhoType{"x": NumSingleton(5)})
	if !Subtype(recX5, recX) {
		t.Error("[x -> 5] <: [x -> Number] (depth)")
	}
	if Subtype(recX, recX5) {
		t.Error("{x Number} ⊄ {x 5}")
	}
	// Records relate to the top/bottom and never to unrelated primitives.
	if !Subtype(recX, TypeUnknown) {
		t.Error("record <: Unknown")
	}
	if !Subtype(TypeNone, recX) {
		t.Error("None <: record")
	}
	if Subtype(recX, TypeNumber) || Subtype(TypeNumber, recX) {
		t.Error("record and Number are unrelated")
	}
	if recX.IsEmpty() {
		t.Error("a record is inhabited (not empty)")
	}
}

// Records compose with non-struct types and other records in the algebra.
func TestRecordAlgebra(t *testing.T) {
	st := &tstruct{}
	inst := instOf(st, map[string]Tval{"x": TvNum(1)})
	recX := RecordType(map[string]*PhoType{"x": TypeNumber})

	// Optional record: (Or {x Number} Nil).
	opt := recX.Or(TypeNil)
	if !opt.Contains(inst) || !opt.Contains(TvNil) {
		t.Error("(Or record none) contains the struct and none")
	}
	if opt.Contains(TvNum(7)) {
		t.Error("(Or record none) excludes a number")
	}

	// (Or record Number) resolves both arms.
	rn := recX.Or(TypeNumber)
	if !rn.Contains(TvNum(7)) || !rn.Contains(inst) {
		t.Error("(Or record Number) contains numbers and matching structs")
	}

	// Intersection (meet): {x N} ∧ {y N} = {x N y N}.
	recY := RecordType(map[string]*PhoType{"y": TypeNumber})
	meet := recX.And(recY)
	if meet != RecordType(map[string]*PhoType{"x": TypeNumber, "y": TypeNumber}) {
		t.Errorf("{x N} ∧ {y N} = %s, want {x N y N}", meet.Name())
	}

	// Rendering and interning.
	if got := RecordType(map[string]*PhoType{"x": TypeNumber, "y": TypeString}).Name(); got != "[x -> Number y -> String]" {
		t.Errorf("render = %q", got)
	}
	if RecordType(map[string]*PhoType{"x": TypeNumber}) != recX {
		t.Error("structurally-equal records intern to one *PhoType")
	}
}
