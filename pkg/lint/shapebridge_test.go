package lint

import (
	"testing"

	"pho/pkg/annot"
)

// The shape↔type bridge lets the gradual mismatch checker use a value's
// inferred SHAPE (struct/list/num/… — known from constructors and literals)
// when its precise type is gradual. It compares the shape's broad type by
// DISJOINTNESS, not subtyping: it fires only when NO value of that shape could
// inhabit the expected type, so a refinement the shape can't see (a singleton
// value, a list's element type, a record's fields) never causes a false
// positive. This is the soundness contract — both halves are tested here.
//
// The values are `var`s, not `const`s, deliberately: a const's type is
// forward-propagated precisely (see TestConstTypePropagation), which would
// bypass the coarse bridge; a var exercises the bridge itself.
func TestShapeBridge(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	point := "(struct Point.{ x Number })\n"
	expect := func(paramType string) string {
		return "(fun f (" + paramType + ") Nil)\n(fun f (x) Nil)\n"
	}
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// Fires: a shaped variable against an incompatible declared type, no annotation.
		{"struct-var vs String", point + expect("String") + "(let var p = Point.{ x 1 })\n(f p)", true},
		{"list-var vs String", expect("String") + "(let var xs = [1 2 3])\n(f xs)", true},
		{"num-var vs String", expect("String") + "(let var n = 5)\n(f n)", true},
		{"nil-var vs Number", expect("Number") + "(let var z = none)\n(f z)", true},

		// Soundness: must stay silent where the shape can't see the refinement.
		{"num-var vs Number", expect("Number") + "(let var n = 5)\n(f n)", false},
		{"num-var vs Number|Nil", expect("(Or Number none)") + "(let var n = 5)\n(f n)", false},
		{"num-var vs singleton 5", expect("5") + "(let var n = 5)\n(f n)", false},
		{"list-var vs (List Number)", expect("(List Number)") + "(let var xs = [1 2 3])\n(f xs)", false},
		{"list-var vs (List String)", expect("(List String)") + "(let var xs = [1 2 3])\n(f xs)", false},
		{"struct-var vs record it satisfies", point + expect("Struct.{ x Number }") + "(let var p = Point.{ x 1 })\n(f p)", false},

		// Control: the precise (literal) path is unchanged.
		{"list-literal vs String", expect("String") + "(f [1 2 3])", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
