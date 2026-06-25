package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// Capitalized var/const are package exports: an importer reads them as
// pkg.Name with no unknown-export, but assigning to one is flagged
// read-only. A lowercase top-level binding stays unexported.
func TestExportedVarConst(t *testing.T) {
	root := writeTree(t, map[string]string{
		"script/std/cfg/cfg.phl":   "(let pi = 3)\n(let var count = 0)\n(let lower = 9)\n",
		"script/std/app/read.phl":  "(import 'std/cfg')\n(fun read_pi () cfg.pi)\n(fun read_count () cfg.count)\n",
		"script/std/app/write.phl": "(import 'std/cfg')\n(fun bump () (= cfg.count 5))\n",
		"script/std/app/priv.phl":  "(import 'std/cfg')\n(fun peek () cfg.#lower)\n",
	})

	analyze := func(rel string) []Diagnostic {
		p := filepath.Join(root, rel)
		src, _ := os.ReadFile(p)
		return AnalyzeFile(p, src)
	}

	// Reading the exported const/var resolves — no unknown-export.
	if d := analyze("script/std/app/read.phl"); hasDiag(d, "unknown-export") {
		t.Errorf("cfg.Pi / cfg.Count are exported; got unknown-export: %#v", d)
	}

	// Assigning to an exported var from another module is rejected.
	if d := analyze("script/std/app/write.phl"); !hasDiag(d, "readonly-module-member") {
		t.Errorf("(= cfg.Count 5) should be readonly-module-member; got %#v", d)
	}

	// A lowercase top-level binding is not an export.
	if d := analyze("script/std/app/priv.phl"); !hasDiag(d, "unknown-export") {
		t.Errorf("cfg.lower (lowercase) must not be exported; got %#v", d)
	}
}
