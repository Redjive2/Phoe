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

// TestStructInitDesugar pins the struct-construction sugar: `P.{ field = value }`
// splices into a constructor call with quoted field names. Private `#field`
// keys and predicate/effect `field?`/`field!` keys are all quoted as names.
func TestStructInitDesugar(t *testing.T) {
	cases := []struct{ src, want string }{
		{"P.{ x = 1 }", "((P 'x' 1))"},
		{"P.{ x = 1 y = 2 }", "((P 'x' 1 'y' 2))"},
		{"P.{ #x = 1 }", "((P '#x' 1))"},
		// Predicate `?` / effect `!` suffixes are part of the field name.
		{"P.{ ok? = true }", "((P 'ok?' true))"},
		{"P.{ #ok? = true }", "((P '#ok?' true))"},
		{"P.{ go! = 1 }", "((P 'go!' 1))"},
		// Kebab-case field names (interior '-') are quoted as a single key.
		{"P.{ my-field = 1 }", "((P 'my-field' 1))"},
		{"P.{ is-fish? = false }", "((P 'is-fish?' false))"},
	}
	for _, c := range cases {
		if got := lowerInspect(c.src); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, got, c.want)
		}
	}
}

// TestBareConstructionRejected pins the hard rule that the bare `field value`
// construction form (no `=`) is a parse error, while the `field = value` form
// and typed-field lists (`{ Type name }`) parse cleanly.
func TestBareConstructionRejected(t *testing.T) {
	const want = "must use 'field = value'"
	if errs := parseAll("(let p = P.{ x 1 y 2 })"); !hasMessageContaining(errs, want) {
		t.Fatalf("bare construction should be rejected, got %#v", errs)
	}
	for _, ok := range []string{"(let p = P.{ x = 1 })", "(struct P.{ Number x })"} {
		if errs := parseAll(ok); hasMessageContaining(errs, want) {
			t.Fatalf("%q must not be flagged, got %#v", ok, errs)
		}
	}
}
