package lint

import (
	"strings"
	"testing"

	"pho/pkg/annot"
	"pho/pkg/core"
)

// Hovering an annotated declaration shows its evaluated annotation metadata.
func TestHoverShowsAnnotations(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	src := "--@ (sig! Num Num -> Num)\n(fun add (x y) (+ x y))"
	md, _, ok := HoverAt("test.pho", []byte(src), 2, 7) // hover on 'add
	if !ok {
		t.Fatal("expected a hover for 'add")
	}
	if !strings.Contains(md, "annotations") || !strings.Contains(md, "sig") {
		t.Fatalf("hover should include the sig annotation metadata, got:\n%s", md)
	}
}

// An annotation whose macro is undefined surfaces the macro's error as a
// lint diagnostic — annotations are evaluated during analysis.
func TestAnnotationUndefinedMacroDiagnoses(t *testing.T) {
	annot.SetDefault(annot.New(nil)) // no macro library loaded
	defer annot.SetDefault(annot.New(nil))

	d := AnalyzeFile("test.pho", []byte("--@ (sig! Num)\n(const y 1)"))
	if !hasDiag(d, "unresolved") {
		t.Fatalf("expected an 'unresolved' diagnostic for the undefined annotation macro, got %#v", d)
	}
}

// An annotation whose macro is defined and clean raises no diagnostic, and
// the onAnnotation hook receives the evaluated results.
func TestAnnotationCleanMacro(t *testing.T) {
	annot.SetDefault(annot.New(map[string]core.StackEntry{
		"note": {Val: core.TvFun(func(ctx core.Context, argv []core.Node) core.Value {
			return core.TvNil
		}), IsConstant: true},
	}))
	defer annot.SetDefault(annot.New(nil))

	d := AnalyzeFile("test.pho", []byte("--@ (note! anything)\n(const y 1)"))
	for _, dg := range d {
		if dg.Code == "unresolved" {
			t.Fatalf("a clean annotation must not diagnose, got %#v", dg)
		}
	}
}

// A file with no annotations triggers no annotation evaluation (and no
// spurious diagnostics) even with an empty evaluator.
func TestNoAnnotationsNoEval(t *testing.T) {
	annot.SetDefault(annot.New(nil))
	defer annot.SetDefault(annot.New(nil))

	d := AnalyzeFile("test.pho", []byte("(const x 1)\n(const y (+ x 1))"))
	for _, dg := range d {
		if dg.Code == "unresolved" {
			t.Fatalf("plain file should not produce annotation diagnostics, got %#v", dg)
		}
	}
}
