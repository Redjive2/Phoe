package lint

import (
	"testing"

	"pho/pkg/annot"
)

// A method call `(x.M args)` carries M's declared RESULT type, so it propagates
// into a const / argument like any other call. The method's signature is the
// inline form `(method R.M (Self P…) R)` — param 0 is the receiver type.
func TestMethodCallResultType(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	box := "(struct Box.{ N Number })\n(method Box.M (Self) String)\n(method Box.M (self) 's')\n(const b Box.{ N 1 })\n"
	add := "(struct Box.{ N Number })\n(method Box.Add (Self Number) Number)\n(method Box.Add (self k) k)\n(const b Box.{ N 1 })\n"
	g := "(fun g (Number) Nil)\n(fun g (n) Nil)\n"
	gs := "(fun gs (String) Nil)\n(fun gs (s) Nil)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"method result clashes with param", box + g + "(const a (b.M))\n(g a)", true},
		{"method result matches param", box + gs + "(const a (b.M))\n(gs a)", false},
		{"method-with-arg result clashes", add + gs + "(const a (b.Add 5))\n(gs a)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
