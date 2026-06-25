package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// A top-level `property` in a library file is a declaration, not a side
// effect: free-standing it exports like a var (read through its getter),
// attached it registers a computed member on its struct. The linter must
// accept it at the top level and resolve both forms across a module
// boundary, mirroring the runtime loader's allow-list.
func TestTopLevelPropertyInLibrary(t *testing.T) {
	lib := `(let var n = 5)
(property twice get (fun () (* n 2)) set (fun (v) (= n (/ v 2))))
(struct Box v)
(property Box.sq get (method Box (self) (* self.v self.v)))
`
	root := writeTree(t, map[string]string{
		"script/std/mylib/lib.phl": lib,
		"script/app.pho": "(import ('std/mylib' m))\n" +
			"(let var a = m.twice)\n" + // free-standing property export resolves
			"(let var b = m.Box.{ v 6 })\n" +
			"(let var c = b.sq)\n" + // attached property member resolves on instance
			"(let var d = m.nope)\n", // sanity: a real unknown export still fires
	})

	// The library itself must lint clean — no phl-side-effect for either
	// property form.
	libPath := filepath.Join(root, "script/std/mylib/lib.phl")
	libSrc, _ := os.ReadFile(libPath)
	if d := AnalyzeFile(libPath, libSrc); hasDiag(d, "phl-side-effect") {
		t.Fatalf("top-level property flagged as a side effect: %#v", d)
	}

	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	diags := AnalyzeFile(app, src)

	if hasDiagWithName(diags, "unknown-export", "Twice") {
		t.Errorf("free-standing property m.Twice should be an export, got %#v", diags)
	}
	if hasDiag(diags, "unknown-member") {
		t.Errorf("attached property b.Sq should resolve as a member, got %#v", diags)
	}
	// Guard the test isn't vacuous: an actual missing export still fires.
	if !hasDiagWithName(diags, "unknown-export", "nope") {
		t.Errorf("expected unknown-export for m.nope, got %#v", diags)
	}
}
