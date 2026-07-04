package lint

import "testing"

// A method on a primitive/union/abstract type (the object model) treats `self`
// as a VALUE of that type, not a struct instance: indexing it and accessing its
// real members (built-in, universal, or extension) are valid — never reported
// as struct-field access. (An UNKNOWN member on such a self is still flagged as
// unknown-member, exactly like any primitive member access — see
// TestScalarAccess.) A method on a real struct still field-checks `self`.
func TestMethodSelfShapeByReceiverKind(t *testing.T) {
	clean := []string{
		"(let Collection.last (self) = self.[(- self.size 1)])\n", // union receiver: indexable
		"(let String.shout (self) = self.size)\n",                 // primitive: built-in member resolves
		"(let Number.wat (self) = self.is?)\n",                    // primitive: universal member resolves
		"(let List.at (self i) = self.[i])\n",                     // collection self is indexable
	}
	for _, src := range clean {
		d := AnalyzeFile("t.phl", []byte(src))
		if hasDiag(d, "unknown-member") || hasDiag(d, "invalid-member-access") {
			t.Errorf("non-struct receiver: a valid self.member or index must not be flagged\n  %q\n  → %#v", src, d)
		}
	}

	// A real struct method still flags a bad field on self.
	d := AnalyzeFile("t.phl", []byte("(struct Point x y)\n(let Point.bad (self) = self.#nope)\n"))
	if !hasDiagWithName(d, "unknown-member", "nope") {
		t.Errorf("struct method should still flag a bad field on self; got %#v", d)
	}
}
