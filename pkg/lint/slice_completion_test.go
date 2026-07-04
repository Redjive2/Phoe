package lint

import "testing"

// A slice of a list is itself a list, so dot-completion on the slice result
// offers the collection members (size/keys/…) — and any imported List
// extensions — not just the universal members. Regression for the
// array-autocomplete improvement (inferShape's PDot slice case).
func TestDotCompletionOnSliceResult(t *testing.T) {
	src := "(let ys = [1 2 3])\n(let xs = ys.[0 : 2])\n(let q = xs.)\n"
	defs := CompletionsAt("main.pho", []byte(src), 3, 13)
	for _, want := range []string{"size", "keys", "empty?"} {
		if !containsName(defs, want) {
			t.Fatalf("a list slice should offer collection member %q, got %v", want, defNames(defs))
		}
	}
}
