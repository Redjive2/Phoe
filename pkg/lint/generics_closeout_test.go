package lint

import (
	"testing"

	"pho/pkg/annot"
)

// TestGenericsUserExampleLintsClean pins the user's original motivating example
// (the syntax that kicked off the whole generics effort): a bounded and an
// unbounded template, a `{}` generic struct, and a generic method sig + impl.
// The entire thing lints cleanly.
func TestGenericsUserExampleLintsClean(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	src := "(struct Some-Type #x)\n" +
		"(template U (Some-Type B))\n" +
		"(struct Container { U u B #b })\n" +
		"(template I O)\n" +
		"(method I.bind (Self (fun (I) O)) O)\n" +
		"(let I.bind (self fn) = (fn self))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
		t.Errorf("the user's generics example should lint clean; got %#v", d)
	}
}

// TestGenericsEndToEnd exercises the whole parametric pipeline in one realistic
// program: a trait bound, a generic function whose result is a type parameter,
// instantiation of the result at a call, and a downstream mismatch — all
// checked precisely, and its sound converse staying clean.
func TestGenericsEndToEnd(t *testing.T) {
	prog := "(trait Shape (method self.area (self) Number))\n" +
		"(template (Shape S))\n" +
		"(fun sized (S) Number)\n" + // sig: takes a Shape-bounded value, returns Number
		"(let sized (s) = (s.area))\n" + // impl: uses the bound's method
		"(template I)\n" +
		"(fun pick (I I) I)\n(let pick (a b) = a)\n" // generic: result is the args' join
	gStr := "(fun g (String) None)\n(let g (s) = None)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = None)\n"

	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// `(pick 1 2)` : {1,2} ⊆ Number — feeding a String-expecting g fires.
		{"unified result vs String", prog + gStr + "(const a (pick 1 2))\n(g a)", true},
		{"unified result vs Number", prog + gNum + "(const a (pick 1 2))\n(g a)", false},
		// A non-Shape value violates the trait bound at the call.
		{"trait bound violated", prog + "(let use () = (sized 'x'))", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
