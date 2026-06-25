package lint

import (
	"testing"

	"pho/pkg/annot"
)

// A `Struct.{ X T … }` type expression lints clean (the brace field-name keys
// are not references), and a universal method like `.Is?` resolves on a struct
// instance.
func TestStructRecordLintsClean(t *testing.T) {
	clean := []string{
		"(struct P x y)\n(let r = (P.{ x 1 y 2 }.is? Struct.{ x Number }))\n",
		"(struct P x)\n(let p = P.{ x 1 })\n(let r = (p.is? Struct.{ x Number y String }))\n",
		"(struct P x)\n(let p = P.{ x 1 })\n(let r = (p.is? Struct))\n",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.phl", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean, got %#v\n  src: %q", d, src)
		}
	}
}

// The gradual checker resolves structural record types in annotations: a value
// outside the record shape is flagged, a conforming struct is accepted.
func TestStructRecordChecking(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"conforming struct arg", "(struct P x)\n(fun f (Struct.{ x Number }) none)\n(fun f (p) none)\n(f P.{ x 1 })", false},
		{"non-struct literal arg", "(fun f (Struct.{ x Number }) none)\n(fun f (p) none)\n(f 5)", true},
		{"record var annotation clean", "(struct P x)\n(let var (Struct.{ x Number } p) = P.{ x 1 })", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := AnalyzeFile("t.pho", []byte(c.src))
			if got := hasDiag(d, "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantErr, c.src, d)
			}
		})
	}
}
