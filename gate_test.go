package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoLegacyErrorPrints is the Phase 4 finish-line gate: every runtime
// error now flows through the diagnostic session (ctx.Errorf / EmitPanic)
// or, for host-layer failures, a `pho:` stderr line — never the legacy
// `fmt.Println("(ERR) ... @ 'pkg.func'.")` pattern that wrote unstructured
// lines to stdout. This walks the source tree and fails if any
// non-test .go file reintroduces an "(ERR)" or "(WARN)" marker, so the
// migration can't silently regress.
func TestNoLegacyErrorPrints(t *testing.T) {
	markers := []string{"(ERR)", "(WARN)"}
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range markers {
			if strings.Contains(string(src), m) {
				t.Errorf("%s contains legacy error marker %q — route runtime errors through ctx.Errorf (or a pho: stderr line for host errors) instead", path, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
