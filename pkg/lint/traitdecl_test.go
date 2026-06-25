package lint

import (
	"testing"

	"pho/pkg/annot"
)

// The named-trait form `(trait Name [(extends…)] member…)` is the
// `(type Name (Trait …))` shorthand: lowercase, inline name, optional `()`
// extends, with method/property and (parse-only) static members. It must lint
// cleanly and resolve as a type, while still driving trait satisfaction checks.

func TestNamedTraitLintsClean(t *testing.T) {
	srcs := []string{
		// named trait + a satisfying struct + an Is? test
		"(struct Circle.{ R Number })\n(method Circle.Draw (self) self.R)\n(trait Drawable (method Self.Draw (self)))\n(const c Circle.{ R 5 })\n(const d (c.Is? Drawable))\n",
		// empty extends () is allowed
		"(trait Named () (property Self.Name get))\n",
		// the user's example: static members + a member annotation
		"(struct Point.{ X Number Y Number })\n(trait Memo\n    (static method Self.Calc (Self) Point)\n    --@ (~type Point)\n    (static property Self.Cached get)\n)\n",
	}
	for i, src := range srcs {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("case %d: new trait syntax should lint clean; got %#v", i, d)
		}
	}
}

func TestNamedTraitSatisfaction(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	const trait = "(trait Drawable (method Self.Draw (self)))\n(fun render (Drawable) Nil)\n(fun render (d) Nil)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"satisfies", trait +
			"(struct Circle.{ R Number })\n(method Circle.Draw (self) self.R)\n(render Circle.{ R 1 })", false},
		{"missing method", trait +
			"(struct Square.{ S Number })\n(render Square.{ S 1 })", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v",
					got, c.wantErr, c.src, AnalyzeFile("t.pho", []byte(c.src)))
			}
		})
	}
}
