package lint

import "testing"

// The retired `(T { … })` struct-construction form is flagged with a
// `retired-construction` diagnostic that points at the `T.{ … }` replacement.
func TestRetiredConstructionFlagged(t *testing.T) {
	diags := analyze(t, `(struct Point x #y)
(let var p = (Point { 'X' -> 1 'y' -> 2 }))
`)
	if !hasDiag(diags, "retired-construction") {
		t.Fatalf("expected retired-construction for (Point { … }), got %#v", diags)
	}
}

// The bare-key `T.{ field value }` construction draws no construction
// diagnostic — it is the supported form.
func TestNewConstructionClean(t *testing.T) {
	diags := analyze(t, `(struct Point x #y)
(let var p = Point.{ x 1 #y 2 })
`)
	if hasDiag(diags, "retired-construction") {
		t.Fatalf("Point.{ … } must not be flagged, got %#v", diags)
	}
}

// A struct constructor applied to ordinary (non-brace) arguments — the shape
// the construction sugar lowers to — is not mistaken for the retired form.
func TestConstructorWithBareArgsNotFlagged(t *testing.T) {
	diags := analyze(t, `(struct Point x #y)
(let var p = (Point 'X' 1 'y' 2))
`)
	if hasDiag(diags, "retired-construction") {
		t.Fatalf("(Point \"X\" 1 …) must not be flagged, got %#v", diags)
	}
}
