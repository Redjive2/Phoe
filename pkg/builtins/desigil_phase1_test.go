package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Phase 1 of the de-sigiling cutover: the declaration and control builtins
// read BARE forms — `(fun add (x y) (+ x y))`, `(if c then else)` — with no
// '/& sigils. These programs eval directly (evalProgram does not lint), so
// they exercise the runtime in isolation from the linter (Phase 2). A
// multi-statement body uses `(identity do …)`, the post-cutover sequencing
// form, and a string-keyed dict reads back through bracket access.
func TestDesigiledBuiltins(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want float64
	}{
		{"var + ref", "(let var x = 5)\nx", 5},
		{"const + ref", "(let k = 7)\nk", 7},
		{"fun named", "(let square (n) = (* n n))\n(square 6)", 36},
		{"fun anon", "(let var dbl = (fun (n) (* n 2)))\n(dbl 9)", 18},
		{"if then", "(if true then 1 else 2)", 1},
		{"if else", "(if false then 1 else 2)", 2},
		{"assign", "(let var x = 1)\n(= x 10)\nx", 10},
		{"foreach iterator", "(let var s = 0)\n(foreach n in [1 2 3 4] (= s (+ s n)))\ns", 10},
		{"while loop", "(let var i = 0)\n(while (< i 3) then (= i (+ i 1)))\ni", 3},
		{"multi-stmt body", "(let f (n) = (identity do (let var t = (* n 2)) (+ t 1)))\n(f 5)", 11},
		{"string dict key read by bracket", "(let var m = [ 'k' -> 9 ])\nm.['k']", 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalProgram(t, c.src)
			if got.Kind != core.KindNum {
				t.Fatalf("eval(%q) = kind %s, want num", c.src, got.Kind)
			}
			if n := got.Val.(float64); n != c.want {
				t.Errorf("eval(%q) = %v, want %v", c.src, n, c.want)
			}
		})
	}
}

// TestDesigiledStructMethod exercises bare struct + method together: a
// mutating method with a multi-statement body, called twice. Construction
// uses the bare-key `Counter.{ Value 10 step 3 }` form.
func TestDesigiledStructMethod(t *testing.T) {
	src := `(struct Counter value #step)
(let Counter.bump (self) = (identity do (= self.value (+ self.value self.#step)) self.value))
(let var c = Counter.{ value = 10 #step = 3 })
(c.bump)
(c.bump)`
	got := evalProgram(t, src)
	if got.Kind != core.KindNum || got.Val.(float64) != 16 {
		t.Errorf("Bump twice from 10 step 3 = %#v, want num 16", got)
	}
}
