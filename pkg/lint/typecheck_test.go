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
		{"provable mismatch", "(let var (Number x) = 'hi')", true},
		{"matching literal", "(let var (Number x) = 5)", false},
		{"matching string", "(let var (String s) = 'hi')", false},
		{"const mismatch", "(let (Boolean b) = 3)", true},
		// Gradual guarantee: no annotation ⇒ no type check.
		{"un-annotated", "(let var x = 'hi')", false},
		// A non-literal initializer is Dynamic ⇒ never a false positive.
		{"dynamic init (reference)", "(let var y = 5)\n(let var (Number x) = y)", false},
		// Union annotations resolve: a member is accepted, an outsider rejected.
		{"union accepts member", "(let var ((Or Number String) x) = 5)", false},
		{"union rejects outsider", "(let var ((Or Number String) x) = true)", true},
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
		{"wrong first arg", add + "(let r = (add 'x' 1))", true},
		{"wrong second arg", add + "(let r = (add 1 'y'))", true},
		{"correct args", add + "(let r = (add 1 2))", false},
		{"nested call result feeds arg", add + "(let r = (add (add 1 2) 3))", false},
		// Gradual guarantee.
		{"reference arg is dynamic", add + "(let var z = 9)\n(let r = (add z 1))", false},
		{"un-annotated callee", "(fun f (x) x)\n(let r = (f 'anything'))", false},
		// The result type flows: add returns Number, so declaring r String mismatches.
		{"result type flows into var", add + "(let (String r) = (add 1 2))", true},
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

	const sumL = "(fun sum_l ((List Number)) Number)\n(fun sum_l (xs) 0)\n"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{"correct list literal", sumL + "(let r = (sum_l [1 2 3]))", false},
		{"wrong element type", sumL + "(let r = (sum_l ['a' 'b']))", true},
		{"mixed list narrows wider", sumL + "(let r = (sum_l [1 'x']))", true},
		{"empty list ok", sumL + "(let r = (sum_l []))", false},
		{"non-list arg", sumL + "(let r = (sum_l 5))", true},
		{"reference arg is dynamic", sumL + "(let var z = [1])\n(let r = (sum_l z))", false},
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

	const decls = "(fun need_n (Number) Number)\n(fun need_n (n) n)\n" +
		"(fun need_s (String) String)\n(fun need_s (s) s)\n" +
		"(let var ((Or Number String) x) = 5)\n"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{"narrowed both arms correct", decls + "(if (x.is? Number) then (need_n x) else (need_s x))", false},
		{"then-arm wrong after narrow", decls + "(if (x.is? Number) then (need_s x) else (need_n x))", true},
		{"else-arm wrong after narrow", decls + "(if (x.is? Number) then (need_n x) else (need_n x))", true},
		{"unguarded union arg", decls + "(need_n x)", true},
		{"unless inverts arms", decls + "(unless (x.is? Number) then (need_s x) else (need_n x))", false},
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
