package lint

import (
	"testing"

	"pho/pkg/annot"
)

// The typed-field struct form `(struct Name.{ F T … })` declares fields with
// types. It must parse cleanly, resolve member access, and feed each field's
// declared type into the gradual checker — while the bare untyped form
// `(struct Name f …)` keeps working.

func TestTypedStructFieldsResolve(t *testing.T) {
	src := "(struct Point.{ x Number y Number })\n" +
		"(let p = Point.{ x 1 y 2 })\n" +
		"(let a = p.x)\n" + // a typed field resolves
		"(let c = p.nope)\n" // an unknown member still fires
	d := AnalyzeFile("t.pho", []byte(src))
	if hasDiag(d, "bad-form-shape") {
		t.Errorf("typed-field struct should not be a bad form; got %#v", d)
	}
	if hasDiagWithName(d, "unknown-member", "X") {
		t.Errorf("typed field X should resolve; got %#v", d)
	}
	if !hasDiagWithName(d, "unknown-member", "Nope") {
		t.Errorf("an unknown member should still fire; got %#v", d)
	}
}

func TestBareStructStillWorks(t *testing.T) {
	src := "(struct Plain a b)\n(let q = Plain.{ a 5 b 6 })\n(let x = q.a)\n(let y = q.z)\n"
	d := AnalyzeFile("t.pho", []byte(src))
	if hasDiag(d, "bad-form-shape") {
		t.Errorf("bare struct form must stay valid; got %#v", d)
	}
	if hasDiagWithName(d, "unknown-member", "A") {
		t.Errorf("bare field A should resolve; got %#v", d)
	}
	if !hasDiagWithName(d, "unknown-member", "Z") {
		t.Errorf("unknown member Z should fire; got %#v", d)
	}
}

func TestTypedStructFieldTypeChecking(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	const box = "(struct Box.{ n Number })\n(let b = Box.{ n 1 })\n"
	stageE(t, []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"typed field matches param", box +
			"(fun need_n (Number) none)\n(fun need_n (n) none)\n(need_n b.n)", false},
		{"typed field clashes with param", box +
			"(fun need_s (String) none)\n(fun need_s (s) none)\n(need_s b.n)", true},
		{"typed field as a typed var init", box +
			"(let (String s) = b.n)", true},
	})
}
