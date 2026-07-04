package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Operator overloading — Features.md §7. `(operator Recv.OP (Self …) Ret)`
// declares an overload the adjacent `(let Recv.OP …)` clauses implement; the
// primitive prefix operators and the index forms dispatch to it when the first
// operand is a struct instance of that type.

// A binary `+` overload: `(+ myNum n)` dispatches to My-Num.+.
func TestOperatorBinaryPlus(t *testing.T) {
	base := "(struct My-Num v)\n" +
		"(operator My-Num.+ (Self Number) My-Num)\n" +
		"(let My-Num.+ (self other) = My-Num.{ v = (+ self.v other) })\n" +
		"(let a = My-Num.{ v = 10 })\n"
	evalNum(t, base+"(let b = (+ a 5))\nb.v\n", 15)
	// The primitive still works on plain numbers.
	evalNum(t, base+"(+ 2 3)\n", 5)
}

// A comparison overload returns a boolean and dispatches on the left operand.
func TestOperatorComparison(t *testing.T) {
	base := "(struct My-Num v)\n" +
		"(operator My-Num.< (Self Number) Boolean)\n" +
		"(let My-Num.< (self other) = (< self.v other))\n" +
		"(let a = My-Num.{ v = 3 })\n"
	wantBool := func(src string, want bool) {
		t.Helper()
		v := evalProgram(t, src)
		if v.Kind != core.KindBool || v.Val.(bool) != want {
			t.Errorf("eval(%q) = %v (%s), want bool %v", src, v.Val, v.Kind, want)
		}
	}
	wantBool(base+"(< a 5)\n", true)
	wantBool(base+"(< a 1)\n", false)
}

// The `[]` read operator: `box.[i]` dispatches to Box.[].
func TestOperatorIndexRead(t *testing.T) {
	base := "(struct Box items)\n" +
		"(operator Box.[] (Self Number) Number)\n" +
		"(let Box.[] (self i) = (* self.items.[i] 10))\n" +
		"(let b = Box.{ items = [1 2 3] })\n"
	evalNum(t, base+"b.[0]\n", 10)
	evalNum(t, base+"b.[2]\n", 30)
}

// The `[]=` write operator with a `(var Self)` receiver: `(= box.[i] v)`
// dispatches to Box.[]= and the in-place mutation persists.
func TestOperatorIndexWrite(t *testing.T) {
	base := "(struct Box items)\n" +
		"(operator Box.[]= ((var Self) Number Number) Number)\n" +
		"(let Box.[]= (self i v) = (= self.items.[i] v))\n" +
		"(let var b = Box.{ items = [1 2 3] })\n"
	evalNum(t, base+"(= b.[1] 99)\nb.items.[1]\n", 99)
}

// Read and write overloads coexist on the same type.
func TestOperatorIndexReadWrite(t *testing.T) {
	base := "(struct Box items)\n" +
		"(operator Box.[] (Self Number) Number)\n" +
		"(let Box.[] (self i) = self.items.[i])\n" +
		"(operator Box.[]= ((var Self) Number Number) Number)\n" +
		"(let Box.[]= (self i v) = (= self.items.[i] v))\n" +
		"(let var b = Box.{ items = [10 20 30] })\n"
	evalNum(t, base+"(= b.[2] 5)\nb.[2]\n", 5)
}

// A non-overloadable operator name is rejected at declaration.
func TestOperatorBadName(t *testing.T) {
	_, codes := evalProgramDiag(t, "(struct My-Num v)\n(operator My-Num.foo (Self Number) My-Num)\n")
	if !hasCode(codes, "bad-form") {
		t.Fatalf("(operator My-Num.foo …) should be bad-form, got %v", codes)
	}
}
