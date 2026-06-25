package lint

import (
	"strings"
	"testing"
)

// slice / map are mangled internal heads behind the `[…]` and `{…}` literals,
// not callable builtins. A `[…]`/`{…}` literal lints clean; a bare
// `(slice …)`/`(map …)` is an unresolved identifier (so the linter never
// presents slice/map as builtins), and the diagnostic redirects the user to
// the literal syntax.
func TestSliceMapAreNotBuiltins(t *testing.T) {
	// Literals lint clean.
	d := AnalyzeFile("ok.pho", []byte("(const a [1 2 3])\n(const b {'k' 1})"))
	if hasDiag(d, "unresolved-identifier") {
		t.Errorf("[…]/{…} literals should lint clean, got %#v", d)
	}

	// Bare (slice …) is unresolved with a bracket-literal hint.
	d = AnalyzeFile("bad.pho", []byte("(const a (slice 1 2 3))"))
	if msg, ok := unresolvedMessage(d); !ok || !strings.Contains(msg, "'slice'") || !strings.Contains(msg, "[a b c]") {
		t.Errorf("(slice …) should be unresolved with a [a b c] hint, got %q (%#v)", msg, d)
	}

	// Bare (map …) is unresolved with a brace-literal hint.
	d = AnalyzeFile("bad2.pho", []byte("(const a (map 'k' 1))"))
	if msg, ok := unresolvedMessage(d); !ok || !strings.Contains(msg, "'map'") || !strings.Contains(msg, "{k v}") {
		t.Errorf("(map …) should be unresolved with a {k v} hint, got %q (%#v)", msg, d)
	}
}

func unresolvedMessage(diags []Diagnostic) (string, bool) {
	for _, d := range diags {
		if d.Code == "unresolved-identifier" {
			return d.Message, true
		}
	}
	return "", false
}
