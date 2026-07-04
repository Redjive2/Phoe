package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Dynamic indexing uses the bracket form `coll.[expr]` (and `coll.[a : b]`
// for slices). Bare `coll.name` is field syntax, reserved for structs and
// packages; using it on a dict/array/string is an error. These tests run
// the full lex→parse→lower→eval pipeline (see evalProgram) so they prove
// the runtime accessor, not just the lowered tree shape.

func TestBracketIndexReads(t *testing.T) {
	num := func(src string) float64 {
		t.Helper()
		v := evalProgram(t, src)
		if v.Kind != core.KindNum {
			t.Fatalf("%s: got kind %q (%v), want num", src, v.Kind, v.Val)
		}
		return v.Val.(float64)
	}

	if got := num("(let var arr = [10 20 30])\n(let var i = 1)\narr.[i]"); got != 20 {
		t.Errorf("arr.[i] = %v, want 20", got)
	}
	if got := num("(let var arr = [10 20 30])\narr.[0]"); got != 10 {
		t.Errorf("arr.[0] = %v, want 10", got)
	}
	if got := num("(let var d = ['a' -> 1 'b' -> 2])\n(let var k = 'b')\nd.[k]"); got != 2 {
		t.Errorf("d.[k] = %v, want 2", got)
	}
	if got := num("(let var d = ['a' -> 1 'b' -> 2])\nd.['a']"); got != 1 {
		t.Errorf("d.[\"a\"] = %v, want 1", got)
	}
	// The fractional-decimal hack is a bare numeric RHS, NOT indexing, and
	// must keep working.
	if got := num("3.14"); got < 3.13 || got > 3.15 {
		t.Errorf("3.14 = %v, want ~3.14", got)
	}
}

func TestBracketSliceReads(t *testing.T) {
	v := evalProgram(t, "(let var arr = [10 20 30 40])\narr.[1 : 3]")
	if v.Kind != core.KindArray {
		t.Fatalf("arr.[1 : 3] kind = %q, want array", v.Kind)
	}
	if got := *v.Val.(*[]core.Value); len(got) != 2 || got[0].Val.(float64) != 20 || got[1].Val.(float64) != 30 {
		t.Errorf("arr.[1 : 3] = %v, want [20 30]", got)
	}
}

func TestBracketIndexWrites(t *testing.T) {
	if got := evalProgram(t, "(let var arr = [10 20 30])\n(= arr.[2] 99)\narr.[2]"); got.Val.(float64) != 99 {
		t.Errorf("after (= arr.[2] 99), arr.[2] = %v, want 99", got.Val)
	}
	if got := evalProgram(t, "(let var d = ['a' -> 1])\n(= d.['c'] 3)\nd.['c']"); got.Val.(float64) != 3 {
		t.Errorf("after (= d.[\"c\"] 3), d.[\"c\"] = %v, want 3", got.Val)
	}
}

// Bare dynamic indexing — the pre-change syntax — is now a no-field error
// that steers the user to the bracket form, for arrays, strings and dicts,
// on both reads and writes.
func TestBareDynamicIndexIsError(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{"array read by var", "(let f (xs) = (identity do (let var i = 0) xs.#i))\n(f [1 2 3])", core.ErrField},
		{"array read by num", "(let f (xs) = xs.0)\n(f [1 2 3])", core.ErrField},
		{"string read", "(let f (s) = (identity do (let var i = 0) s.#i))\n(f 'hi')", core.ErrField},
		{"dict read literal key", "(let f (d) = d.'a')\n(f ['a' -> 1])", core.ErrField},
		{"array write", "(let f (xs) = (= xs.0 9))\n(f [1 2 3])", core.ErrField},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, codes := evalProgramDiag(t, c.src)
			if !hasCode(codes, c.code) {
				t.Fatalf("%s: got codes %v, want %q", c.src, codes, c.code)
			}
		})
	}
}

// Malformed brackets and shape/op mismatches inside the bracket world.
func TestBracketFormErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{"multi-element index", "(let var arr = [1 2 3])\narr.[0 1]", core.ErrBadForm},
		{"empty bracket", "(let var arr = [1 2 3])\narr.[]", core.ErrBadForm},
		{"slice a dict", "(let var d = ['a' -> 1])\nd.[0 : 1]", core.ErrBadForm},
		{"assign to a slice", "(let var arr = [1 2 3])\n(= arr.[0 : 1] 9)", core.ErrBadAssign},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, codes := evalProgramDiag(t, c.src)
			if !hasCode(codes, c.code) {
				t.Fatalf("%s: got codes %v, want %q", c.src, codes, c.code)
			}
		})
	}
}

// Field access on structs stays bare; bracketing a struct access is the
// inverse mistake and is rejected.
func TestStructFieldAccessStaysBare(t *testing.T) {
	field := "(struct P x)\n(let var p = P.{ x = 7 })\np.x"
	if got := evalProgram(t, field); got.Kind != core.KindNum || got.Val.(float64) != 7 {
		t.Errorf("p.X = %v, want 7", got.Val)
	}

	bracketed := "(struct P x)\n(let var p = P.{ x = 7 })\np.['X']"
	if _, codes := evalProgramDiag(t, bracketed); !hasCode(codes, core.ErrField) {
		t.Errorf("p.[\"X\"] should be a no-field error, got codes %v", codes)
	}
}
