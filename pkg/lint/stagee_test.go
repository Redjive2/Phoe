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
		{"tail ok (param)", "(fun f (Number) Number)\n(fun f (n) n)", false},
		{"tail wrong (param)", "(fun g (Number) String)\n(fun g (n) n)", true},
		{"tail wrong (literal)", "(fun h (Number) Number)\n(fun h (n) 's')", true},
		{"both if-arms checked", "(fun k (Number) Number)\n(fun k (n) (if (> n 0) then 1 else 2))", false},
		{"one if-arm wrong", "(fun k (Number) Number)\n(fun k (n) (if (> n 0) then 1 else 's'))", true},
		{"union result accepts members", "(fun m (Number) (Or Number Nil))\n(fun m (n) (if (> n 0) then n else Nil))", false},
		{"explicit return wrong", "(fun e (Number) Number)\n(fun e (n) do (return 's') n)", true},
		{"explicit return ok", "(fun e (Number) Number)\n(fun e (n) do (return 0) n)", false},
		// Gradual: an un-annotated function isn't return-checked; a Dynamic tail
		// (a call to an un-annotated helper) never flags.
		{"un-annotated fun", "(fun u (n) 's')", false},
		{"dynamic tail", "(fun helper (x) x)\n(fun d (Number) Number)\n(fun d (n) (helper n))", false},
		// A nested function's own return doesn't count against the outer one.
		{"nested fun return ignored", "(fun o (Number) Number)\n(fun o (n) do (const g (fun (x) 's')) n)", false},
	})
}

// Method-call argument checking: `(x.M args…)` is checked against M's harvested
// methodsig when x's struct is statically known.
func TestMethodCallArgChecking(t *testing.T) {
	const decl = "(struct P X)\n(method P.Add (P Number) Number)\n(method P.Add (self n) n)\n(const p P.{ X 1 })\n"
	stageE(t, []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"arg ok", decl + "(p.Add 5)", false},
		{"arg wrong", decl + "(p.Add 's')", true},
		// Gradual: an un-annotated method, or an unknown receiver, isn't checked.
		{"un-annotated method", "(struct Q X)\n(method Q.M (self n) n)\n(const q Q.{ X 1 })\n(q.M 's')", false},
		{"unknown receiver", "(method P.Add (P Number) Number)\n(struct P X)\n(method P.Add (self n) n)\n(z.Add 's')", false},
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
		{"assign ok", "(var (Number x) 5)\n(= x 6)", false},
		{"assign wrong", "(var (Number x) 5)\n(= x 's')", true},
		{"un-annotated gradual", "(var y 5)\n(= y 's')", false},
		// Reassigning a narrowed union var to the OTHER member is valid (checked
		// against the DECLARED union, not the narrowed type) — no false positive.
		{"reassign narrowed union", "(var ((Or Number String) u) 5)\n(if (u.Is? Number) then (= u 's'))", false},
	})
}
