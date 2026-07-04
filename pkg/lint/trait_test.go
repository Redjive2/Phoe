package lint

import (
	"testing"

	"pho/pkg/annot"
)

// A `(Trait (extends…) member…)` form lints clean: the extends-list entries are
// resolved as trait references, but the member signatures' `Self` receiver,
// parameter names, and get/set keywords are declarations — never flagged. A
// reference INSIDE a default body still resolves (and a typo still flags).
func TestTraitLintsClean(t *testing.T) {
	clean := []string{
		"(type Drawable (Trait () (method self.draw (self))))\n",
		"(type Greet (Trait () (method self.hi (self) 'hi')))\n",
		"(type Has-Name (Trait () (property self.name get)))\n",
		"(type Mut (Trait () (property self.x get set)))\n",
		"(type With-Impl (Trait () (property self.area\n  get (method self (self) 0)\n  set (method self (self v) none))))\n",
		// extends another trait by name.
		"(type Drawable (Trait () (method self.draw (self))))\n(type Shape (Trait (Drawable) (method self.area (self))))\n",
		// a default body's references resolve (self + params are bound).
		"(type Add (Trait () (method self.inc (self n) (+ n 1))))\n",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.phl", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean, got %#v\n  src: %q", d, src)
		}
	}

	// A genuine unresolved reference in a default body still fires.
	if d := AnalyzeFile("t.phl", []byte("(type Bad (Trait () (method self.hi (self) (nope))))\n")); !hasDiagWithName(d, "unresolved-identifier", "nope") {
		t.Errorf("a typo in a trait default body should flag; got %#v", d)
	}
	// An unknown extended trait still flags as unresolved.
	if d := AnalyzeFile("t.phl", []byte("(type Shape (Trait (nonexistent) (method self.area (self))))\n")); !hasDiagWithName(d, "unresolved-identifier", "nonexistent") {
		t.Errorf("an unknown extended trait should flag; got %#v", d)
	}
}

// The gradual checker resolves a trait type and STATICALLY checks that a value
// flowing into a trait-typed slot satisfies it — using the collected struct
// member surface. A struct missing a required member is flagged; a non-struct
// value never satisfies a method-trait; property requirements honor fields;
// extends folds in the supertrait's requirements.
func TestTraitStaticChecking(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	draw := "(type Drawable (Trait () (method self.draw (self))))\n(fun f (Drawable) None)\n(let f (p) = none)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"struct satisfies", draw + "(struct P x)\n(let P.draw (self) = 1)\n(f P.{ x = 1 })", false},
		{"struct missing method", draw + "(struct Q x)\n(f Q.{ x = 1 })", true},
		{"non-struct never satisfies", draw + "(f 5)", true},
		{"property by field", "(type Has-Name (Trait () (property self.name get)))\n(fun g (Has-Name) None)\n(let g (x) = none)\n(struct R name)\n(g R.{ name = 'x' })", false},
		{"property by field missing", "(type Has-Name (Trait () (property self.name get)))\n(fun g (Has-Name) None)\n(let g (x) = none)\n(struct S x)\n(g S.{ x = 1 })", true},
		{"extends: needs both", "(type Drawable (Trait () (method self.draw (self))))\n" +
			"(type Shape (Trait (Drawable) (method self.area (self))))\n(fun h (Shape) None)\n(let h (s) = none)\n" +
			"(struct C x)\n(let C.area (self) = 1)\n(h C.{ x = 1 })", true}, // missing Draw (inherited)
		{"var annotated satisfies", "(type Drawable (Trait () (method self.draw (self))))\n(struct P x)\n(let P.draw (self) = 1)\n(let var (Drawable p) = P.{ x = 1 })", false},
		{"var annotated missing", "(type Drawable (Trait () (method self.draw (self))))\n(struct Q x)\n(let var (Drawable q) = Q.{ x = 1 })", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantErr, c.src, AnalyzeFile("t.pho", []byte(c.src)))
			}
		})
	}
}
