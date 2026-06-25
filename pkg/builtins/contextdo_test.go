package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Context-aware do: a bare `do` inside an if/unless arm captures only that
// arm's statements, stopping at the next `elif`/`else` boundary instead of
// swallowing the sibling branches. Each case picks a different branch and
// checks that ONLY that arm's side effects ran, proving the arms split
// independently rather than collapsing into the first `do`.
func TestContextAwareDoBranches(t *testing.T) {
	// Three arms, each accumulating a distinct magnitude into x. A correct
	// split runs exactly one arm's two statements; a broken split (the first
	// `do` swallowing the elif/else) would mis-shape the if and error or run
	// the wrong work.
	prog := func(a, b, c bool) string {
		return `(var x 0)
(if ` + boolLit(a) + ` then do
    (= x (+ x 1))
    (= x (+ x 10))
 elif ` + boolLit(b) + ` then do
    (= x (+ x 100))
    (= x (+ x 200))
 else do
    (= x (+ x 1000))
    (= x (+ x 2000)))
x`
	}

	cases := []struct {
		name    string
		a, b, c bool
		want    float64
	}{
		{"first arm", true, false, false, 11},
		{"elif arm", false, true, false, 300},
		{"else arm", false, false, false, 3000},
		{"first wins over elif", true, true, false, 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalProgram(t, prog(tc.a, tc.b, tc.c))
			if v.Kind != core.KindNum {
				t.Fatalf("expected number, got kind %q (%v)", v.Kind, v.Val)
			}
			if got := v.Val.(float64); got != tc.want {
				t.Errorf("x = %v, want %v", got, tc.want)
			}
		})
	}
}

// A trailing `do` arm with no elif/else still captures to the end, and a
// single-statement arm works too.
func TestContextAwareDoTrailing(t *testing.T) {
	src := `(var x 0)
(if True then do
    (= x 5)
    (= x (+ x 2)))
x`
	v := evalProgram(t, src)
	if v.Kind != core.KindNum || v.Val.(float64) != 7 {
		t.Fatalf("trailing do arm: got kind %q val %v, want 7", v.Kind, v.Val)
	}
}

// unless arms are context-aware too: `do` stops at the `else` boundary.
func TestContextAwareDoUnless(t *testing.T) {
	// cond is False, so the `then` arm runs (unless takes the then-arm when
	// the condition is false). It must run ONLY its two statements.
	src := `(var x 0)
(unless False then do
    (= x 1)
    (= x (+ x 4))
 else do
    (= x 99)
    (= x (+ x 1)))
x`
	v := evalProgram(t, src)
	if v.Kind != core.KindNum || v.Val.(float64) != 5 {
		t.Fatalf("unless then arm: got kind %q val %v, want 5", v.Kind, v.Val)
	}
}

func boolLit(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
