package builtins

import (
	"testing"

	"pho/pkg/core"
)

// The revised block notation (Doc/Features.md): `&expr` is a one-argument
// function whose implicit parameter is `it`, so `&(+ it 1)` is a ready-made
// mapper. `&do …` turns the rest of the form into the block's do-body, and a
// literal like `&Nil` is a block that ignores its argument.

func TestBlockItMapper(t *testing.T) {
	apply := "(fun apply (x f) (f x))\n"
	cases := []struct {
		src  string
		want float64
	}{
		{apply + "(apply 5 &(+ it 1))", 6},
		{apply + "(apply 4 &(* it it))", 16},
		{apply + "(apply 10 &(- it 3))", 7},
	}
	for _, tc := range cases {
		v := evalProgram(t, tc.src)
		if v.Kind != core.KindNum || v.Val.(float64) != tc.want {
			t.Errorf("%s = %v (%s), want %v", tc.src, v.Val, v.Kind, tc.want)
		}
	}
}

func TestBlockLiteralIgnoresIt(t *testing.T) {
	// &Nil is a block; called with an argument it still yields Nil. Called with
	// no argument the optional `it` is Nil — either way the body is the literal.
	if v := evalProgram(t, "(fun apply (x f) (f x))\n(apply 99 &none)"); v.Kind != core.KindNil {
		t.Fatalf("&Nil applied = %v (%s), want Nil", v.Val, v.Kind)
	}
	if v := evalProgram(t, "(fun call0 (f) (f))\n(call0 &42)"); v.Kind != core.KindNum || v.Val.(float64) != 42 {
		t.Fatalf("&42 called with no arg = %v (%s), want 42", v.Val, v.Kind)
	}
}

func TestBlockDoBodyCapturesRestOfForm(t *testing.T) {
	// &do sequences the rest of the form as the body; `it` is the argument.
	src := "(fun apply (x f) (f x))\n" +
		"(apply 3 &do\n" +
		"  (let y = (+ it 1))\n" +
		"  (* y 10))"
	if v := evalProgram(t, src); v.Kind != core.KindNum || v.Val.(float64) != 40 {
		t.Fatalf("&do block = %v (%s), want 40", v.Val, v.Kind)
	}
}

// Each block call gets its own `it`: a nested block shadows the outer one.
func TestBlockItIsPerCall(t *testing.T) {
	src := "(fun apply (x f) (f x))\n" +
		"(apply 2 &(apply 10 &(* it it)))" // inner it = 10 → 100; outer it=2 unused
	if v := evalProgram(t, src); v.Kind != core.KindNum || v.Val.(float64) != 100 {
		t.Fatalf("nested block = %v (%s), want 100", v.Val, v.Kind)
	}
}
