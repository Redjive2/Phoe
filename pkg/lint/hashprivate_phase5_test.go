package lint

import "testing"

// TestHashPrivateMember confirms the new `#` private marker is honored for
// struct members alongside the legacy lowercase rule (Doc/PlanV1/Syntax.md,
// Phase 5): a `#`-prefixed field is private — reachable from `self` inside a
// method, but flagged when accessed from outside — while a public field is not.
func TestHashPrivateMember(t *testing.T) {
	diags := analyze(t, `(struct Box pub ##secret)
(method Box.peek (self) self.##secret)
(let b = Box.{ pub = 1 ##secret = 2 })
b.##secret
`)
	if !hasDiagWithName(diags, "private-member-access", "'#secret'") {
		t.Fatalf("expected private-member-access for b.#secret, got %#v", diags)
	}
	// self.#secret inside the method is privileged — must NOT be flagged.
	// The public field Pub is accessible from outside — also not flagged.
	for _, name := range []string{"'Pub'"} {
		if hasDiagWithName(diags, "private-member-access", name) {
			t.Fatalf("%s should not be flagged private, got %#v", name, diags)
		}
	}
}

// TestHashBindingResolves confirms a `#`-prefixed name is a real identifier to
// the linter (identRe accepts the marker): the binding is registered and
// references to it resolve rather than tripping unresolved-identifier.
func TestHashBindingResolves(t *testing.T) {
	diags := analyze(t, "(let #x = 5)\n(+ #x 1)")
	if hasDiag(diags, "unresolved-identifier") {
		t.Fatalf("#x should resolve to its binding, got %#v", diags)
	}
}
