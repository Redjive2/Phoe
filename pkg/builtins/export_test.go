package builtins

import (
	"os"
	"path/filepath"
	"testing"

	"pho/pkg/core"
	"pho/pkg/modload"
)

// A package exports every capitalized top-level binding — including var
// and const — and an exported const/var reads back as its value. A
// lowercase binding stays private. (The read-only-from-outside rule is
// enforced by the `=` builtin and the linter; see decl.go / checkAssign.)
func TestPackageExportsVarConst(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "cfg.phl"),
		[]byte("(const Pi 3)\n(var Count 0)\n(const lower 9)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := modload.LoadPackage(pkgDir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}

	if v, ok := pkg.Exports["Pi"]; !ok || v.Kind != core.KindNum || v.Val.(float64) != 3 {
		t.Errorf("const Pi should export as num 3; got %v (ok=%v)", v, ok)
	}
	if v, ok := pkg.Exports["Count"]; !ok || v.Kind != core.KindNum || v.Val.(float64) != 0 {
		t.Errorf("var Count should export as num 0; got %v (ok=%v)", v, ok)
	}
	if _, ok := pkg.Exports["lower"]; ok {
		t.Errorf("lowercase 'lower' must not be exported")
	}
}
