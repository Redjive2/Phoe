package builtins

import (
	"testing"

	"pho/pkg/core"
)

// `(spread arr)` splices an array's elements into ANY call — array literals
// (slice), variadic builtins, append, and user funs — at any position, not
// just as a function parameter marker.
func TestSpreadIntoCalls(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"(let ns = [1 2 3])\n(+ (spread ns))", 6},            // spread is the only arg
		{"(+ 1 (spread [2 3]) 4)", 10},                        // spread between fixed args
		{"[0 (spread [1 2 3]) 4].size", 5},                    // array literal (slice)
		{"(append [0] (spread [1 2 3])).size", 4},             // append
		{"(let f (a b c) = (+ a b c))\n(f (spread [4 5 6]))", 15}, // user fun
	} {
		v := evalProgram(t, tc.src)
		if v.Kind != core.KindNum || v.Val.(float64) != tc.want {
			t.Errorf("%s = %v (%s), want num %v", tc.src, v.Val, v.Kind, tc.want)
		}
	}

	// The spliced elements land in order in an array literal.
	v := evalProgram(t, "[0 (spread [1 2 3]) 4]")
	if v.Kind != core.KindArray {
		t.Fatalf("array-literal spread: got kind %s", v.Kind)
	}
	got := *v.Val.(*[]core.Value)
	want := []float64{0, 1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("spliced array = %v, want length %d", got, len(want))
	}
	for i, w := range want {
		if got[i].Kind != core.KindNum || got[i].Val.(float64) != w {
			t.Errorf("element %d = %v, want %v", i, got[i].Val, w)
		}
	}

	// Spreading a non-array is a clean diagnostic, not a panic.
	if _, codes := evalProgramDiag(t, "[1 (spread 5) 2]"); !hasCode(codes, core.ErrBadSpread) {
		t.Errorf("spreading a non-array should raise %q; got %v", core.ErrBadSpread, codes)
	}
}
