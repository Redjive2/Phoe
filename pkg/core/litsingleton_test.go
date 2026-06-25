package core

import "testing"

// Number / string / bool literal singletons extend the atom-singleton machinery
// to the other scalar primitives: `5`, `"GET"`, `True` each become their own
// type, so (Or 200 404 500) and (Or "GET" "POST") are precise enums. These
// exercise membership, subtyping, the set algebra, and rendering directly.
func TestLiteralSingletonMembership(t *testing.T) {
	cases := []struct {
		v    Tval
		ty   *PhoType
		want bool
	}{
		// numbers
		{TvNum(5), NumSingleton(5), true},
		{TvNum(6), NumSingleton(5), false},
		{TvNum(5), NumSingleton(5).Or(NumSingleton(6)), true},
		{TvNum(5), TypeNumber, true},         // a number inhabits bare Number
		{TvStr("5"), NumSingleton(5), false}, // the string "5" is not the number 5
		// strings
		{TvStr("GET"), StrSingleton("GET"), true},
		{TvStr("POST"), StrSingleton("GET"), false},
		{TvStr("GET"), StrSingleton("GET").Or(StrSingleton("POST")), true},
		{TvStr("GET"), TypeString, true},
		// bools
		{TvBool(true), BoolSingleton(true), true},
		{TvBool(false), BoolSingleton(true), false},
		{TvBool(true), TypeBoolean, true},
		// cross-primitive & top
		{TvNum(5), TypeUnknown, true},
		{TvAtom("ok"), NumSingleton(5), false},
		{TvNum(5), StrSingleton("5"), false},
	}
	for _, c := range cases {
		if got := c.ty.Contains(c.v); got != c.want {
			t.Errorf("%s.Contains(%s) = %v, want %v", c.ty.Name(), Stringify(c.v), got, c.want)
		}
	}
}

func TestLiteralSingletonSubtype(t *testing.T) {
	statuses := NumSingleton(200).Or(NumSingleton(404))
	methods := StrSingleton("GET").Or(StrSingleton("POST"))

	yes := [][2]*PhoType{
		{NumSingleton(5), NumSingleton(5)},
		{NumSingleton(200), statuses},  // member ⊆ enum
		{NumSingleton(5), TypeNumber},  // singleton ⊆ base
		{statuses, TypeNumber},         // enum ⊆ base
		{StrSingleton("GET"), methods}, //
		{methods, TypeString},          //
		{BoolSingleton(true), TypeBoolean},
		{NumSingleton(5), TypeUnknown},
	}
	for _, p := range yes {
		if !Subtype(p[0], p[1]) {
			t.Errorf("want %s <: %s", p[0].Name(), p[1].Name())
		}
	}
	no := [][2]*PhoType{
		{NumSingleton(5), NumSingleton(6)},                       // distinct literals unrelated
		{statuses, NumSingleton(200)},                            // enum ⊄ one member
		{TypeNumber, NumSingleton(5)},                            // bare Number ⊄ a singleton
		{NumSingleton(5), StrSingleton("5")},                     // number ⊄ string
		{NumSingleton(5), TypeString},                            // number is not a string
		{StrSingleton("GET"), methods.And(StrSingleton("POST"))}, // "GET" ⊄ "POST"
	}
	for _, p := range no {
		if Subtype(p[0], p[1]) {
			t.Errorf("want %s ⊄ %s", p[0].Name(), p[1].Name())
		}
	}
}

func TestLiteralSingletonAlgebra(t *testing.T) {
	// Intersection of distinct literals is empty; with the base type is itself.
	if got := NumSingleton(5).And(NumSingleton(6)); !got.IsEmpty() {
		t.Errorf("5 ∧ 6 = %s, want None", got.Name())
	}
	if got := NumSingleton(5).And(TypeNumber); got != NumSingleton(5) {
		t.Errorf("5 ∧ Number = %s, want 5", got.Name())
	}
	// Enum narrowing: (Or 200 404) ∧ 200 = 200.
	statuses := NumSingleton(200).Or(NumSingleton(404))
	if got := statuses.And(NumSingleton(200)); got != NumSingleton(200) {
		t.Errorf("(Or 200 404) ∧ 200 = %s, want 200", got.Name())
	}
	// The full bool set normalizes back to the bare Boolean type.
	if got := BoolSingleton(true).Or(BoolSingleton(false)); got != TypeBoolean {
		t.Errorf("(Or True False) = %s, want Boolean (interned identity)", got.Name())
	}
	// Interning: equal literals share a pointer; distinct base types differ.
	if NumSingleton(5) != NumSingleton(5) {
		t.Errorf("5 is not interned to a single *PhoType")
	}
	if NumSingleton(5) == TypeNumber {
		t.Errorf("5 must not be the same type as bare Number")
	}
}

func TestLiteralSingletonRender(t *testing.T) {
	checks := []struct {
		t    *PhoType
		want string
	}{
		{NumSingleton(5), "5"},
		{NumSingleton(200).Or(NumSingleton(404)), "200 | 404"},
		{StrSingleton("GET"), `'GET'`},
		{StrSingleton("GET").Or(StrSingleton("POST")), `'GET' | 'POST'`},
		{BoolSingleton(true), "True"},
		{BoolSingleton(false), "False"},
		{TypeNumber, "Number"},
		{NumSingleton(5).Or(StrSingleton("x")), `5 | 'x'`},
	}
	for _, c := range checks {
		if got := c.t.Name(); got != c.want {
			t.Errorf("render = %q, want %q", got, c.want)
		}
	}
}
