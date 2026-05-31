package syntax

import (
	"strings"
	"testing"

	"pho/pkg/core"
)

func parseAll(src string) []ParseError {
	tokens, lexErrs := LexPos(src)
	_, parseErrs := ParsePos(tokens)
	return append(lexErrs, parseErrs...)
}

func hasMessageContaining(errs []ParseError, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

// #1 — unterminated string is reported, not silently consumed.
func TestUnterminatedString(t *testing.T) {
	errs := parseAll(`(print "hello`)
	if !hasMessageContaining(errs, "unterminated string") {
		t.Fatalf("expected unterminated-string error, got %#v", errs)
	}
}

// #2 — a stray backtick gets a specific message instead of "unrecognized
// character".
func TestStrayBacktick(t *testing.T) {
	errs := parseAll("(print `)")
	if !hasMessageContaining(errs, "stray '`'") {
		t.Fatalf("expected stray-backtick error, got %#v", errs)
	}
	if hasMessageContaining(errs, "unrecognized character") {
		t.Fatalf("did not expect generic unrecognized-character error, got %#v", errs)
	}
}

// #4 — a sigil immediately followed by a closer is recovered cleanly: we
// emit a missing-expression error and don't swallow the closer.
func TestSigilHitsCloser(t *testing.T) {
	errs := parseAll("(' )")
	if !hasMessageContaining(errs, "missing expression after") {
		t.Fatalf("expected missing-expression error, got %#v", errs)
	}
	// And the surrounding `(` should still find its `)` (no
	// missing-closing-paren error).
	if hasMessageContaining(errs, "missing closing") {
		t.Fatalf("sigil should not have consumed the closer; got %#v", errs)
	}
}

// #5 — a stray `.` at primary position reports a specific message.
func TestStrayDot(t *testing.T) {
	errs := parseAll("(. x)")
	if !hasMessageContaining(errs, "unexpected '.'") {
		t.Fatalf("expected unexpected-dot error, got %#v", errs)
	}
}

// (name! a b) parses to a PMacroCall, not a PBranch with a "!" child.
func TestMacroCallDetection(t *testing.T) {
	tokens, _ := LexPos("(my! a b)")
	tree, errs := ParsePos(tokens)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %#v", errs)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 top-level node, got %d", len(tree))
	}
	mc, ok := tree[0].(*core.PMacroCall)
	if !ok {
		t.Fatalf("expected *core.PMacroCall, got %T", tree[0])
	}
	if head, ok := mc.Head.(*core.PLeaf); !ok || head.Value != "my" {
		t.Fatalf("expected head leaf 'my', got %#v", mc.Head)
	}
	if len(mc.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(mc.Args))
	}
	for i, want := range []string{"a", "b"} {
		leaf, ok := mc.Args[i].(*core.PLeaf)
		if !ok || leaf.Value != want {
			t.Fatalf("arg %d: expected %q leaf, got %#v", i, want, mc.Args[i])
		}
	}
}

// `(plain a b)` — no `!` — stays a PBranch.
func TestNonMacroStaysBranch(t *testing.T) {
	tokens, _ := LexPos("(plain a b)")
	tree, _ := ParsePos(tokens)
	if _, ok := tree[0].(*core.PBranch); !ok {
		t.Fatalf("expected *core.PBranch, got %T", tree[0])
	}
}

// Macro detection is paren-only — `[a! b]` stays a PBranch (array
// literal, not a macro call).
func TestMacroCallOnlyForParens(t *testing.T) {
	tokens, _ := LexPos("[a! b]")
	tree, _ := ParsePos(tokens)
	if _, ok := tree[0].(*core.PBranch); !ok {
		t.Fatalf("expected *core.PBranch for [...] form, got %T", tree[0])
	}
}

// Sanity: well-formed source produces no errors.
// Backslash escapes inside a string must not terminate the literal:
// `"a\"b"` is one string, not `"a\"` followed by garbage. The lexer's
// job is just "don't end here"; the actual translation happens in
// core/eval.go's leaf evaluator.
func TestBackslashEscapeDoesNotTerminate(t *testing.T) {
	src := `(print "a\"b")`
	errs := parseAll(src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors with \\\" escape, got %#v", errs)
	}
	tokens, _ := LexPos(src)
	// Find the string token and confirm its source includes the whole
	// "a\"b" body.
	var found bool
	for _, tk := range tokens {
		if strings.HasPrefix(tk.Value, `"`) && strings.HasSuffix(tk.Value, `"`) {
			if tk.Value != `"a\"b"` {
				t.Errorf("expected token \"a\\\"b\", got %q", tk.Value)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("no string token found in tokens %#v", tokens)
	}
}

func TestNoErrorsOnValidInput(t *testing.T) {
	src := `(var 'x 5)
(print x.foo)
(set 'y [1 2 3])
(map &(+ x 1) y)
(var 's "hello")
(var 'c ` + "`X`" + `)
`
	errs := parseAll(src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors on valid source, got %#v", errs)
	}
}
