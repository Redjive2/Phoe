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

	pt := "(struct Point.{ X Number Y Number })\n"
	sig := func(p string) string { return "(fun f (" + p + ") Nil)\n(fun f (x) Nil)\n" }
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// A struct name in a signature now resolves (was Dynamic → no check).
		{"wrong struct to struct param", pt + "(struct Other.{ A Number B Number })\n" + sig("Point") + "(const o Other.{ A 1 B 2 })\n(f o)", true},
		{"primitive to struct param", pt + sig("Point") + "(f 5)", true},
		{"struct missing required field", pt + sig("Struct.{ X Number Z Number }") + "(const p Point.{ X 1 Y 2 })\n(f p)", true},
		{"struct wrong field type", pt + sig("Struct.{ X String }") + "(const p Point.{ X 1 Y 2 })\n(f p)", true},

		// Soundness — must stay silent.
		{"matching struct", pt + sig("Point") + "(const p Point.{ X 1 Y 2 })\n(f p)", false},
		{"struct satisfies wider record", pt + sig("Struct.{ X Number }") + "(const p Point.{ X 1 Y 2 })\n(f p)", false},
		{"same-shape struct is structural", pt + "(struct Twin.{ X Number Y Number })\n" + sig("Point") + "(const tw Twin.{ X 1 Y 2 })\n(f tw)", false},
		{"untyped struct stays coarse", "(struct Plain A B)\n" + sig("Point") + "(const pl Plain.{ A 1 B 2 })\n(f pl)", false},
		{"struct satisfies trait", pt + "(method Point.Draw (self) self.X)\n(trait Drawable (method Self.Draw (self)))\n(fun g (Drawable) Nil)\n(fun g (d) Nil)\n(const p Point.{ X 1 Y 2 })\n(g p)", false},
		{"return struct vs trait result", pt + "(method Point.Draw (self) self.X)\n(trait Drawable (method Self.Draw (self)))\n(const p Point.{ X 1 Y 2 })\n(fun h () Drawable)\n(fun h () p)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
