package builtins

import "testing"

// The lambda builtin (Features.md §11): a flat-header anonymous callable. At
// runtime the types, return type, receiver type, and effect suffix are all
// erased — the value is a plain callable whose receiver, when present, is simply
// its first parameter named `self`.
func TestLambdaRuntime(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want float64
	}{
		{"free, untyped params", "(let f = (lambda a b -> (+ a b)))\n(f 2 3)", 5},
		{"typed param + return type", "(let f = (lambda (Integer n) Integer -> (+ n 1)))\n(f 4)", 5},
		{"implicit receiver (self is arg 0)", "(let f = (lambda self other -> (+ self other)))\n(f 10 5)", 15},
		{"explicit receiver type", "(let f = (lambda Integer self other -> (+ self other)))\n(f 10 5)", 15},
		{"inferred-receiver keyword Self", "(let f = (lambda Self self other -> (+ self other)))\n(f 1 2)", 3},
		{"no params, return type only", "(let f = (lambda Number -> 42))\n(f)", 42},
		{"no params at all", "(let f = (lambda -> 7))\n(f)", 7},
		{"effect suffix erased (!)", "(let f = (lambda! a -> a))\n(f 9)", 9},
		{"self-mut suffix with receiver (=)", "(let f = (lambda= self x -> (+ self x)))\n(f 1 2)", 3},
		{"combined suffix ?!=", "(let f = (lambda?!= a -> a))\n(f 8)", 8},
		{"closes over its scope", "(let base = 100)\n(let f = (lambda n -> (+ n base)))\n(f 5)", 105},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { evalNum(t, tc.src, tc.want) })
	}
}

// Malformed lambda headers are rejected with a bad-form error.
func TestLambdaErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"missing arrow", "(lambda a b)"},
		{"self among params", "(lambda a self -> a)"},
		{"bare type in param position", "(lambda a Number b -> a)"},
		{"two bodies", "(lambda a -> a a)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, diags := evalProgramDiag(t, tc.src)
			if !hasCode(diags, "bad-form") {
				t.Fatalf("expected a bad-form diagnostic, got %v", diags)
			}
		})
	}
}
