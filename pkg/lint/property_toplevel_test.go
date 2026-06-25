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
	lib := `(var n 5)
(property Twice get (fun () (* n 2)) set (fun (v) (= n (/ v 2))))
(struct Box V)
(property Box.Sq get (method Box (self) (* self.V self.V)))
`
	root := writeTree(t, map[string]string{
		"script/std/mylib/lib.phl": lib,
		"script/app.pho": "(import ('std/mylib' m))\n" +
			"(var a m.Twice)\n" + // free-standing property export resolves
			"(var b m.Box.{ V 6 })\n" +
			"(var c b.Sq)\n" + // attached property member resolves on instance
			"(var d m.Nope)\n", // sanity: a real unknown export still fires
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
	if !hasDiagWithName(diags, "unknown-export", "Nope") {
		t.Errorf("expected unknown-export for m.Nope, got %#v", diags)
	}
}
