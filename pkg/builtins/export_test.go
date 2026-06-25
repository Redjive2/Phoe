package builtins

import (
	"os"
	"path/filepath"
	"testing"

	"pho/pkg/core"
	"pho/pkg/modload"
)

// A package exports every public top-level binding — including var and
// const — and an exported const/var reads back as its value. Visibility is
// the '#' prefix: a '#'-prefixed binding stays private. (The read-only-from-
// outside rule is enforced by the `=` builtin and the linter; see decl.go /
// checkAssign.)
func TestPackageExportsVarConst(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "cfg.phl"),
		[]byte("(let pi = 3)\n(let var count = 0)\n(let lower = 9)\n(let #secret = 7)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := modload.LoadPackage(pkgDir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}

	if v, ok := pkg.Exports["pi"]; !ok || v.Kind != core.KindNum || v.Val.(float64) != 3 {
		t.Errorf("const pi should export as num 3; got %v (ok=%v)", v, ok)
	}
	if v, ok := pkg.Exports["count"]; !ok || v.Kind != core.KindNum || v.Val.(float64) != 0 {
		t.Errorf("var count should export as num 0; got %v (ok=%v)", v, ok)
	}
	if v, ok := pkg.Exports["lower"]; !ok || v.Kind != core.KindNum || v.Val.(float64) != 9 {
		t.Errorf("public 'lower' should export as num 9; got %v (ok=%v)", v, ok)
	}
	if _, ok := pkg.Exports["#secret"]; ok {
		t.Errorf("'#secret' (private) must not be exported")
	}
	if _, ok := pkg.Exports["secret"]; ok {
		t.Errorf("'#secret' (private) must not be exported under stripped name")
	}
}
