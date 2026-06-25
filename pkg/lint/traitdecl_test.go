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
		"(struct Circle.{ r Number })\n(method Circle.draw (self) self.r)\n(trait Drawable (method self.draw (self)))\n(let c = Circle.{ r 5 })\n(let d = (c.is? Drawable))\n",
		// empty extends () is allowed
		"(trait Named () (property self.name get))\n",
		// the user's example: static members + a member annotation
		"(struct Point.{ x Number y Number })\n(trait Memo\n    (static method self.calc (self) Point)\n    --@ (~type Point)\n    (static property self.cached get)\n)\n",
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

	const trait = "(trait Drawable (method self.draw (self)))\n(fun render (Drawable) none)\n(fun render (d) none)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"satisfies", trait +
			"(struct Circle.{ r Number })\n(method Circle.draw (self) self.r)\n(render Circle.{ r 1 })", false},
		{"missing method", trait +
			"(struct Square.{ s Number })\n(render Square.{ s 1 })", true},
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
