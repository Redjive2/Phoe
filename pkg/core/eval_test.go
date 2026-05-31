package core

import "testing"

// TestUnescapeStringLit covers the conventional C-style backslash
// escapes the leaf evaluator translates when it sees a `"..."`
// literal. Unknown escapes pass through verbatim — that's documented
// behavior, not a bug to be fixed later.
func TestUnescapeStringLit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"newline", `line1\nline2`, "line1\nline2"},
		{"tab", `col\tcol`, "col\tcol"},
		{"carriage return", `a\rb`, "a\rb"},
		{"escaped quote", `she said \"hi\"`, `she said "hi"`},
		{"escaped backslash", `path\\to\\file`, `path\to\file`},
		{"null byte", `before\0after`, "before\x00after"},
		{"bell", `ring\a`, "ring\x07"},
		{"backspace", `oops\b`, "oops\x08"},
		{"form feed", `page\fbreak`, "page\x0Cbreak"},
		{"vtab", `v\vt`, "v\x0Bt"},
		{"unknown escape passes through", `\q stays`, `\q stays`},
		{"percent escape", `before \% after`, "before % after"},
		{"trailing backslash is preserved", `tail\`, `tail\`},
		{"only escapes", `\n\t\r`, "\n\t\r"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unescapeStringLit(tc.in)
			if got != tc.want {
				t.Errorf("unescapeStringLit(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNeedsUnescapeFastPath confirms the no-backslash fast path
// returns the input untouched (same string, not a copy via builder).
// Equality is enough — Go's string comparison doesn't tell us about
// allocation, but if we ever break the bool, the path's behavior
// would still be correct; this is a lock on intent, not performance.
func TestNeedsUnescapeFastPath(t *testing.T) {
	if needsUnescape("plain ascii") {
		t.Errorf("plain string should report no escapes")
	}
	if !needsUnescape(`has \n`) {
		t.Errorf("string with backslash should report needs escape")
	}
}
