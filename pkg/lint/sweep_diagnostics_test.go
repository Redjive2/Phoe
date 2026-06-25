package lint

import (
	"os"
	"regexp"
	"testing"
)

// ----------------------------------------------------------------------
// Construction field checks (unknown-field / duplicate-field)
// ----------------------------------------------------------------------

// An unknown field name in a `T.{ field value … }` construction is an error
// (the runtime rejects an undeclared key — see builtins/decl.go).
func TestUnknownFieldInConstruction(t *testing.T) {
	diags := analyze(t, "(struct Point x #y)\n(let var p = Point.{ bogus 1 })\n")
	if !hasDiagWithName(diags, "unknown-field", "Bogus") {
		t.Fatalf("expected unknown-field for Point.{ Bogus … }, got %#v", diags)
	}
	// Valid fields draw nothing.
	clean := analyze(t, "(struct Point x #y)\n(let var p = Point.{ x 1 #y 2 })\n")
	if hasDiag(clean, "unknown-field") {
		t.Fatalf("Point.{ X … y … } must be clean, got %#v", clean)
	}
}

// Setting the same field twice in a construction is a (warning) — the last
// value silently wins.
func TestDuplicateFieldInConstruction(t *testing.T) {
	diags := analyze(t, "(struct Point x #y)\n(let var p = Point.{ x 1 x 2 })\n")
	if !hasDiag(diags, "duplicate-field") {
		t.Fatalf("expected duplicate-field for Point.{ X 1 X 2 }, got %#v", diags)
	}
}

// ----------------------------------------------------------------------
// Unreachable code after an unconditional exit
// ----------------------------------------------------------------------

func TestUnreachableAfterReturn(t *testing.T) {
	// A statement after a bare (return …) in a do-sequence can never run.
	diags := analyze(t, "(fun f () (identity do (return 1) (let var x = 2)))\n")
	if !hasDiag(diags, "unreachable-code") {
		t.Fatalf("expected unreachable-code after (return …), got %#v", diags)
	}
	// A return as the LAST statement is the exit value — not unreachable.
	last := analyze(t, "(fun f () (identity do (let var x = 1) (return x)))\n")
	if hasDiag(last, "unreachable-code") {
		t.Fatalf("return as last statement must not flag unreachable, got %#v", last)
	}
	// A return nested inside an `(if …)` arm is conditional — not unreachable.
	cond := analyze(t, "(fun f (c) (identity do (if c then (return 1)) (let var x = 2) x))\n")
	if hasDiag(cond, "unreachable-code") {
		t.Fatalf("conditional return must not flag unreachable, got %#v", cond)
	}
}

// ----------------------------------------------------------------------
// Highlight ↔ linter drift guards (the editor's @function.builtin list must
// not list a name the linter/runtime doesn't know, and the two checked-in
// query copies must stay byte-identical).
// ----------------------------------------------------------------------

const (
	canonicalHighlights = "../../tooling/tree-sitter-pho/queries/highlights.scm"
	zedHighlights       = "../../tooling/zed-pho/languages/pho/highlights.scm"
)

// TestHighlightBuiltinsSubsetOfLint keeps the editor's builtin-function
// highlight list from drifting ahead of the linter's authoritative
// builtinNames: a name highlighted as a builtin that the linter would flag
// unresolved is a contradiction the user sees as a miscolor.
func TestHighlightBuiltinsSubsetOfLint(t *testing.T) {
	src, err := os.ReadFile(canonicalHighlights)
	if err != nil {
		t.Skipf("canonical highlights.scm not found (%v)", err)
	}
	known := map[string]bool{}
	for _, n := range builtinNames {
		known[n] = true
	}
	for _, name := range builtinHighlightNames(t, string(src)) {
		if !known[name] {
			t.Errorf("highlights.scm tags %q as @function.builtin, but it is not in lint.builtinNames", name)
		}
	}
}

// TestHighlightCopiesInSync guards the two tracked highlights.scm copies (the
// canonical grammar-repo one and the Zed-loaded mirror) against silent drift.
func TestHighlightCopiesInSync(t *testing.T) {
	a, err1 := os.ReadFile(canonicalHighlights)
	b, err2 := os.ReadFile(zedHighlights)
	if err1 != nil || err2 != nil {
		t.Skipf("highlights.scm copies not both present (%v / %v)", err1, err2)
	}
	if string(a) != string(b) {
		t.Errorf("highlights.scm copies have drifted:\n  %s\n  %s\nkeep them byte-identical (canonical is the source of truth)", canonicalHighlights, zedHighlights)
	}
}

// (?s) = DOTALL so the names can span lines; capture the #any-of? body up to
// its closing `))`. A `)` inside an inline comment (e.g. "(gradual typing)") is
// harmless: it's a single paren, never the `))` the non-greedy match stops at.
var builtinAnyOf = regexp.MustCompile(`(?s)#any-of\?\s+@function\.builtin\s*(.*?)\)\)`)
var quoted = regexp.MustCompile(`"([^"]+)"`)

// builtinHighlightNames extracts the literal names from the
// `@function.builtin (#any-of? …)` block of a highlights query.
func builtinHighlightNames(t *testing.T, scm string) []string {
	m := builtinAnyOf.FindStringSubmatch(scm)
	if m == nil {
		t.Fatalf("could not locate the @function.builtin #any-of? block in highlights.scm")
	}
	var names []string
	for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
		names = append(names, q[1])
	}
	if len(names) == 0 {
		t.Fatalf("extracted zero builtin names from highlights.scm")
	}
	return names
}
