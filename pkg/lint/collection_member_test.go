package lint

import "testing"

// A method or property declared on the generic Collection type (String | List |
// Map) resolves when accessed on a concrete list, string, or map: the linter
// expands the union receiver across its members, mirroring the runtime's
// union-receiver dispatch. A genuinely-unknown member still flags.
func TestCollectionMemberResolves(t *testing.T) {
	clean := []string{
		"(method Collection.foo (self) self)\n(let a = [1 2].foo)\n(let b = 'x'.foo)\n(let c = [ 'k' -> 1 ].foo)\n",
		"(property Collection.big? get (method Collection (self) (> self.size 1)))\n(let a = [1 2].big?)\n(let b = 'x'.big?)\n",
	}
	for _, src := range clean {
		d := AnalyzeFile("t.phl", []byte(src))
		if hasDiag(d, "unknown-member") {
			t.Errorf("Collection member must resolve on a concrete list/string/map\n  %q\n  → %#v", src, d)
		}
	}
	if d := AnalyzeFile("t.phl", []byte("(let a = [1 2].nope)\n")); !hasDiagWithName(d, "unknown-member", "Nope") {
		t.Errorf("a genuinely-unknown member should still flag; got %#v", d)
	}
}
