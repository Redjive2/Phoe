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
			"(struct Point x y)\n(let var p = Point.{ x 1 y 2 })\n(let var s = p.x)",
			true, "",
		},
		{
			"unknown member is flagged",
			"(struct Point x y)\n(let var p = Point.{ x 1 y 2 })\n(let var s = p.z)",
			false, "unknown-member",
		},
		{
			"self.field resolves inside a method",
			"(struct Point x y)\n(method Point.sum (self) (+ self.x self.y))",
			true, "",
		},
		{
			"self.unknown is flagged inside a method",
			"(struct Point x y)\n(method Point.bad (self) self.q)",
			false, "unknown-member",
		},
		{
			"multi-line struct definition resolves",
			"(struct Box\n    width\n    height)\n(let var b = Box.{ width 3 height 4 })\n(let var w = b.width)",
			true, "",
		},
		{
			"fieldless struct is valid and its instance has no fields",
			"(struct Empty)\n(let var e = Empty.{ })\n(let var x = e.nope)",
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
	prelude := "(struct Temp #celsius)\n" +
		"(property Temp.fahrenheit\n" +
		"    get (method Temp (self) (+ self.#celsius 32)))\n" +
		"(let var t = Temp.{ #celsius 0 })\n"

	// The attached property is detected as a member of the struct.
	if cs := codes("t.pho", prelude+"(let var f = t.fahrenheit)"); len(cs) != 0 {
		t.Errorf("property access should be clean, got %v", cs)
	}
	// A non-member is still flagged (proving detection is specific, not blanket).
	if cs := codes("t.pho", prelude+"(let var f = t.nope)"); !hasCode2(cs, "unknown-member") {
		t.Errorf("unknown member should be flagged, got %v", cs)
	}
	// A free-standing property resolves as a bare name.
	free := "(let var backing = 0)\n" +
		"(property tally get (fun () backing) set (fun (v) (= backing v)))\n" +
		"(let var x = tally)"
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
	lib := "(struct Circle radius center)\n" +
		"(property Circle.area get (method Circle (self) (* self.radius self.radius)))\n"
	if err := os.WriteFile(filepath.Join(pkg, "shapes.phl"), []byte(lib), 0644); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "main.pho")
	src := "(import 'shapes')\n" +
		"(let var c = shapes.Circle.{ radius 2 center 0 })\n" +
		"(let var r = c.radius)\n" + // imported struct field
		"(let var a = c.area)" // imported attached property
	if cs := codes(main, src); len(cs) != 0 {
		t.Errorf("imported field + property should resolve clean, got %v", cs)
	}

	bad := src + "\n(let var x = c.nope)"
	if !hasCode2(codes(main, bad), "unknown-member") {
		t.Errorf("unknown imported member should be flagged")
	}
}
