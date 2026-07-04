package lint

import (
	"testing"

	"pho/pkg/annot"
)

// Struct types resolve at lint time: a fully-typed struct gets a precise record
// type, so a struct NAME in an annotation resolves to it (no longer Dynamic),
// and a struct-shaped value is checked against a declared struct/record/
// primitive type. The representation is structural (Pho's runtime is
// duck-typed), and SOUND — only fully-precisely-typed structs get a record;
// structs with untyped or struct-typed fields stay coarse; traits are checked
// structurally; same-shape structs are interchangeable.
func TestNominalStructTypes(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	pt := "(struct Point.{ Number x Number y })\n"
	sig := func(p string) string { return "(fun f (" + p + ") None)\n(let f (x) = none)\n" }
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// A struct name in a signature now resolves (was Dynamic → no check).
		{"wrong struct to struct param", pt + "(struct Other.{ Number a Number b })\n" + sig("Point") + "(let o = Other.{ a = 1 b = 2 })\n(f o)", true},
		{"primitive to struct param", pt + sig("Point") + "(f 5)", true},
		{"struct missing required field", pt + sig("Struct.{ Number x Number z }") + "(let p = Point.{ x = 1 y = 2 })\n(f p)", true},
		{"struct wrong field type", pt + sig("Struct.{ String x }") + "(let p = Point.{ x = 1 y = 2 })\n(f p)", true},

		// Soundness — must stay silent.
		{"matching struct", pt + sig("Point") + "(let p = Point.{ x = 1 y = 2 })\n(f p)", false},
		{"struct satisfies wider record", pt + sig("Struct.{ Number x }") + "(let p = Point.{ x = 1 y = 2 })\n(f p)", false},
		{"same-shape struct is structural", pt + "(struct Twin.{ Number x Number y })\n" + sig("Point") + "(let tw = Twin.{ x = 1 y = 2 })\n(f tw)", false},
		{"untyped struct stays coarse", "(struct Plain a b)\n" + sig("Point") + "(let pl = Plain.{ a = 1 b = 2 })\n(f pl)", false},
		{"struct satisfies trait", pt + "(let Point.draw (self) = self.x)\n(trait Drawable (method self.draw (self)))\n(fun g (Drawable) None)\n(let g (d) = none)\n(let p = Point.{ x = 1 y = 2 })\n(g p)", false},
		{"return struct vs trait result", pt + "(let Point.draw (self) = self.x)\n(trait Drawable (method self.draw (self)))\n(let p = Point.{ x = 1 y = 2 })\n(fun h () Drawable)\n(let h () = p)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
