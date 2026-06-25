package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTree creates files under a temp root from rel-path → content.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const ioLib = `(fun visible () 1)
(struct Reader id)
`

// An entry script's import resolves against its own directory.
func TestResolveImportFromScriptDir(t *testing.T) {
	root := writeTree(t, map[string]string{
		"script/std/io/io.phl": ioLib,
		"script/app.pho":       "(import 'std/io')\n(let var x = (io.visible))\n(let var y = (io.nope))\n",
	})
	app := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(app)
	diags := AnalyzeFile(app, src)
	if !hasDiagWithName(diags, "unknown-export", "nope") {
		t.Fatalf("expected unknown-export for io.nope (import must resolve), got %#v", diags)
	}
	if hasDiagWithName(diags, "unknown-export", "visible") {
		t.Fatalf("io.visible is exported — no diagnostic expected, got %#v", diags)
	}
}

// A nested library's "std/…" import resolves by walking ancestors to
// the script root — regardless of the process cwd.
func TestResolveImportFromNestedLibrary(t *testing.T) {
	root := writeTree(t, map[string]string{
		"script/std/io/io.phl":     ioLib,
		"script/std/pctl/pctl.phl": "(import 'std/io')\n(fun use () (io.visible))\n(fun bad () (io.nope))\n",
	})
	pctl := filepath.Join(root, "script/std/pctl/pctl.phl")
	src, _ := os.ReadFile(pctl)
	diags := AnalyzeFile(pctl, src)
	if !hasDiagWithName(diags, "unknown-export", "nope") {
		t.Fatalf("expected unknown-export via ancestor-resolved import, got %#v", diags)
	}
	if hasDiagWithName(diags, "unknown-export", "visible") {
		t.Fatalf("io.visible must validate, got %#v", diags)
	}
}

// Shape inference through an ancestor-resolved constructor: member
// checks fire on instances of imported structs.
func TestResolveImportFeedsShapeInference(t *testing.T) {
	root := writeTree(t, map[string]string{
		"script/std/io/io.phl": ioLib,
		"script/std/app/a.phl": "(import 'std/io')\n(fun go () (identity do\n  (let var r = io.Reader.{ id 1 })\n  (let var x = r.id)\n  (let var y = r.bogus)))\n",
	})
	a := filepath.Join(root, "script/std/app/a.phl")
	src, _ := os.ReadFile(a)
	diags := AnalyzeFile(a, src)
	if !hasDiagWithName(diags, "unknown-member", "bogus") {
		t.Fatalf("expected unknown-member on imported struct instance, got %#v", diags)
	}
	if hasDiagWithName(diags, "unknown-member", "id") {
		t.Fatalf("r.id is a real field, got %#v", diags)
	}
}

// Definition.Path resolution is absolute, making package identity
// comparable across importing files.
func TestResolveImportPathIsAbsolute(t *testing.T) {
	root := writeTree(t, map[string]string{
		"script/std/io/io.phl": ioLib,
		"script/app.pho":       "(import 'std/io')\n(let var x = (io.visible))\n",
	})
	got := resolveImportPath(filepath.Join(root, "script/app.pho"), "std/io")
	want := filepath.Join(root, "script/std/io")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

// Unresolvable paths fall back to the raw string (legacy cwd-relative
// behavior preserved).
func TestResolveImportFallback(t *testing.T) {
	if got := resolveImportPath("/nonexistent/dir/x.pho", "no/such/pkg"); got != "no/such/pkg" {
		t.Fatalf("expected raw fallback, got %s", got)
	}
}
