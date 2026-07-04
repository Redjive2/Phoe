package lint

import "testing"

// A lambda form lints clean: its params bind, body references resolve, and a
// leading Capitalized leaf in a typed param / receiver / return position is a
// TYPE, not a mis-cased param (no capitalized-param).
func TestLambdaLintsClean(t *testing.T) {
	clean := []string{
		"(let f = (lambda a b -> (+ a b)))",
		"(let f = (lambda (Number n) Number -> (+ n 1)))",     // typed param + return type
		"(let f = (lambda self x -> (+ self x)))",             // implicit receiver
		"(let f = (lambda Number self x -> (+ self x)))",      // explicit receiver type
		"(let f = (lambda -> 7))",                             // no params
		"(let g = (lambda a -> (+ a 1)))\n(let h = (g 5))",    // applied
		"(let base = 10)\n(let f = (lambda n -> (+ n base)))", // closes over scope
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean for %q, got %v", src, d)
		}
	}
}

// A lambda body is reference-checked: an unresolved name inside it is flagged,
// and a malformed header draws bad-lambda.
func TestLambdaDiagnostics(t *testing.T) {
	d := AnalyzeFile("t.pho", []byte("(let f = (lambda a -> (+ a nope)))"))
	if !hasDiagWithName(d, "unresolved-identifier", "nope") {
		t.Fatalf("a lambda body's unresolved name must be flagged, got %v", d)
	}
	if d := AnalyzeFile("t.pho", []byte("(lambda a b)")); !hasDiag(d, "bad-lambda") {
		t.Fatalf("a lambda without '->' must draw bad-lambda, got %v", d)
	}
}

// A lambda that performs an environmental effect must be written `lambda!`;
// otherwise it draws missing-bang on the lambda head. A `lambda!` is clean.
func TestLambdaEffects(t *testing.T) {
	enableEffectCheck(t)
	sink := "(fun sink! (Number) None)\n(let sink! (n) = none)\n"
	bad := analyze(t, sink+"(let f = (lambda a -> (sink! a)))")
	if !hasDiag(bad, "missing-bang") {
		t.Fatalf("a lambda calling a `!`-function must draw missing-bang, got %v", bad)
	}
	good := analyze(t, sink+"(let f = (lambda! a -> (sink! a)))")
	if hasDiag(good, "missing-bang") {
		t.Fatalf("lambda! correctly declares its effect — clean, got %v", good)
	}
}
