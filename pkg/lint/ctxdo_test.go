package lint

import "testing"

// Context-aware do: a `do` arm inside if/unless is bounded by the next
// elif/else, so after NormalizeDo the branch keywords still sit at the form's
// top level and the walker skips them via parseIfForm. Before the boundary
// was honored, the first `do` swallowed the elif/else markers into its body,
// and the walker reference-checked them as identifiers — surfacing spurious
// 'elif'/'then'/'else' is-not-defined errors.
func TestContextAwareDoLintsClean(t *testing.T) {
	src := []byte(`(fun classify (Number) String)
(let classify (n) = do
    (if (< n 0) then do
        (let r = 'neg')
        r
     elif (== n 0) then do
        (let r = 'zero')
        r
     else do
        (let r = 'pos')
        r))
(classify -3)`)
	for _, d := range AnalyzeFile("test.pho", src) {
		t.Errorf("if+do should lint clean, got %s: %s", d.Code, d.Message)
	}
}

// The unless form is context-aware too: its then-arm `do` stops at `else`.
func TestContextAwareDoUnlessLintsClean(t *testing.T) {
	src := []byte(`(fun pick (Boolean) Number)
(let pick (b) = do
    (unless b then do
        (let r = 1)
        r
     else do
        (let r = 2)
        r))
(pick false)`)
	for _, d := range AnalyzeFile("test.pho", src) {
		t.Errorf("unless+do should lint clean, got %s: %s", d.Code, d.Message)
	}
}
