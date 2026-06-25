package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression coverage for the struct-definition syntax change
// `(struct Name (f0 f1))` → `(struct Name f0 f1)`: the linter must still
// resolve struct fields and detect attached properties as struct members,
// in-file and across package imports.

// codes returns the diagnostic codes AnalyzeFile produces for src.
func codes(path string, src string) []string {
	var out []string
	for _, d := range AnalyzeFile(path, []byte(src)) {
		out = append(out, d.Code)
	}
	return out
}

func hasCode2(cs []string, code string) bool {
	for _, c := range cs {
		if c == code {
			return true
		}
	}
	return false
}

func TestStructSyntaxFieldResolution(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantClean bool   // no diagnostics at all
		wantCode  string // if non-clean, this code must appear
	}{
		{
			"bare fields resolve on member access",
			"(struct Point X Y)\n(var p Point.{ X 1 Y 2 })\n(var s p.X)",
			true, "",
		},
		{
			"unknown member is flagged",
			"(struct Point X Y)\n(var p Point.{ X 1 Y 2 })\n(var s p.Z)",
			false, "unknown-member",
		},
		{
			"self.field resolves inside a method",
			"(struct Point X Y)\n(method Point.Sum (self) (+ self.X self.Y))",
			true, "",
		},
		{
			"self.unknown is flagged inside a method",
			"(struct Point X Y)\n(method Point.Bad (self) self.Q)",
			false, "unknown-member",
		},
		{
			"multi-line struct definition resolves",
			"(struct Box\n    Width\n    Height)\n(var b Box.{ Width 3 Height 4 })\n(var w b.Width)",
			true, "",
		},
		{
			"fieldless struct is valid and its instance has no fields",
			"(struct Empty)\n(var e Empty.{ })\n(var x e.Nope)",
			false, "unknown-member",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := codes("t.pho", tc.src)
			if tc.wantClean {
				if len(cs) != 0 {
					t.Errorf("expected clean, got %v", cs)
				}
				return
			}
			if !hasCode2(cs, tc.wantCode) {
				t.Errorf("expected %q, got %v", tc.wantCode, cs)
			}
		})
	}
}

func TestStructSyntaxPropertyDetection(t *testing.T) {
	prelude := "(struct Temp celsius)\n" +
		"(property Temp.Fahrenheit\n" +
		"    get (method Temp (self) (+ self.celsius 32)))\n" +
		"(var t Temp.{ celsius 0 })\n"

	// The attached property is detected as a member of the struct.
	if cs := codes("t.pho", prelude+"(var f t.Fahrenheit)"); len(cs) != 0 {
		t.Errorf("property access should be clean, got %v", cs)
	}
	// A non-member is still flagged (proving detection is specific, not blanket).
	if cs := codes("t.pho", prelude+"(var f t.Nope)"); !hasCode2(cs, "unknown-member") {
		t.Errorf("unknown member should be flagged, got %v", cs)
	}
	// A free-standing property resolves as a bare name.
	free := "(var backing 0)\n" +
		"(property Tally get (fun () backing) set (fun (v) (= backing v)))\n" +
		"(var x Tally)"
	if cs := codes("t.pho", free); len(cs) != 0 {
		t.Errorf("free-standing property should be clean, got %v", cs)
	}
}

func TestStructSyntaxCrossPackage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "shapes")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	lib := "(struct Circle Radius Center)\n" +
		"(property Circle.Area get (method Circle (self) (* self.Radius self.Radius)))\n"
	if err := os.WriteFile(filepath.Join(pkg, "shapes.phl"), []byte(lib), 0644); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "main.pho")
	src := "(import 'shapes')\n" +
		"(var c shapes.Circle.{ Radius 2 Center 0 })\n" +
		"(var r c.Radius)\n" + // imported struct field
		"(var a c.Area)" // imported attached property
	if cs := codes(main, src); len(cs) != 0 {
		t.Errorf("imported field + property should resolve clean, got %v", cs)
	}

	bad := src + "\n(var x c.Nope)"
	if !hasCode2(codes(main, bad), "unknown-member") {
		t.Errorf("unknown imported member should be flagged")
	}
}
