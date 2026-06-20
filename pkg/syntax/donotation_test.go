package syntax

import (
	"strings"
	"testing"

	"pho/pkg/core"
)

// lowerInspect lexes, parses and lowers `src`, then renders the lowered tree.
// Inspect un-mangles the core.Do head back to the readable `do` keyword (like
// dot/slice/map), so a head-position do-form already renders with a stable
// `do` — no suffix folding needed. The ReplaceAll still folds the mangled
// suffix where core.Do survives as quoted string DATA (see the quote test
// below), which Inspect doesn't un-mangle.
func lowerInspect(src string) string {
	toks, _ := LexPos(src)
	tree, _ := ParsePos(toks)
	got := core.Inspect(Lower(tree))
	return strings.ReplaceAll(got, core.Do, "do!")
}

// `do` notation: a non-head `do` captures every following sibling into a
// single (Do …) sub-call; a head `do` is renamed in place so the form IS the
// (Do …) call. The outer pair of parens in each `want` is Lower's top-level
// form wrapper. Inspect renders the core.Do head as the readable `do`.
func TestDoNotationDesugar(t *testing.T) {
	cases := []struct{ src, want string }{
		// Non-head `do` — the tail becomes one (do …) arg.
		{"(identity do x y)", "((identity (do x y)))"},
		{"(fun f (a) do x y)", "((fun f (a) (do x y)))"},
		// Only the tail after `do` is captured; leading siblings stay put.
		{"(f a b do x)", "((f a b (do x)))"},
		// Head `do` sequences in place — no extra nesting, no identity needed.
		{"(do x y)", "((do x y))"},
		// No `do`: untouched.
		{"(f x y)", "((f x y))"},
	}
	for _, c := range cases {
		if got := lowerInspect(c.src); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, got, c.want)
		}
	}
}

// The sugar fires inside quotes too, so a quoted body carries the (Do …)
// structure as data — this is what makes `'(identity do …)` work as a fun
// body once it is Derepr'd and evaluated.
func TestDoNotationDesugarsInsideQuote(t *testing.T) {
	got := lowerInspect("'(identity do x y)")
	if !strings.Contains(got, `"do!"`) {
		t.Fatalf("quoted do-notation did not carry the (Do …) head as data: %s", got)
	}
}
