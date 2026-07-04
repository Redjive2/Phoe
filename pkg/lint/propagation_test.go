package lint

import (
	"testing"

	"pho/pkg/annot"
)

// Forward type propagation: a top-level const's inferred type flows to later
// references, so `(const a (f x)) … (g a)` is checked even though nothing is
// annotated at the use site. CONST only — a var is reassignable, so its
// initializer type isn't propagated (that would be unsound). A gradual result
// (an unannotated call) stays Dynamic and never fires.
func TestConstTypePropagation(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	f := "(fun f (Number) String)\n(let f (n) = 's')\n"   // f  : Number -> String
	g := "(fun g (Number) None)\n(let g (n) = none)\n"    // g  : Number -> Nil
	gs := "(fun gs (String) None)\n(let gs (s) = none)\n" // gs : String -> Nil
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"call-result chain", f + g + "(let a = (f 5))\n(g a)", true},
		{"multi-hop const", f + g + "(let a = (f 5))\n(let b = a)\n(g b)", true},
		{"const matches expected", f + gs + "(let a = (f 5))\n(gs a)", false},
		{"var is NOT propagated", f + g + "(let var a = (f 5))\n(g a)", false},
		{"unannotated call result stays gradual", g + "(let h (x) = x)\n(let a = (h 5))\n(g a)", false},
		{"const literal stays precise", g + gs + "(let a = 5)\n(gs a)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
