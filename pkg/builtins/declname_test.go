package builtins

import (
	"testing"

	"pho/pkg/core"
)

// A declaration name is a bare identifier, taken literally: (const x 5)
// declares "x". Post-cutover there is no dynamic / quoted / string-literal
// name form — the name is always the leaf's text.
func TestDeclNameIsLiteral(t *testing.T) {
	got := evalProgram(t, "(const x 5)\nx")
	if got.Kind != core.KindNum || got.Val != float64(5) {
		t.Fatalf("x = %#v, want 5", got)
	}
}

// = with a bare target assigns to that binding.
func TestAssignTarget(t *testing.T) {
	got := evalProgram(t, "(var x 1)\n(= x 5)\nx")
	if got.Kind != core.KindNum || got.Val != float64(5) {
		t.Fatalf("x = %#v, want 5", got)
	}
}
