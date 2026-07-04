package lint

import "testing"

// A single-clause callable whose only clause is a TOTAL pattern — one that
// matches every value of its slot's type — is exhaustive and must not draw a
// non-exhaustive-clauses warning. A pattern that NARROWS the dispatch space (a
// literal, a type test on a wider slot, a literal list/field) still does.
func TestExhaustiveTotalPatterns(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantWarn bool
	}{
		// The reported case: an all-binder struct destructure of the receiver.
		{
			"all-binder struct destructure covers the receiver",
			"(struct Box.{ Number #inner })\n" +
				"(method Box.peek (Self) Number)\n" +
				"(let Box.peek (Box.{ #inner = v }) = v)\n",
			false,
		},
		{
			"Self.{ … } destructure covers the receiver",
			"(struct Box.{ Number #inner })\n" +
				"(method Box.peek (Self) Number)\n" +
				"(let Box.peek (Self.{ #inner = v }) = v)\n",
			false,
		},
		{
			"(Type x) test covering the sig's slot type",
			"(fun f (Number) Number)\n(let f ((Number n)) = n)\n",
			false,
		},
		{
			"all-binder list pattern",
			"(fun f (List) Number)\n(let f ([a b]) = a)\n",
			false,
		},
		// Genuine narrowing — the warning must still fire.
		{
			"(Number x) test on a wider Unknown slot narrows",
			"(fun f (Unknown) Number)\n(let f ((Number n)) = 1)\n",
			true,
		},
		{
			"a literal list element narrows",
			"(fun f (List) Number)\n(let f ([0 b]) = b)\n",
			true,
		},
		{
			"a literal struct field narrows",
			"(struct Box.{ Number #inner })\n" +
				"(method Box.peek (Self) Number)\n" +
				"(let Box.peek (Box.{ #inner = 0 }) = 1)\n",
			true,
		},
		{
			"a single literal clause narrows",
			"(fun f (Number) Number)\n(let f (0) = 1)\n",
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasDiag(analyze(t, tc.src), "non-exhaustive-clauses"); got != tc.wantWarn {
				t.Errorf("non-exhaustive-clauses = %v, want %v", got, tc.wantWarn)
			}
		})
	}
}
