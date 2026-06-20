package builtins

import (
	"testing"

	"pho/pkg/core"
)

// A macro that constructs a declaration names the binding with a bare
// identifier in the data: [Do ["var" "x" 5] "x"] resumes to (Do (var x 5) x),
// declaring x=5 and reading it back. Post-cutover names are bare, so the
// data carries "x" (→ the identifier x), not the apostrophe form. The head
// is the mangled core.Do sequencing primitive — `do` notation is a
// parse-time rewrite, so runtime-constructed data names the primitive
// directly.
func TestResumeBareNameDeclaration(t *testing.T) {
	got := evalProgram(t, `(resume (slice "`+core.Do+`" (slice "var" "x" 5) "x"))`)
	if got.Kind != core.KindNum || got.Val != float64(5) {
		t.Fatalf(`resume of [Do ["var" "x" 5] "x"] = %#v, want num 5`, got)
	}
}

// The apostrophe form also works as a value: a quoted symbol resumes to the
// string of its name.
func TestResumeApostropheAsValue(t *testing.T) {
	got := evalProgram(t, `(resume "'hello")`)
	if got.Kind != core.KindStr || got.Val != "hello" {
		t.Fatalf(`(resume "'hello") = %#v, want str "hello"`, got)
	}
}
