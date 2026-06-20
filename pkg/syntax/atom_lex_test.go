package syntax

import (
	"testing"
)

// tokenValues lexes src and returns the token texts (dropping spans), or
// fails if the lexer reported errors.
func tokenValues(t *testing.T, src string) []string {
	t.Helper()
	toks, errs := LexPos(src)
	if len(errs) != 0 {
		t.Fatalf("LexPos(%q) reported errors: %v", src, errs)
	}
	out := make([]string, len(toks))
	for i, tk := range toks {
		out[i] = tk.Value
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A ':' glued to an identifier/digit is one atom token; a free-standing ':'
// (followed by a space or a closer) stays the lone slice/range separator. So
// spaced slices keep working and atoms read naturally elsewhere.
func TestAtomTokenization(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{":foo", []string{":foo"}},
		{":01213", []string{":01213"}},
		{":done?", []string{":done?"}},
		{"(f :foo)", []string{"(", "f", ":foo", ")"}},
		// Spaced slice: the colon is its own token (the separator).
		{"[1 : 2]", []string{"[", "1", ":", "2", "]"}},
		{"xs.[1 :]", []string{"xs", ".", "[", "1", ":", "]"}},
		{"xs.[:]", []string{"xs", ".", "[", ":", "]"}},
		// Glued atom inside an array is an element, not a separator.
		{"[a :foo]", []string{"[", "a", ":foo", "]"}},
		// Documented consequence: a no-space slice reads as [1, :2].
		{"xs.[1:2]", []string{"xs", ".", "[", "1", ":2", "]"}},
	}
	for _, tc := range cases {
		got := tokenValues(t, tc.src)
		if !eqStrings(got, tc.want) {
			t.Errorf("LexPos(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}

// A single trailing '?' is part of an identifier (the predicate convention).
func TestTrailingQuestionToken(t *testing.T) {
	if got := tokenValues(t, "atom?"); !eqStrings(got, []string{"atom?"}) {
		t.Errorf("LexPos(%q) = %v, want [atom?]", "atom?", got)
	}
	if got := tokenValues(t, "(empty? xs)"); !eqStrings(got, []string{"(", "empty?", "xs", ")"}) {
		t.Errorf("LexPos(%q) = %v", "(empty? xs)", got)
	}
}

// A bare '?' (not trailing an identifier or atom) is still unrecognized.
func TestBareQuestionIsError(t *testing.T) {
	_, errs := LexPos("(? x)")
	if !hasMessageContaining(errs, "unrecognized character") {
		t.Fatalf("expected unrecognized-character error for bare '?', got %v", errs)
	}
}
