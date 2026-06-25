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

	box := "(struct Box.{ n Number })\n(method Box.m (Self) String)\n(method Box.m (self) 's')\n(let b = Box.{ n 1 })\n"
	add := "(struct Box.{ n Number })\n(method Box.add (Self Number) Number)\n(method Box.add (self k) k)\n(let b = Box.{ n 1 })\n"
	g := "(fun g (Number) none)\n(fun g (n) none)\n"
	gs := "(fun gs (String) none)\n(fun gs (s) none)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"method result clashes with param", box + g + "(let a = (b.m))\n(g a)", true},
		{"method result matches param", box + gs + "(let a = (b.m))\n(gs a)", false},
		{"method-with-arg result clashes", add + gs + "(let a = (b.add 5))\n(gs a)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
