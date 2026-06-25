package annot

import (
	"testing"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// stashAnnotations builds the span-keyed table on file.Annotations for an
// annotated file (the modload.AnnotationStasher path).
func TestStashAnnotations(t *testing.T) {
	SetDefault(New(nil))
	defer SetDefault(New(nil))

	tokens, _ := syntax.LexPos("--@ (~sig Num)\n(fun add (x) (+ x 1))")
	tree, _ := syntax.ParsePos(tokens)
	file := &core.File{}
	stashAnnotations(tree, file)

	tab, ok := file.Annotations.(FileAnnotations)
	if !ok {
		t.Fatalf("file.Annotations is not a FileAnnotations: %#v", file.Annotations)
	}
	if len(tab) != 1 {
		t.Fatalf("expected one annotated form in the table, got %d", len(tab))
	}
}

// An un-annotated file leaves file.Annotations nil — no allocation, no work.
func TestStashNoAnnotations(t *testing.T) {
	tokens, _ := syntax.LexPos("(fun add (x) (+ x 1))")
	tree, _ := syntax.ParsePos(tokens)
	file := &core.File{}
	stashAnnotations(tree, file)
	if file.Annotations != nil {
		t.Fatalf("un-annotated file should have nil Annotations, got %#v", file.Annotations)
	}
}
