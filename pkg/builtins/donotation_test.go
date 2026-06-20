package builtins

import (
	"testing"

	"pho/pkg/core"
)

// `do` notation runs every expression after `do` in sequence and yields the
// last one's value. A leading `do` works directly — `(do …)` IS the
// sequence — so no `identity` wrapper is needed at head position. These run
// the full pipeline (see evalProgram).

func TestDoNotationSequencesAndReturnsLast(t *testing.T) {
	// All three tail expressions run, in order, in the enclosing scope
	// (do introduces no frame), and the value is the last expression's.
	v := evalProgram(t, "(var x 0)\n(do (= x 5) (= x (+ x 1)) x)")
	if v.Kind != core.KindNum || v.Val.(float64) != 6 {
		t.Fatalf("do-notation result = %v (kind %s), want 6", v.Val, v.Kind)
	}
}

// A multi-statement function body uses (identity do …) to sequence its
// forms and yield the last; bodies are bare expressions post-cutover.
func TestDoNotationInFunBody(t *testing.T) {
	src := "(fun addWithLog (a b) (identity do\n" +
		"  (+ a 0)\n" +
		"  (+ a b)))\n" +
		"(addWithLog 3 4)"
	if v := evalProgram(t, src); v.Kind != core.KindNum || v.Val.(float64) != 7 {
		t.Fatalf("addWithLog 3 4 = %v (kind %s), want 7", v.Val, v.Kind)
	}
}

func TestIdentityEchoesArgument(t *testing.T) {
	if v := evalProgram(t, "(identity 42)"); v.Kind != core.KindNum || v.Val.(float64) != 42 {
		t.Fatalf("(identity 42) = %v, want 42", v.Val)
	}
	for _, src := range []string{"(identity)", "(identity 1 2)"} {
		if _, codes := evalProgramDiag(t, src); !hasCode(codes, core.ErrArity) {
			t.Fatalf("%s: expected arity error, got %v", src, codes)
		}
	}
}

// A leading `do` sequences directly: `(do x y z)` evaluates each form in
// order and yields the last, with no `identity` wrapper and no over-nested
// call on the result.
func TestHeadDoSequences(t *testing.T) {
	if v := evalProgram(t, "(do 1 2 3)"); v.Kind != core.KindNum || v.Val.(float64) != 3 {
		t.Fatalf("(do 1 2 3) = %v (kind %s), want 3", v.Val, v.Kind)
	}
}

// `do` notation is a parse-time rewrite, so a `do` recovered from quoted
// data has to be re-applied when the data becomes code again. resume and
// macros must sequence it via core.Do, not leave it a bare `do` identifier
// that resolves to nothing.
func TestResumeAppliesDoNotation(t *testing.T) {
	// resume a quoted do-form directly.
	if v := evalProgram(t, "(resume '(identity do (var a 1) (var b 2) (+ a b)))"); v.Kind != core.KindNum || v.Val.(float64) != 3 {
		t.Fatalf("resume of (identity do …) = %v (kind %s), want 3", v.Val, v.Kind)
	}
	// a head `do` recovered from data sequences in place too — no identity.
	if v := evalProgram(t, "(resume '(do (var a 1) (var b 2) (+ a b)))"); v.Kind != core.KindNum || v.Val.(float64) != 3 {
		t.Fatalf("resume of head (do …) = %v (kind %s), want 3", v.Val, v.Kind)
	}
	// a macro whose returned data builds a do-body (slice of quoted symbols).
	prog := "(macro seq! (x y) (slice 'identity 'do x y))\n(seq! (var n 4) (* n 5))"
	if v := evalProgram(t, prog); v.Kind != core.KindNum || v.Val.(float64) != 20 {
		t.Fatalf("macro-generated do-body = %v (kind %s), want 20", v.Val, v.Kind)
	}
}
