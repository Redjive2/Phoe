package syntax

import "testing"

// TestArrowToken confirms `->` lexes as a single token (the map key/value
// separator), not as `-` followed by `>`.
func TestArrowToken(t *testing.T) {
	if got := tokenValues(t, "a -> b"); len(got) != 3 || got[1] != "->" {
		t.Fatalf("tokens = %v, want [a -> b]", got)
	}
}

// TestMapBracketDesugar pins the new `[k -> v]` map literal: it lowers to the
// same (map …) as the legacy `{k v}` form, while an arrow-free `[…]` stays a
// list (Doc/PlanV1/Syntax.md, Phase 4). The outer parens are Lower's wrapper.
func TestMapBracketDesugar(t *testing.T) {
	cases := []struct{ src, want string }{
		// `[k -> v]` and `{k v}` lower to the same map.
		{"[:k -> :v]", "([:k -> :v])"},
		{"[:k -> :v]", "([:k -> :v])"},
		{"[:a -> 1 :b -> 2]", "([:a -> 1 :b -> 2])"},
		// Empty list vs empty map.
		{"[]", "([])"},
		{"[->]", "([])"},
		// Arrow-free `[…]` is still a list.
		{"[1 2 3]", "([1 2 3])"},
	}
	for _, c := range cases {
		if got := lowerInspect(c.src); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, got, c.want)
		}
	}
}

// TestStructInitDesugar pins the struct-construction sugar: the new
// `P.{ field = value }` form and the old `P.{ field value }` pair form both
// splice into a constructor call with quoted field names, and private `#field`
// keys are accepted.
func TestStructInitDesugar(t *testing.T) {
	cases := []struct{ src, want string }{
		{"P.{ x = 1 }", "((p 'x' 1))"},
		{"P.{ x 1 }", "((p 'x' 1))"},
		{"P.{ x = 1 y = 2 }", "((p 'x' 1 'y' 2))"},
		{"P.{ #x = 1 }", "((p '#x' 1))"},
	}
	for _, c := range cases {
		if got := lowerInspect(c.src); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, got, c.want)
		}
	}
}
