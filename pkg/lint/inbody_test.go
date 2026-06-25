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

	g := "(fun g (Number) Nil)\n(fun g (n) Nil)\n"    // g  : Number -> Nil
	gs := "(fun gs (String) Nil)\n(fun gs (s) Nil)\n" // gs : String -> Nil
	h := "(fun h (Number) String)\n(fun h (n) 's')\n" // h  : Number -> String
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// New in-body coverage.
		{"typed param misused in body", g + "(fun f (String) Nil)\n(fun f (x) (g x))\n", true},
		{"in-body const chain", g + h + "(fun f (n) do (const a (h n)) (g a))\n", true},
		{"in-body literal const", gs + "(fun f () do (const a 5) (gs a))\n", true},
		{"local const shadow is used", "(const x 5)\n" + g + "(fun f () do (const x 's') (g x))\n", true},

		// Soundness.
		{"typed param used correctly", g + "(fun f (Number) Nil)\n(fun f (x) (g x))\n", false},
		{"in-body var is not propagated", g + h + "(fun f () do (var a (h 5)) (g a))\n", false},
		{"local shadow resolved in body scope, not file scope", "(const x 's')\n" + g + "(fun f () do (const x 5) (g x))\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
