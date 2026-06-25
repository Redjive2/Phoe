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
	src := "(struct Point.{ X Number Y Number })\n" +
		"(const p Point.{ X 1 Y 2 })\n" +
		"(const a p.X)\n" + // a typed field resolves
		"(const c p.Nope)\n" // an unknown member still fires
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
	src := "(struct Plain A B)\n(const q Plain.{ A 5 B 6 })\n(const x q.A)\n(const y q.Z)\n"
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

	const box = "(struct Box.{ N Number })\n(const b Box.{ N 1 })\n"
	stageE(t, []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"typed field matches param", box +
			"(fun needN (Number) Nil)\n(fun needN (n) Nil)\n(needN b.N)", false},
		{"typed field clashes with param", box +
			"(fun needS (String) Nil)\n(fun needS (s) Nil)\n(needS b.N)", true},
		{"typed field as a typed var init", box +
			"(const (String s) b.N)", true},
	})
}
