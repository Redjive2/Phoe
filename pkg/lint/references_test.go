package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refsFromFile invokes ReferencesAt with the cursor inside `needle`'s
// first occurrence on the given line of `file`.
func refsFromFile(t *testing.T, root, file string, line int, lineText, needle string) []DefSite {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	col := strings.Index(lineText, needle)
	if col < 0 {
		t.Fatalf("needle %q not in %q", needle, lineText)
	}
	return ReferencesAt(root, file, src, line, col+1)
}

func sitesByFile(sites []DefSite) map[string]int {
	out := map[string]int{}
	for _, s := range sites {
		out[s.File]++
	}
	return out
}

// Package siblings: a helper declared in a.phl and used in b.phl is
// found from both sides.
func TestReferencesAcrossSiblings(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/a.phl": "(fun Helper () 1)\n(fun UseLocal () (Helper))\n",
		"lib/b.phl": "(fun UseRemote () (Helper))\n",
	})
	a := filepath.Join(root, "lib/a.phl")
	b := filepath.Join(root, "lib/b.phl")

	// From the declaration in a.phl.
	sites := refsFromFile(t, root, a, 1, "(fun Helper () 1)", "Helper")
	got := sitesByFile(sites)
	if got[a] != 2 || got[b] != 1 {
		t.Fatalf("expected 2 sites in a.phl (decl + local use) and 1 in b.phl, got %#v", sites)
	}

	// From the use site in b.phl — same answer.
	sites = refsFromFile(t, root, b, 1, "(fun UseRemote () (Helper))", "Helper")
	got = sitesByFile(sites)
	if got[a] != 2 || got[b] != 1 {
		t.Fatalf("expected identical results from the b.phl side, got %#v", sites)
	}
}

// Importers: an exported fun referenced via alias.Member in a separate
// program is found from the declaration, and vice versa.
func TestReferencesAcrossImporters(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.phl": "(fun Visible () 1)\n",
		"app.pho":     "(import 'lib')\n(var x (lib.Visible))\n",
	})
	lib := filepath.Join(root, "lib/lib.phl")
	app := filepath.Join(root, "app.pho")

	sites := refsFromFile(t, root, lib, 1, "(fun Visible () 1)", "Visible")
	got := sitesByFile(sites)
	if got[lib] != 1 || got[app] != 1 {
		t.Fatalf("expected decl in lib + use in app, got %#v", sites)
	}

	// From the importer side.
	sites = refsFromFile(t, root, app, 2, "(var x (lib.Visible))", "Visible")
	got = sitesByFile(sites)
	if got[lib] != 1 || got[app] != 1 {
		t.Fatalf("expected identical results from the app side, got %#v", sites)
	}
}

// Struct members across packages: method calls and field reads on
// instances constructed via (pkg.Struct ...) in an importer.
func TestReferencesMemberAcrossImporters(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.phl": "(struct Thing Part)\n(method Thing.Grow (self) self.Part)\n",
		"app.pho":     "(import 'lib')\n(var t lib.Thing.{ Part 1 })\n(var a (t.Grow))\n(var b t.Part)\n",
	})
	lib := filepath.Join(root, "lib/lib.phl")
	app := filepath.Join(root, "app.pho")

	// Method: decl in lib + call in app.
	sites := refsFromFile(t, root, lib, 2, "(method Thing.Grow (self) self.Part)", "Grow")
	got := sitesByFile(sites)
	if got[lib] != 1 || got[app] != 1 {
		t.Fatalf("expected Grow decl + app call, got %#v", sites)
	}

	// Field, from its declaration inside the struct form: decl +
	// self.Part in lib, t.Part in app (the dict key in the constructor
	// is not a field reference).
	sites = refsFromFile(t, root, lib, 1, "(struct Thing Part)", "Part")
	got = sitesByFile(sites)
	if got[lib] != 2 || got[app] != 1 {
		t.Fatalf("expected field decl + self read in lib, read in app, got %#v", sites)
	}
}

// Std-style nested layout: an export referenced from a cousin package
// through an ancestor-resolved "std/…" import.
func TestReferencesStdStyleLayout(t *testing.T) {
	root := writeTree(t, map[string]string{
		"script/std/io/io.phl":     "(fun Visible () 1)\n",
		"script/std/pctl/pctl.phl": "(import 'std/io')\n(fun Use () (io.Visible))\n",
	})
	io := filepath.Join(root, "script/std/io/io.phl")
	pctl := filepath.Join(root, "script/std/pctl/pctl.phl")

	sites := refsFromFile(t, root, io, 1, "(fun Visible () 1)", "Visible")
	got := sitesByFile(sites)
	if got[io] != 1 || got[pctl] != 1 {
		t.Fatalf("expected decl in io + use in pctl, got %#v", sites)
	}
}

// Locality: params and import aliases never leave their file, and no
// cross-file reads happen at all for them.
func TestReferencesLocalityNoScan(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/a.phl": "(import 'lib')\n(fun F (count) (+ count 1))\n",
		"lib/b.phl": "(fun G (count) (+ count 2))\n",
	})
	a := filepath.Join(root, "lib/a.phl")
	src, _ := os.ReadFile(a)

	reads := 0
	SetSourceReader(func(path string) ([]byte, error) {
		reads++
		return os.ReadFile(path)
	})
	defer SetSourceReader(nil)

	// Param `count` in F: line 2.
	col := strings.Index("(fun F (count) (+ count 1))", "count")
	sites := ReferencesAt(root, a, src, 2, col+1)
	for _, s := range sites {
		if s.File != a {
			t.Fatalf("param references must stay in-file, got %#v", sites)
		}
	}
	if len(sites) != 2 {
		t.Fatalf("expected param decl + 1 use, got %#v", sites)
	}
	// PackageScope reads siblings for the base analysis (b.phl), but
	// no candidate-file reference walks should have happened beyond
	// that — b.phl analyzed would have re-read a.phl too.
	if reads > 2 {
		t.Fatalf("expected no cross-file reference scans for a param, got %d reads", reads)
	}
}

// Unsaved-buffer overlay: an importer's edited content (served via
// SetSourceReader) is what gets searched — not what's on disk.
func TestReferencesSeeOverlay(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/lib.phl": "(fun Visible () 1)\n",
		"app.pho":     "(import 'lib')\n",
	})
	lib := filepath.Join(root, "lib/lib.phl")
	app := filepath.Join(root, "app.pho")

	edited := "(import 'lib')\n(var x (lib.Visible))\n(var y (lib.Visible))\n"
	SetSourceReader(func(path string) ([]byte, error) {
		if path == app {
			return []byte(edited), nil
		}
		return os.ReadFile(path)
	})
	defer SetSourceReader(nil)

	sites := refsFromFile(t, root, lib, 1, "(fun Visible () 1)", "Visible")
	got := sitesByFile(sites)
	if got[app] != 2 {
		t.Fatalf("expected 2 app sites from the edited buffer, got %#v", sites)
	}
}

// A .pho program's top-level decls aren't importable — references stay
// in-file even with a workspace root.
func TestReferencesProgramDeclsAreLocal(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.pho":  "(fun Helper () 1)\n(var x (Helper))\n",
		"other.pho": "(fun Helper () 2)\n",
	})
	main := filepath.Join(root, "main.pho")
	sites := refsFromFile(t, root, main, 1, "(fun Helper () 1)", "Helper")
	for _, s := range sites {
		if s.File != main {
			t.Fatalf("program decls must stay in-file, got %#v", sites)
		}
	}
	if len(sites) != 2 {
		t.Fatalf("expected decl + use in main.pho, got %#v", sites)
	}
}
