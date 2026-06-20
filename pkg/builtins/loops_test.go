package builtins

import (
	"testing"

	"pho/pkg/core"
)

func num(t *testing.T, src string, want float64) {
	t.Helper()
	v := evalProgram(t, src)
	if v.Kind != core.KindNum || v.Val.(float64) != want {
		t.Errorf("%s = %v (%s), want num %v", src, v.Val, v.Kind, want)
	}
}

// foreach iterates arrays, strings (by rune), and dicts (by key); `in` is a
// required keyword. while/until are the conditional loops with `then`.
func TestLoopForms(t *testing.T) {
	num(t, "(var s 0)\n(foreach n in [1 2 3 4] (= s (+ s n)))\ns", 10)        // array
	num(t, "(var c 0)\n(foreach ch in \"abcde\" (= c (+ c 1)))\nc", 5)        // string (runes)
	num(t, "(var k 0)\n(foreach key in { 1 10 2 20 } (= k (+ k key)))\nk", 3) // dict keys (1+2)

	num(t, "(var i 0)\n(while (< i 5) then (= i (+ i 1)))\ni", 5)  // while: loop while true
	num(t, "(var j 0)\n(until (== j 5) then (= j (+ j 1)))\nj", 5) // until: loop until true

	// break exits the nearest loop; the sum stops before n == 3.
	num(t, "(var s 0)\n(foreach n in [1 2 3 4 5] (identity do (if (== n 3) then (break)) (= s (+ s n))))\ns", 3)
}

// The noop keywords are mandatory and foreach is iteration-only.
func TestLoopFormErrors(t *testing.T) {
	for _, src := range []string{
		"(foreach x of [1 2 3] (= x x))",          // wrong keyword (not `in`)
		"(var i 0)\n(while (< i 1) when (= i 1))", // wrong keyword (not `then`)
		"(var i 0)\n(until (< i 1) when (= i 1))", // wrong keyword (not `then`)
	} {
		if _, codes := evalProgramDiag(t, src); !hasCode(codes, core.ErrBadForm) {
			t.Errorf("%q should raise %q; got %v", src, core.ErrBadForm, codes)
		}
	}
}
