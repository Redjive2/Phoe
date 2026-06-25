package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// A `Collection` (String|List|Map) method/property declared in one package
// resolves on a concrete string/list/map in an IMPORTING file — the import's
// member surface must expand the union receiver across its members, mirroring
// the same-file behavior and the runtime. Regression for cross-module
// Collection resolution.
func TestCrossModuleCollectionMember(t *testing.T) {
	lib := "(method Collection.shout (self) self)\n" +
		"(property Collection.big? get (method Collection (self) (> self.size 1)))\n"
	root := writeTree(t, map[string]string{
		"script/std/coll/coll.phl": lib,
		"script/app.pho": "(import 'std/coll')\n" +
			"(let var a = 'hi'.shout)\n" + // String inherits the Collection method
			"(let var b = [1 2].shout)\n" + // List too
			"(let var c = [ 'k' -> 1 ].shout)\n" + // Map too
			"(let var d = [1 2].big?)\n" + // a Collection property, cross-module
			"(let var e = [1 2].nope)\n", // sanity: an unknown member still fires
	})

	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	d := AnalyzeFile(app, src)

	for _, member := range []string{"Shout", "Big?"} {
		if hasDiagWithName(d, "unknown-member", member) {
			t.Errorf("cross-module Collection member %q should resolve on String/List/Map; got %#v", member, d)
		}
	}
	if !hasDiagWithName(d, "unknown-member", "nope") {
		t.Errorf("a genuinely-unknown member should still fire; got %#v", d)
	}
	// Importing a package purely for its extension methods is a USE — the import
	// must not be flagged unused.
	if hasDiag(d, "unused-import") {
		t.Errorf("an import used only via its extension members should not be unused; got %#v", d)
	}
}

// An extension member called on a receiver whose static shape is UNKNOWN (an
// untyped parameter, a slice expression) still counts as using the import that
// provides it — the member can't be type-checked, but the import is required at
// runtime for the call to resolve. Regression for the unused-import false
// positive on untyped receivers (std/random.Shuffle's `slice.Concat`).
func TestExtensionUseOnUnknownReceiver(t *testing.T) {
	lib := "(method List.concat (self (spread lists)) self)\n"
	root := writeTree(t, map[string]string{
		"script/std/coll/coll.phl": lib,
		// `a` is an untyped parameter, so the receiver `a.[: 1]` (and `a`
		// itself) has an unknown static shape — the member access can't be
		// checked, yet the import is genuinely used.
		"script/app.pho": "(import 'std/coll')\n" +
			"(fun f (a b) (a.[: 1].concat b))\n",
	})

	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	d := AnalyzeFile(app, src)

	if hasDiag(d, "unused-import") {
		t.Errorf("an import used via an extension member on an untyped receiver should not be unused; got %#v", d)
	}

	// Control: an import whose extensions are NOT among the file's member
	// accesses is still flagged — the suppression is keyed to the member name,
	// not blanket-applied to every import.
	root2 := writeTree(t, map[string]string{
		"script/std/coll/coll.phl": lib,
		"script/app.pho": "(import 'std/coll')\n" +
			"(fun g (a) (a.nonexistent))\n",
	})
	app2 := filepath.Join(root2, "script/app.pho")
	src2, _ := os.ReadFile(app2)
	if d2 := AnalyzeFile(app2, src2); !hasDiag(d2, "unused-import") {
		t.Errorf("an import providing no accessed member should still be unused; got %#v", d2)
	}
}
