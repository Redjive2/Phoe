package lint

import (
	"testing"

	"pho/pkg/annot"
)

// stageE runs each source through the analyzer and asserts whether a
// type-mismatch fires, with the annotation library loaded.
func stageE(t *testing.T, cases []struct {
	name    string
	src     string
	wantErr bool
}) {
	t.Helper()
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := AnalyzeFile("t.pho", []byte(c.src))
			if got := hasDiag(d, "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantErr, c.src, d)
			}
		})
	}
}

// Return-type checking: a function's return points (the body's tail
// expression(s) and every explicit `(return …)`) must inhabit the declared
// result type. Un-inferable returns stay gradual.
func TestReturnTypeChecking(t *testing.T) {
	stageE(t, []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"tail ok (param)", "(fun f (Number) Number)\n(let f (n) = n)", false},
		{"tail wrong (param)", "(fun g (Number) String)\n(let g (n) = n)", true},
		{"tail wrong (literal)", "(fun h (Number) Number)\n(let h (n) = 's')", true},
		{"both if-arms checked", "(fun k (Number) Number)\n(let k (n) = (if (> n 0) then 1 else 2))", false},
		{"one if-arm wrong", "(fun k (Number) Number)\n(let k (n) = (if (> n 0) then 1 else 's'))", true},
		{"union result accepts members", "(fun m (Number) (Or Number None))\n(let m (n) = (if (> n 0) then n else none))", false},
		{"explicit return wrong", "(fun e (Number) Number)\n(fun e (n) do (return 's') n)", true},
		{"explicit return ok", "(fun e (Number) Number)\n(fun e (n) do (return 0) n)", false},
		// Gradual: an un-annotated function isn't return-checked; a Dynamic tail
		// (a call to an un-annotated helper) never flags.
		{"un-annotated fun", "(let u (n) = 's')", false},
		{"dynamic tail", "(let helper (x) = x)\n(fun d (Number) Number)\n(let d (n) = (helper n))", false},
		// A nested function's own return doesn't count against the outer one.
		{"nested fun return ignored", "(fun o (Number) Number)\n(fun o (n) do (let g = (fun (x) 's')) n)", false},
	})
}

// Method-call argument checking: `(x.M args…)` is checked against M's harvested
// methodsig when x's struct is statically known.
func TestMethodCallArgChecking(t *testing.T) {
	const decl = "(struct P x)\n(method P.add (P Number) Number)\n(let P.add (self n) = n)\n(let p = P.{ x = 1 })\n"
	stageE(t, []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"arg ok", decl + "(p.add 5)", false},
		{"arg wrong", decl + "(p.add 's')", true},
		// Gradual: an un-annotated method, or an unknown receiver, isn't checked.
		{"un-annotated method", "(struct Q x)\n(let Q.m (self n) = n)\n(let q = Q.{ x = 1 })\n(q.m 's')", false},
		{"unknown receiver", "(method P.add (P Number) Number)\n(struct P x)\n(let P.add (self n) = n)\n(z.add 's')", false},
	})
}

// Assignment checking: `(= x v)` validates v against x's DECLARED type — not a
// narrowed flow type, so reassigning a narrowed union member stays clean.
func TestAssignmentChecking(t *testing.T) {
	stageE(t, []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"assign ok", "(let var (Number x) = 5)\n(= x 6)", false},
		{"assign wrong", "(let var (Number x) = 5)\n(= x 's')", true},
		{"un-annotated gradual", "(let var y = 5)\n(= y 's')", false},
		// Reassigning a narrowed union var to the OTHER member is valid (checked
		// against the DECLARED union, not the narrowed type) — no false positive.
		{"reassign narrowed union", "(let var ((Or Number String) u) = 5)\n(if (u.is? Number) then (= u 's'))", false},
	})
}
