package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// Struct-field navigation crosses the import boundary: an imported package's
// recursive/nested struct navigates (node.Next.Next), and a local struct whose
// field is typed as an imported struct (`Inner pkg.B`) navigates into the
// imported member surface. Typos fire at any depth; valid accesses are clean.
func TestImportedStructNavigation(t *testing.T) {
	root := writeTree(t, map[string]string{
		"script/std/coll/coll.phl": "(struct Node.{ Number value Node next })\n(struct B.{ Number x })\n",
		"script/app.pho": "(import 'std/coll')\n" +
			"(let n = coll.Node.{ value = 1 next = none })\n" +
			"(let ok1 = n.next.value)\n" +
			"(let deep = n.next.next.value)\n" +
			"(struct A.{ coll.B inner })\n" +
			"(let a = A.{ inner = coll.B.{ x = 1 } })\n" +
			"(let ok2 = a.inner.x)\n",
	})
	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	if d := AnalyzeFile(app, src); len(d) != 0 {
		t.Errorf("valid imported-struct navigation should be clean; got %#v", d)
	}

	bad := writeTree(t, map[string]string{
		"script/std/coll/coll.phl": "(struct Node.{ Number value Node next })\n(struct B.{ Number x })\n",
		"script/app.pho": "(import 'std/coll')\n" +
			"(let n = coll.Node.{ value = 1 next = none })\n" +
			"(let x = n.next.next.nope)\n" + // deep recursive typo, cross-module
			"(struct A.{ coll.B inner })\n" +
			"(let a = A.{ inner = coll.B.{ x = 1 } })\n" +
			"(let y = a.inner.zap)\n", // imported-struct field typo
	})
	app2 := filepath.Join(bad, "script/app.pho")
	src2, _ := os.ReadFile(app2)
	d := AnalyzeFile(app2, src2)
	for _, member := range []string{"nope", "zap"} {
		if !hasDiagWithName(d, "unknown-member", member) {
			t.Errorf("a typo %q through an imported struct should fire; got %#v", member, d)
		}
	}
}

// A field inside an imported struct typed as a struct from a FURTHER import
// (`pkg2.Foo`) navigates transitively — the importing program never names the
// innermost package. shape.Shape.Origin is geo.Point; app imports only shape.
func TestTransitiveImportedNavigation(t *testing.T) {
	files := map[string]string{
		"script/std/geo/geo.phl":     "(struct Point.{ Number x Number y })\n",
		"script/std/shape/shape.phl": "(import 'std/geo')\n(struct Shape.{ geo.Point origin })\n",
	}
	files["script/app.pho"] = "(import 'std/shape')\n(let s = shape.Shape.{ origin = none })\n(let ok = s.origin.x)\n"
	root := writeTree(t, files)
	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	if d := AnalyzeFile(app, src); len(d) != 0 {
		t.Errorf("transitive imported-struct navigation should be clean; got %#v", d)
	}

	files["script/app.pho"] = "(import 'std/shape')\n(let s = shape.Shape.{ origin = none })\n(let bad = s.origin.nope)\n"
	root = writeTree(t, files)
	app = filepath.Join(root, "script/app.pho")
	src, _ = os.ReadFile(app)
	if !hasDiagWithName(AnalyzeFile(app, src), "unknown-member", "nope") {
		t.Errorf("a typo through a transitive imported struct should fire")
	}
}
