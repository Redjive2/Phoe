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
		"script/std/coll/coll.phl": "(struct Node.{ Value Number Next Node })\n(struct B.{ X Number })\n",
		"script/app.pho": "(import 'std/coll')\n" +
			"(const n coll.Node.{ Value 1 Next Nil })\n" +
			"(const ok1 n.Next.Value)\n" +
			"(const deep n.Next.Next.Value)\n" +
			"(struct A.{ Inner coll.B })\n" +
			"(const a A.{ Inner coll.B.{ X 1 } })\n" +
			"(const ok2 a.Inner.X)\n",
	})
	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	if d := AnalyzeFile(app, src); len(d) != 0 {
		t.Errorf("valid imported-struct navigation should be clean; got %#v", d)
	}

	bad := writeTree(t, map[string]string{
		"script/std/coll/coll.phl": "(struct Node.{ Value Number Next Node })\n(struct B.{ X Number })\n",
		"script/app.pho": "(import 'std/coll')\n" +
			"(const n coll.Node.{ Value 1 Next Nil })\n" +
			"(const x n.Next.Next.Nope)\n" + // deep recursive typo, cross-module
			"(struct A.{ Inner coll.B })\n" +
			"(const a A.{ Inner coll.B.{ X 1 } })\n" +
			"(const y a.Inner.Zap)\n", // imported-struct field typo
	})
	app2 := filepath.Join(bad, "script/app.pho")
	src2, _ := os.ReadFile(app2)
	d := AnalyzeFile(app2, src2)
	for _, member := range []string{"Nope", "Zap"} {
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
		"script/std/geo/geo.phl":     "(struct Point.{ X Number Y Number })\n",
		"script/std/shape/shape.phl": "(import 'std/geo')\n(struct Shape.{ Origin geo.Point })\n",
	}
	files["script/app.pho"] = "(import 'std/shape')\n(const s shape.Shape.{ Origin Nil })\n(const ok s.Origin.X)\n"
	root := writeTree(t, files)
	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	if d := AnalyzeFile(app, src); len(d) != 0 {
		t.Errorf("transitive imported-struct navigation should be clean; got %#v", d)
	}

	files["script/app.pho"] = "(import 'std/shape')\n(const s shape.Shape.{ Origin Nil })\n(const bad s.Origin.Nope)\n"
	root = writeTree(t, files)
	app = filepath.Join(root, "script/app.pho")
	src, _ = os.ReadFile(app)
	if !hasDiagWithName(AnalyzeFile(app, src), "unknown-member", "Nope") {
		t.Errorf("a typo through a transitive imported struct should fire")
	}
}
