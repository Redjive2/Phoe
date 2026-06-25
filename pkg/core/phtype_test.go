package core

import "testing"

// Stage C: the set-theoretic Boolean algebra. These tests validate Or/And/Not/
// Subtype against an INDEPENDENT brute-force membership oracle over a finite
// universe of sample points (one per primitive kind plus two struct types),
// then check the Boolean laws by interned-pointer equality.

// a point in the value universe: a single primitive family OR one struct.
type tpoint struct {
	name string
	bit  baseBits // non-zero for a primitive point
	st   *tstruct // non-nil for a struct point
}

// rawContains decides point membership DIRECTLY from t's raw fields, with no
// reference to And/Or/Not/Subtype — the oracle the algebra is checked against.
func rawContains(t *PhoType, p tpoint) bool {
	if p.st != nil {
		_, in := t.structs[p.st]
		if t.allStr {
			return !in // cofinite: every struct EXCEPT the listed exceptions
		}
		return in
	}
	return t.base&p.bit != 0
}

func algebraFixture() (points []tpoint, types []*PhoType, stA, stB *tstruct) {
	stA, stB = &tstruct{}, &tstruct{}
	tA, tB := structType(stA), structType(stB)

	points = []tpoint{
		{"num", bNum, nil}, {"list", bList, nil}, {"dict", bDict, nil},
		{"str", bStr, nil}, {"chr", bChr, nil}, {"atom", bAtom, nil},
		{"bool", bBool, nil}, {"nil", bNil, nil}, {"fun", bFun, nil},
		{"type", bType, nil}, {"A", 0, stA}, {"B", 0, stB},
	}
	types = []*PhoType{
		TypeNone, TypeUnknown, TypeNumber, TypeString, TypeBoolean, TypeList,
		tA, tB,
		TypeNumber.Or(TypeString),
		TypeNumber.Or(tA),
		tA.Or(tB),
		TypeNumber.Not(),
		tA.Not(),
		TypeCollection,
		TypeNumber.Or(TypeString).And(TypeNumber), // == Number
		TypeNumber.Or(tA).Or(TypeBoolean),
	}
	return
}

// Or/And/Not agree with the oracle pointwise, and Subtype is exactly
// containment of point-sets.
func TestTypeAlgebraVsOracle(t *testing.T) {
	points, types, _, _ := algebraFixture()

	for _, a := range types {
		na := a.Not()
		for _, p := range points {
			if rawContains(na, p) != !rawContains(a, p) {
				t.Errorf("Not(%s) @ %s: got %v", a.Name(), p.name, rawContains(na, p))
			}
		}
		for _, b := range types {
			or, and := a.Or(b), a.And(b)
			subWant := true
			for _, p := range points {
				if rawContains(or, p) != (rawContains(a, p) || rawContains(b, p)) {
					t.Errorf("Or(%s,%s) @ %s mismatch", a.Name(), b.Name(), p.name)
				}
				if rawContains(and, p) != (rawContains(a, p) && rawContains(b, p)) {
					t.Errorf("And(%s,%s) @ %s mismatch", a.Name(), b.Name(), p.name)
				}
				if rawContains(a, p) && !rawContains(b, p) {
					subWant = false
				}
			}
			if Subtype(a, b) != subWant {
				t.Errorf("Subtype(%s, %s): got %v want %v", a.Name(), b.Name(), Subtype(a, b), subWant)
			}
		}
	}
}

// The Boolean laws hold by interned-pointer identity (canonicalization makes
// structurally-equal types the same *PhoType).
func TestTypeAlgebraLaws(t *testing.T) {
	_, types, _, _ := algebraFixture()
	eq := func(name string, x, y *PhoType) {
		if x != y {
			t.Errorf("%s: %s != %s", name, x.Name(), y.Name())
		}
	}
	for _, a := range types {
		eq("¬¬a=a", a.Not().Not(), a)
		eq("a∨¬a=⊤", a.Or(a.Not()), TypeUnknown)
		eq("a∧¬a=⊥", a.And(a.Not()), TypeNone)
		eq("a∨a=a", a.Or(a), a)
		eq("a∧a=a", a.And(a), a)
		eq("a∨⊥=a", a.Or(TypeNone), a)
		eq("a∧⊤=a", a.And(TypeUnknown), a)
		eq("a∨⊤=⊤", a.Or(TypeUnknown), TypeUnknown)
		eq("a∧⊥=⊥", a.And(TypeNone), TypeNone)
		for _, b := range types {
			eq("a∨b=b∨a", a.Or(b), b.Or(a))
			eq("a∧b=b∧a", a.And(b), b.And(a))
			eq("¬(a∨b)=¬a∧¬b", a.Or(b).Not(), a.Not().And(b.Not()))
			eq("¬(a∧b)=¬a∨¬b", a.And(b).Not(), a.Not().Or(b.Not()))
			eq("a\\b=a∧¬b", a.Diff(b), a.And(b.Not()))
		}
	}
}

// Dynamic is the gradual top: it contains every value and is flagged gradual,
// and the gradual flag propagates through the connectives.
func TestDynamicType(t *testing.T) {
	if !TypeDynamic.IsGradual() {
		t.Error("Dynamic should be gradual")
	}
	if TypeNumber.IsGradual() || TypeUnknown.IsGradual() {
		t.Error("concrete types must not be gradual")
	}
	if TypeDynamic == TypeUnknown {
		t.Error("Dynamic and Unknown must be distinct values")
	}
	// Contains everything.
	for _, v := range []Tval{TvNum(1), TvStr("x"), TvBool(true), TvNil} {
		if !TypeDynamic.Contains(v) {
			t.Errorf("Dynamic should contain %s", v.Kind)
		}
	}
	// gradual flag propagates.
	if !TypeNumber.Or(TypeDynamic).IsGradual() {
		t.Error("Or with Dynamic should be gradual")
	}
	if !TypeDynamic.And(TypeNumber).IsGradual() {
		t.Error("And with Dynamic should be gradual")
	}
}

// Collection is exactly String | List | Map.
func TestCollectionType(t *testing.T) {
	if TypeList.Or(TypeDict).Or(TypeString) != TypeCollection {
		t.Error("Collection != String|List|Map")
	}
	for _, v := range []Tval{TvSlice(nil), TvStr("s")} {
		if !TypeCollection.Contains(v) {
			t.Errorf("Collection should contain %s", v.Kind)
		}
	}
	if TypeCollection.Contains(TvNum(1)) {
		t.Error("Collection should not contain a number")
	}
}
