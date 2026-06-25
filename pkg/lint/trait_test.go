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
		"(type Drawable (Trait () (method Self.Draw (self))))\n",
		"(type Greet (Trait () (method Self.Hi (self) 'hi')))\n",
		"(type HasName (Trait () (property Self.Name get)))\n",
		"(type Mut (Trait () (property Self.X get set)))\n",
		"(type WithImpl (Trait () (property Self.Area\n  get (method Self (self) 0)\n  set (method Self (self v) Nil))))\n",
		// extends another trait by name.
		"(type Drawable (Trait () (method Self.Draw (self))))\n(type Shape (Trait (Drawable) (method Self.Area (self))))\n",
		// a default body's references resolve (self + params are bound).
		"(type Add (Trait () (method Self.Inc (self n) (+ n 1))))\n",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.phl", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean, got %#v\n  src: %q", d, src)
		}
	}

	// A genuine unresolved reference in a default body still fires.
	if d := AnalyzeFile("t.phl", []byte("(type Bad (Trait () (method Self.Hi (self) (nope))))\n")); !hasDiagWithName(d, "unresolved-identifier", "nope") {
		t.Errorf("a typo in a trait default body should flag; got %#v", d)
	}
	// An unknown extended trait still flags as unresolved.
	if d := AnalyzeFile("t.phl", []byte("(type Shape (Trait (Nonexistent) (method Self.Area (self))))\n")); !hasDiagWithName(d, "unresolved-identifier", "Nonexistent") {
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

	draw := "(type Drawable (Trait () (method Self.Draw (self))))\n(fun f (Drawable) Nil)\n(fun f (p) Nil)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"struct satisfies", draw + "(struct P X)\n(method P.Draw (self) 1)\n(f P.{ X 1 })", false},
		{"struct missing method", draw + "(struct Q X)\n(f Q.{ X 1 })", true},
		{"non-struct never satisfies", draw + "(f 5)", true},
		{"property by field", "(type HasName (Trait () (property Self.Name get)))\n(fun g (HasName) Nil)\n(fun g (x) Nil)\n(struct R Name)\n(g R.{ Name 'x' })", false},
		{"property by field missing", "(type HasName (Trait () (property Self.Name get)))\n(fun g (HasName) Nil)\n(fun g (x) Nil)\n(struct S X)\n(g S.{ X 1 })", true},
		{"extends: needs both", "(type Drawable (Trait () (method Self.Draw (self))))\n" +
			"(type Shape (Trait (Drawable) (method Self.Area (self))))\n(fun h (Shape) Nil)\n(fun h (s) Nil)\n" +
			"(struct C X)\n(method C.Area (self) 1)\n(h C.{ X 1 })", true}, // missing Draw (inherited)
		{"var annotated satisfies", "(type Drawable (Trait () (method Self.Draw (self))))\n(struct P X)\n(method P.Draw (self) 1)\n(var (Drawable p) P.{ X 1 })", false},
		{"var annotated missing", "(type Drawable (Trait () (method Self.Draw (self))))\n(struct Q X)\n(var (Drawable q) Q.{ X 1 })", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantErr, c.src, AnalyzeFile("t.pho", []byte(c.src)))
			}
		})
	}
}
