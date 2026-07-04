package lint

import "testing"

// Phase 3/4 generics: a template parameter resolves to a core TYPE VARIABLE
// carrying its bound, and a value passed where a bounded parameter is expected
// is checked against the bound by DISJOINTNESS — a provable mismatch only when
// no instantiation of the parameter could accept the value. This is sound and
// gradual: a value narrower than the bound (a singleton) and an unbounded
// parameter never false-positive.
func TestGenericBoundEnforcement(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{
			"bound violated — String where Number-bounded expected",
			"(template (Number B))\n(fun f (B) None)\n(let f (x) = none)\n(f 'str')",
			true,
		},
		{
			"bound satisfied — a Number",
			"(template (Number B))\n(fun f (B) None)\n(let f (x) = none)\n(f 5)",
			false,
		},
		{
			// The key soundness case: a singleton `5` is a valid instantiation of a
			// Number-bounded parameter, so disjointness (not subtype) is right.
			"bound satisfied — a singleton subtype of the bound",
			"(template (Number B))\n(fun f (B) None)\n(let f (x) = none)\n(let n = 5)\n(f n)",
			false,
		},
		{
			"unbounded parameter accepts anything",
			"(template U)\n(fun f (U) None)\n(let f (x) = none)\n(f 'anything')",
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}

// A struct construction `Struct.{ field = value … }` now checks each field value
// against its declared field type — enforcing a generic parameter's BOUND at
// construction, and (as a general win) catching wrong-typed fields on any typed
// struct. Sound + gradual: disjointness decides, so untyped/unbounded fields and
// singleton subtypes never false-positive.
func TestConstructionFieldTypes(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"generic bound violated at construction",
			"(template (Number B))\n(struct Box { B v })\n(Box.{ v = 'str' })", true},
		{"generic bound satisfied at construction",
			"(template (Number B))\n(struct Box { B v })\n(Box.{ v = 5 })", false},
		{"unbounded generic field accepts anything",
			"(template U)\n(struct Box { U v })\n(Box.{ v = 'anything' })", false},
		{"non-generic typed field violated",
			"(struct P.{ Number n })\n(P.{ n = 'str' })", true},
		{"non-generic typed field satisfied",
			"(struct P.{ Number n })\n(P.{ n = 5 })", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
