package lint

import (
	"os"
	"path/filepath"
	"testing"

	"pho/pkg/annot"
)

// An IMPORTED struct's field types are harvested (PackageStructs resolves them
// against builtins), so a fully-primitively-typed imported struct gets a precise
// record and its values check against a declared record/struct/primitive type
// across the import boundary — not just locally.
func TestImportedStructRecord(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	geo := "(struct Point.{ x Number y Number })\n"
	path, src := writeApp(t, geo, "(fun f (Struct.{ z Number }) none)\n(fun f (x) none)\n(let p = geo.Point.{ X 1 Y 2 })\n(f p)")
	if !hasDiag(AnalyzeFile(path, src), "type-mismatch") {
		t.Errorf("an imported struct missing a required field should fire")
	}
	path, src = writeApp(t, geo, "(fun f (Struct.{ x Number }) none)\n(fun f (x) none)\n(let p = geo.Point.{ X 1 Y 2 })\n(f p)")
	if hasDiag(AnalyzeFile(path, src), "type-mismatch") {
		t.Errorf("an imported struct satisfying the record should be clean")
	}
}

// writeApp lays out geo + app packages and returns the app path and its source.
func writeApp(t *testing.T, geoSrc, appBody string) (string, []byte) {
	t.Helper()
	root := writeTree(t, map[string]string{
		"script/std/geo/geo.phl": geoSrc,
		"script/app.pho":         "(import 'std/geo')\n" + appBody,
	})
	path := filepath.Join(root, "script/app.pho")
	src, _ := os.ReadFile(path)
	return path, src
}
