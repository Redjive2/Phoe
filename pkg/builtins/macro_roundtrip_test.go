package builtins

import (
	"testing"

	"pho/pkg/core"
)

// These guard the quote-system round-trip: a macro returns data that
// `resume` reconstructs (TreeifyVal -> Derepr -> evaluate). The fix in
// TreeifyVal adds a quote level so a string literal survives Derepr's
// single strip as a string, instead of collapsing into an (unresolved)
// bare identifier — while identifiers and numbers still round-trip.

// A bare string survives pause/resume as a string.
func TestResumeString(t *testing.T) {
	got := evalProgram(t, `(resume (pause "hello"))`)
	if got.Kind != core.KindStr || got.Val != "hello" {
		t.Fatalf(`(resume (pause "hello")) = {%v %#v}, want str "hello"`, got.Kind, got.Val)
	}
}

// Strings inside an array survive too — before the fix they de-stringed
// into unresolved identifiers, yielding [Nil Nil].
func TestResumeArrayOfStrings(t *testing.T) {
	got := evalProgram(t, `(resume (pause ["a" "b"]))`)
	arr, ok := got.Val.(*[]core.Value)
	if !ok {
		t.Fatalf("expected array, got kind %v", got.Kind)
	}
	if len(*arr) != 2 ||
		(*arr)[0].Kind != core.KindStr || (*arr)[0].Val != "a" ||
		(*arr)[1].Kind != core.KindStr || (*arr)[1].Val != "b" {
		t.Fatalf(`round-tripped array = %#v, want ["a" "b"] as strings`, *arr)
	}
}

// Numbers must still round-trip (the fix only touches the string case).
func TestResumeArrayOfNumbers(t *testing.T) {
	got := evalProgram(t, `(resume (pause [1 2]))`)
	arr, ok := got.Val.(*[]core.Value)
	if !ok {
		t.Fatalf("expected array, got kind %v", got.Kind)
	}
	if len(*arr) != 2 ||
		(*arr)[0].Kind != core.KindNum || (*arr)[0].Val != float64(1) ||
		(*arr)[1].Kind != core.KindNum || (*arr)[1].Val != float64(2) {
		t.Fatalf("round-tripped array = %#v, want [1 2] as nums", *arr)
	}
}

// A nested array (a macro building (head "strlit" ident) shapes) keeps
// its string literals while the head/identifier resolve as code — the
// exact mix the pipe!-style macros rely on.
func TestResumeNestedMixedRoundTrip(t *testing.T) {
	got := evalProgram(t, `(resume (pause [["x" 1] ["y" 2]]))`)
	outer, ok := got.Val.(*[]core.Value)
	if !ok || len(*outer) != 2 {
		t.Fatalf("expected 2-element array, got %#v", got.Val)
	}
	first, ok := (*outer)[0].Val.(*[]core.Value)
	if !ok || len(*first) != 2 || (*first)[0].Kind != core.KindStr || (*first)[0].Val != "x" || (*first)[1].Val != float64(1) {
		t.Fatalf(`first pair = %#v, want ["x" 1]`, (*outer)[0].Val)
	}
}
