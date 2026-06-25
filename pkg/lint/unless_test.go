package lint

import "testing"

// `unless` parses like `if` minus `elif`: the linter walks its condition and
// arms, hoists arm declarations, and flags an `elif` as a bad-form-shape.
func TestUnlessLint(t *testing.T) {
	// A valid unless (with and without else) lints clean.
	clean := "(fun f (a) do\n  (unless (> a 0) then 'x' else 'y'))\n"
	if d := AnalyzeFile("t.phl", []byte(clean)); hasDiag(d, "bad-form-shape") || hasDiag(d, "unresolved-identifier") {
		t.Errorf("valid unless should lint clean, got %#v", d)
	}

	// A var declared in an arm hoists into the enclosing scope, like if.
	hoist := "(fun g (a) do\n  (unless (> a 0) then (let var x = 5) else (let var x = 9))\n  (+ x 1))\n"
	if d := AnalyzeFile("t.phl", []byte(hoist)); hasDiag(d, "unresolved-identifier") {
		t.Errorf("unless arm var should hoist, got %#v", d)
	}

	// `elif` is not supported.
	bad := "(unless true then 1 elif false then 2)"
	if !hasDiagWithName(AnalyzeFile("t.pho", []byte(bad)), "bad-form-shape", "elif") {
		t.Errorf("unless + elif should be a bad-form-shape mentioning elif, got %#v", AnalyzeFile("t.pho", []byte(bad)))
	}
}
