package lint

import (
	"testing"

	"pho/pkg/annot"
)

// These lock in the decl/impl-split `=` integration in the checkers directly
// (hand-written `(= …)` impls), independent of the codemod. Without them the
// fixes are only exercised by migrated code and could silently regress.

// checkFlow must descend into a `(let f (params) = body)` impl with its own scope +
// signature-typed params, so an in-body type misuse is caught (typecheck.go).
func TestDeclImplEqInBodyInference(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	g := "(fun g (Number) None)\n(let g (n) = none)\n" // g : Number -> Nil, impl via `=`
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"typed param misused in = impl body", g + "(fun f (String) None)\n(let f (x) = (g x))\n", true},
		{"typed param used correctly in = impl", g + "(fun f (Number) None)\n(let f (x) = (g x))\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}

// A top-level `(let name (params) = body)` impl is a definition permitted in a .phl
// library; a 2-arg `(= target value)` reassignment stays a side effect
// (checkers.go checkPhlSideEffects, mirroring modload.isLibraryForm).
func TestDeclImplEqPhlSideEffect(t *testing.T) {
	d := AnalyzeFile("library.phl", []byte("(fun f (Number) Number)\n(let f (x) = x)\n"))
	if hasDiag(d, "phl-side-effect") {
		t.Errorf("a top-level `=` impl should be allowed in a .phl library, got %#v", d)
	}
	d = AnalyzeFile("library.phl", []byte("(let var x = 5)\n(= x 7)\n"))
	if !hasDiag(d, "phl-side-effect") {
		t.Errorf("a top-level `=` reassignment is a side effect and should be flagged in a .phl library")
	}
}

// The effect hover works on a `(= Owner.name …)` method impl and reports its
// effect (effects.go callableEffectLabel gating off declOf, not the raw head).
func TestDeclImplEqMethodEffectHover(t *testing.T) {
	src := "(struct Counter n)\n(let Counter.bump! ((var self) by) = (= self.n (+ self.n by)))\n"
	md, _, ok := HoverAt("t.pho", []byte(src), 2, 14) // on `bump!`
	if !ok {
		t.Fatal("expected a hover for the `=` method bump!")
	}
	if !containsSub(md, "mutates") {
		t.Errorf("bump! hover should report a self-mutating effect, got:\n%s", md)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
