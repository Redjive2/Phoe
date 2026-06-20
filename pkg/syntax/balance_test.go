package syntax

import (
	"sort"
	"strings"
	"testing"
)

// applyEdits applies edits to src (used to verify what the editor
// would end up with). Edits are applied back-to-front so earlier
// offsets stay valid.
func applyEdits(t *testing.T, src string, edits []Edit) string {
	t.Helper()
	type offEdit struct {
		start, end int
		text       string
	}
	toOffset := func(line, col int) int {
		cur := 1
		for i := 0; i < len(src); i++ {
			if cur == line {
				return i + col - 1
			}
			if src[i] == '\n' {
				cur++
			}
		}
		if cur == line {
			return len(src) + col - 1
		}
		return len(src)
	}
	var offs []offEdit
	for _, e := range edits {
		offs = append(offs, offEdit{
			start: toOffset(e.Span.StartLine, e.Span.StartCol),
			end:   toOffset(e.Span.EndLine, e.Span.EndCol),
			text:  e.NewText,
		})
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i].start > offs[j].start })
	for _, o := range offs {
		if o.end > len(src) {
			o.end = len(src)
		}
		src = src[:o.start] + o.text + src[o.end:]
	}
	return src
}

// The retroactive close: two forms abandoned above a new top-level
// form. Cursor at the end of the new form (as if `)` was just typed).
// Both closers are inserted at the predicted site, and the result is
// balanced.
func TestBalanceRetroactiveClose(t *testing.T) {
	src := `(fun 'f '(x)
  '(do
    (print x)

(var 'y 5)
`
	edits := BalanceClosers(src, 5, 11)
	if len(edits) != 1 {
		t.Fatalf("expected 1 merged insertion, got %#v", edits)
	}
	e := edits[0]
	if e.Span.StartLine != 3 || e.Span.StartCol != 14 || e.NewText != "))" {
		t.Fatalf("expected insertion of \"))\" at 3:14, got %q at %d:%d",
			e.NewText, e.Span.StartLine, e.Span.StartCol)
	}
	fixed := applyEdits(t, src, edits)
	if errs := parseAll(fixed); len(errs) != 0 {
		t.Fatalf("expected balanced output, got %#v\nsrc:\n%s", errs, fixed)
	}
}

// Typing inside an unfinished form must NOT slam it shut: cursor
// indented deeper than the open forms' line indents → no edits.
func TestBalanceStaysOpenWhileTypingInside(t *testing.T) {
	src := `(fun 'f '(x)
  '(do
    (print x)
    `
	// Cursor on line 4, col 5 — as after Enter with auto-indent.
	if edits := BalanceClosers(src, 4, 5); edits != nil {
		t.Fatalf("expected no edits while inside the form, got %#v", edits)
	}
}

// Dedenting the cursor to column 1 signals every open form above is
// finished — all close at the predicted site.
func TestBalanceDedentClosesAll(t *testing.T) {
	src := `(fun 'f '(x)
  '(do
    (print x)
`
	edits := BalanceClosers(src, 4, 1)
	if len(edits) != 1 || edits[0].NewText != "))" {
		t.Fatalf("expected merged \"))\" insertion, got %#v", edits)
	}
	fixed := applyEdits(t, src, edits)
	if errs := parseAll(fixed); len(errs) != 0 {
		t.Fatalf("expected balanced output, got %#v\nsrc:\n%s", errs, fixed)
	}
}

// Partial dedent: cursor at the inner form's indent closes the inner
// form but keeps the outer one open.
func TestBalancePartialDedent(t *testing.T) {
	src := `(fun 'f '(x)
  '(do
    (print x)
  `
	// Cursor line 4 col 3 — at the '(do line indent (3), inside fun (1).
	edits := BalanceClosers(src, 4, 3)
	if len(edits) != 1 || edits[0].NewText != ")" {
		t.Fatalf("expected single \")\" insertion for the do form, got %#v", edits)
	}
}

// Balanced code in many layouts: never any edits.
func TestBalanceNoOpOnBalancedCode(t *testing.T) {
	cases := []string{
		"(print x)",
		"(fun 'f '(x)\n  '(do\n    (print x)))\n",
		"(print\n\"weird indent\")",
		"(a\n  (b x\n  y\n)\n)",
	}
	for _, src := range cases {
		for line := 1; line <= strings.Count(src, "\n")+1; line++ {
			if edits := BalanceClosers(src, line, 1); edits != nil {
				t.Errorf("expected no edits for balanced %q at line %d, got %#v", src, line, edits)
			}
		}
	}
}

// A stray closer near the cursor is deleted.
func TestBalanceDeletesStrayCloser(t *testing.T) {
	src := "(print x))\n"
	edits := BalanceClosers(src, 1, 11)
	if len(edits) != 1 || edits[0].NewText != "" {
		t.Fatalf("expected one deletion, got %#v", edits)
	}
	fixed := applyEdits(t, src, edits)
	if errs := parseAll(fixed); len(errs) != 0 {
		t.Fatalf("expected balanced output after deletion, got %#v (src %q)", errs, fixed)
	}
}

// Broken code in a DIFFERENT region of the file is left alone.
func TestBalanceScopedToCursorRegion(t *testing.T) {
	src := `(broken 'above
  x

(fine 1)

(another 2)

(also 'broken
  y
`
	// Cursor inside (another 2) — two regions away from both broken
	// forms. Hmm: (broken 'above x is region 0, (fine 1) region 1,
	// (another 2) region 2. The preceding-form scope only reaches
	// region 1, so neither broken form may be touched.
	if edits := BalanceClosers(src, 6, 9); edits != nil {
		t.Fatalf("expected no edits away from broken regions, got %#v", edits)
	}
}

// An unterminated string near the cursor disables balancing entirely.
func TestBalanceBailsOnUnterminatedString(t *testing.T) {
	src := "(print \"oops\n"
	if edits := BalanceClosers(src, 2, 1); edits != nil {
		t.Fatalf("expected nil edits with unterminated string, got %#v", edits)
	}
}

// Idempotency: applying the returned edits and re-running yields nil,
// for every scenario that produced edits.
func TestBalanceIdempotent(t *testing.T) {
	cases := []struct {
		src       string
		line, col int
	}{
		{"(fun 'f '(x)\n  '(do\n    (print x)\n\n(var 'y 5)\n", 5, 11},
		{"(fun 'f '(x)\n  '(do\n    (print x)\n", 4, 1},
		{"(print x))\n", 1, 11},
		{"(a\n  (b x\n(c y)\n", 3, 6},
	}
	for _, c := range cases {
		edits := BalanceClosers(c.src, c.line, c.col)
		if edits == nil {
			continue
		}
		fixed := applyEdits(t, c.src, edits)
		for line := 1; line <= strings.Count(fixed, "\n")+1; line++ {
			if again := BalanceClosers(fixed, line, 1); again != nil {
				t.Errorf("not idempotent for %q: second pass at line %d gave %#v\nfixed:\n%s",
					c.src, line, again, fixed)
			}
		}
	}
}
