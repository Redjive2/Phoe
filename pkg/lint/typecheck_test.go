package lint

import (
	"testing"

	"pho/pkg/annot"
)

// Stage E1: an inline `(T name)` type on a var/const is checked against the
// initializer, but only when the mismatch is PROVABLE — the gradual guarantee.
func TestTypeAnnotationVarCheck(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{"provable mismatch", "(var (Number x) 'hi')", true},
		{"matching literal", "(var (Number x) 5)", false},
		{"matching string", "(var (String s) 'hi')", false},
		{"const mismatch", "(const (Boolean b) 3)", true},
		// Gradual guarantee: no annotation ⇒ no type check.
		{"un-annotated", "(var x 'hi')", false},
		// A non-literal initializer is Dynamic ⇒ never a false positive.
		{"dynamic init (reference)", "(var y 5)\n(var (Number x) y)", false},
		// Union annotations resolve: a member is accepted, an outsider rejected.
		{"union accepts member", "(var ((Or Number String) x) 5)", false},
		{"union rejects outsider", "(var ((Or Number String) x) True)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := AnalyzeFile("t.pho", []byte(c.src))
			got := hasDiag(d, "type-mismatch")
			if got != c.wantError {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantError, c.src, d)
			}
		})
	}
}

// Stage E2: call arguments are checked against the callee's inline signature,
// the result type flows into further checks, and the gradual guarantee holds
// for references and un-annotated callees.
func TestCallArgSignatureCheck(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	const add = "(fun add (Number Number) Number)\n(fun add (x y) (+ x y))\n"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{"wrong first arg", add + "(const r (add 'x' 1))", true},
		{"wrong second arg", add + "(const r (add 1 'y'))", true},
		{"correct args", add + "(const r (add 1 2))", false},
		{"nested call result feeds arg", add + "(const r (add (add 1 2) 3))", false},
		// Gradual guarantee.
		{"reference arg is dynamic", add + "(var z 9)\n(const r (add z 1))", false},
		{"un-annotated callee", "(fun f (x) x)\n(const r (f 'anything'))", false},
		// The result type flows: add returns Number, so declaring r String mismatches.
		{"result type flows into var", add + "(const (String r) (add 1 2))", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := AnalyzeFile("t.pho", []byte(c.src))
			if got := hasDiag(d, "type-mismatch"); got != c.wantError {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantError, c.src, d)
			}
		})
	}
}

// Stage F: parametric types in signatures — a `(List Number)` parameter checks
// list-literal arguments element-wise (covariant), gradual-safely.
func TestParametricSignatureCheck(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	const sumL = "(fun sumL ((List Number)) Number)\n(fun sumL (xs) 0)\n"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{"correct list literal", sumL + "(const r (sumL [1 2 3]))", false},
		{"wrong element type", sumL + "(const r (sumL ['a' 'b']))", true},
		{"mixed list narrows wider", sumL + "(const r (sumL [1 'x']))", true},
		{"empty list ok", sumL + "(const r (sumL []))", false},
		{"non-list arg", sumL + "(const r (sumL 5))", true},
		{"reference arg is dynamic", sumL + "(var z [1])\n(const r (sumL z))", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := AnalyzeFile("t.pho", []byte(c.src))
			if got := hasDiag(d, "type-mismatch"); got != c.wantError {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantError, c.src, d)
			}
		})
	}
}

// Stage E3: occurrence typing. A `(x.Is? T)` guard narrows a
// union-typed binding to T in the then-arm and to ¬T in the else-arm; `unless`
// inverts.
func TestOccurrenceTyping(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	const decls = "(fun needN (Number) Number)\n(fun needN (n) n)\n" +
		"(fun needS (String) String)\n(fun needS (s) s)\n" +
		"(var ((Or Number String) x) 5)\n"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{"narrowed both arms correct", decls + "(if (x.Is? Number) then (needN x) else (needS x))", false},
		{"then-arm wrong after narrow", decls + "(if (x.Is? Number) then (needS x) else (needN x))", true},
		{"else-arm wrong after narrow", decls + "(if (x.Is? Number) then (needN x) else (needN x))", true},
		{"unguarded union arg", decls + "(needN x)", true},
		{"unless inverts arms", decls + "(unless (x.Is? Number) then (needS x) else (needN x))", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := AnalyzeFile("t.pho", []byte(c.src))
			if got := hasDiag(d, "type-mismatch"); got != c.wantError {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantError, c.src, d)
			}
		})
	}
}
