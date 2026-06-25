package lint

import "testing"

// A method or property declared on the generic Collection type (String | List |
// Map) resolves when accessed on a concrete list, string, or map: the linter
// expands the union receiver across its members, mirroring the runtime's
// union-receiver dispatch. A genuinely-unknown member still flags.
func TestCollectionMemberResolves(t *testing.T) {
	clean := []string{
		"(method Collection.Foo (self) self)\n(const a [1 2].Foo)\n(const b 'x'.Foo)\n(const c { 'k' 1 }.Foo)\n",
		"(property Collection.Big? get (method Collection (self) (> self.Size 1)))\n(const a [1 2].Big?)\n(const b 'x'.Big?)\n",
	}
	for _, src := range clean {
		d := AnalyzeFile("t.phl", []byte(src))
		if hasDiag(d, "unknown-member") {
			t.Errorf("Collection member must resolve on a concrete list/string/map\n  %q\n  → %#v", src, d)
		}
	}
	if d := AnalyzeFile("t.phl", []byte("(const a [1 2].Nope)\n")); !hasDiagWithName(d, "unknown-member", "Nope") {
		t.Errorf("a genuinely-unknown member should still flag; got %#v", d)
	}
}
