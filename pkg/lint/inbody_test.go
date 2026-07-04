package lint

import (
	"testing"

	"pho/pkg/annot"
)

// In-body inference: the gradual checker descends into function/method bodies
// using the body's own scope (so params and locals resolve, and a local
// correctly shadows a top-level binding) with params bound to their signature
// types and in-body consts propagated. This is where most code lives, so it is
// the largest coverage jump — and it must stay sound (vars don't propagate; a
// shadowing local is resolved in its own scope, not the file scope).
func TestInBodyInference(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	g := "(fun g (Number) None)\n(let g (n) = none)\n"    // g  : Number -> Nil
	gs := "(fun gs (String) None)\n(let gs (s) = none)\n" // gs : String -> Nil
	h := "(fun h (Number) String)\n(let h (n) = 's')\n"   // h  : Number -> String
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// New in-body coverage.
		{"typed param misused in body", g + "(fun f (String) None)\n(let f (x) = (g x))\n", true},
		{"in-body const chain", g + h + "(let f (n) = do (let a = (h n)) (g a))\n", true},
		{"in-body literal const", gs + "(let f () = do (let a = 5) (gs a))\n", true},
		{"local const shadow is used", "(let x = 5)\n" + g + "(let f () = do (let x = 's') (g x))\n", true},

		// Soundness.
		{"typed param used correctly", g + "(fun f (Number) None)\n(let f (x) = (g x))\n", false},
		{"in-body var is not propagated", g + h + "(let f () = do (let var a = (h 5)) (g a))\n", false},
		{"local shadow resolved in body scope, not file scope", "(let x = 's')\n" + g + "(let f () = do (let x = 5) (g x))\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
