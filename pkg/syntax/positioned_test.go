package syntax

import (
	"strings"
	"testing"

	"pho/pkg/ast"
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

// An identifier may carry both a predicate '?' and an effect '!', always in the
// order `name?!`; `name!?` must not lex as a single identifier.
func TestQuestionBangSuffix(t *testing.T) {
	vals := func(src string) []string {
		toks, _ := LexPos(src)
		var out []string
		for _, tk := range toks {
			out = append(out, tk.Value)
		}
		return out
	}
	for _, name := range []string{"ok?", "flush!", "grab?!"} {
		got := vals("(" + name + ")")
		if len(got) != 3 || got[1] != name {
			t.Fatalf("%q should lex as one identifier, got %v", name, got)
		}
	}
	// `bad!?` splits — the '?' cannot follow the '!'.
	got := vals("(bad!? x)")
	if len(got) < 2 || got[1] != "bad!" {
		t.Fatalf("bad!? should split into 'bad!' + stray '?', got %v", got)
	}
	for _, v := range got {
		if v == "bad!?" {
			t.Fatalf("bad!? must not be one identifier, got %v", got)
		}
	}
}

// #1 — unterminated string is reported, not silently consumed.
func TestUnterminatedString(t *testing.T) {
	errs := parseAll(`(print 'hello`)
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
	errs := parseAll("(& )")
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

// (~name a b) parses to a PMacroCall, not a PBranch with a "~" child.
func TestMacroCallDetection(t *testing.T) {
	tokens, _ := LexPos("(~my a b)")
	tree, errs := ParsePos(tokens)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %#v", errs)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 top-level node, got %d", len(tree))
	}
	mc, ok := tree[0].(*ast.PMacroCall)
	if !ok {
		t.Fatalf("expected *ast.PMacroCall, got %T", tree[0])
	}
	if head, ok := mc.Head.(*ast.PLeaf); !ok || head.Value != "my" {
		t.Fatalf("expected head leaf 'my', got %#v", mc.Head)
	}
	if len(mc.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(mc.Args))
	}
	for i, want := range []string{"a", "b"} {
		leaf, ok := mc.Args[i].(*ast.PLeaf)
		if !ok || leaf.Value != want {
			t.Fatalf("arg %d: expected %q leaf, got %#v", i, want, mc.Args[i])
		}
	}
}

// `(plain a b)` — no `~` prefix — stays a PBranch.
func TestNonMacroStaysBranch(t *testing.T) {
	tokens, _ := LexPos("(plain a b)")
	tree, _ := ParsePos(tokens)
	if _, ok := tree[0].(*ast.PBranch); !ok {
		t.Fatalf("expected *ast.PBranch, got %T", tree[0])
	}
}

// Macro detection is paren-only — `[~a b]` stays a PBranch (array
// literal, not a macro call).
func TestMacroCallOnlyForParens(t *testing.T) {
	tokens, _ := LexPos("[~a b]")
	tree, _ := ParsePos(tokens)
	if _, ok := tree[0].(*ast.PBranch); !ok {
		t.Fatalf("expected *ast.PBranch for [...] form, got %T", tree[0])
	}
}

// Backslash escapes inside a string must not terminate the literal:
// `'a\'b'` is one string, not `'a\'` followed by garbage. The lexer's
// job is just "don't end here"; the actual translation happens in
// core/eval.go's leaf evaluator.
func TestBackslashEscapeDoesNotTerminate(t *testing.T) {
	src := `(print 'a\'b')`
	errs := parseAll(src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors with \\' escape, got %#v", errs)
	}
	tokens, _ := LexPos(src)
	// Find the string token and confirm its source includes the whole
	// 'a\'b' body (the escaped quote didn't end it early).
	var found bool
	for _, tk := range tokens {
		if strings.HasPrefix(tk.Value, `'`) && strings.HasSuffix(tk.Value, `'`) {
			if tk.Value != `'a\'b'` {
				t.Errorf("expected token 'a\\'b', got %q", tk.Value)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("no string token found in tokens %#v", tokens)
	}
}

func TestNoErrorsOnValidInput(t *testing.T) {
	src := `(var x 5)
(print x.foo)
(set y [1 2 3])
(map &(+ x 1) y)
(var s 'hello')
(var c ` + "`X`" + `)
`
	errs := parseAll(src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors on valid source, got %#v", errs)
	}
}

// ----------------------------------------------------------------------
// Indentation-guided recovery
// ----------------------------------------------------------------------

func parseTree(t *testing.T, src string) ([]ast.PNode, []ParseError) {
	t.Helper()
	tokens, lexErrs := LexPos(src)
	if len(lexErrs) != 0 {
		t.Fatalf("unexpected lex errors: %#v", lexErrs)
	}
	return ParsePos(tokens)
}

// A missing closer mid-file must not swallow the following top-level
// form: the unclosed form is cut off at the dedented line, and the
// error points at the inferred close site, not the opener.
func TestRecoveryMissingCloserMidFile(t *testing.T) {
	src := `(fun f (x)
  (do
    (print x)

(var y 5)
`
	tree, errs := parseTree(t, src)
	if len(tree) != 2 {
		t.Fatalf("expected 2 top-level forms after recovery, got %d: %#v", len(tree), tree)
	}
	// Two unclosed forms: the (do ...) and the (fun ...).
	if len(errs) != 2 {
		t.Fatalf("expected 2 missing-closer errors, got %#v", errs)
	}
	for _, e := range errs {
		// Both inferred close sites sit at the end of `(print x)` —
		// line 3, just after the closing paren at col 13.
		if e.Span.StartLine != 3 || e.Span.StartCol != 14 {
			t.Errorf("expected close site 3:14, got %d:%d (%s)",
				e.Span.StartLine, e.Span.StartCol, e.Message)
		}
		if e.OpenSpan.StartLine == 0 {
			t.Errorf("expected OpenSpan to point at the opener, got zero span (%s)", e.Message)
		}
	}
	// The trailing (var y 5) must have survived as its own form.
	br, ok := tree[1].(*ast.PBranch)
	if !ok || len(br.Children) == 0 {
		t.Fatalf("expected (var y 5) as second top-level form, got %#v", tree[1])
	}
	if head, ok := br.Children[0].(*ast.PLeaf); !ok || head.Value != "var" {
		t.Fatalf("expected second form to be the var decl, got %#v", tree[1])
	}
}

// Balanced code, no matter how badly indented, parses with zero errors —
// the recovery rule must not fire when the closer exists downstream.
func TestRecoveryDoesNotFireOnBalancedCode(t *testing.T) {
	cases := []string{
		"(print\n'hello')",      // continuation at col 1
		"(foo (bar\nbaz))",      // dedented child of inner form
		"  (a x\n)",             // indented top-level, dedented closer
		"(a\n  (b x\n  y\n)\n)", // dedented closers, child at opener indent
		"(a\n(b)\n(c)\n)",       // siblings at col 1 inside col-1 form
	}
	for _, src := range cases {
		tree, errs := parseTree(t, src)
		if len(errs) != 0 {
			t.Errorf("expected no errors for balanced %q, got %#v", src, errs)
		}
		if len(tree) != 1 {
			t.Errorf("expected 1 top-level form for %q, got %d", src, len(tree))
		}
	}
}

// Unclosed at EOF with no dedent: the error lands at the end of the
// last token (the predicted insertion point), with OpenSpan on the
// opener.
func TestRecoveryUnclosedAtEOF(t *testing.T) {
	src := `(do
  (print x)`
	tree, errs := parseTree(t, src)
	if len(tree) != 1 {
		t.Fatalf("expected 1 top-level form, got %d", len(tree))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 missing-closer error, got %#v", errs)
	}
	e := errs[0]
	if e.Span.StartLine != 2 || e.Span.StartCol != 12 {
		t.Errorf("expected close site 2:12, got %d:%d", e.Span.StartLine, e.Span.StartCol)
	}
	if e.OpenSpan.StartLine != 1 || e.OpenSpan.StartCol != 1 {
		t.Errorf("expected OpenSpan 1:1, got %d:%d", e.OpenSpan.StartLine, e.OpenSpan.StartCol)
	}
}

// Nested unclosed forms each get their own error, and both close at
// the same inferred site.
func TestRecoveryNestedUnclosed(t *testing.T) {
	src := `(a
  (b x
(c y)
`
	tree, errs := parseTree(t, src)
	if len(tree) != 2 {
		t.Fatalf("expected 2 top-level forms, got %d", len(tree))
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %#v", errs)
	}
}

// A multi-line string inside a form must not trigger recovery: tokens
// after it inherit indentation state rather than looking line-leading.
func TestRecoveryMultilineString(t *testing.T) {
	src := "(print 'line one\nline two' x)"
	tree, errs := parseTree(t, src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %#v", errs)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 form, got %d", len(tree))
	}
}

// Comment-only lines produce no tokens and are invisible to recovery.
func TestRecoveryCommentLines(t *testing.T) {
	src := `(do
-- a comment at column 1
  (print x))
`
	tree, errs := parseTree(t, src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %#v", errs)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 form, got %d", len(tree))
	}
}

// Deeply nested input must not recurse the parser without bound — past
// maxParseDepth it flattens and reports once, so neither the parser nor
// any downstream walker can stack-overflow (an uncatchable crash).
func TestParseDepthCapNoOverflow(t *testing.T) {
	for _, depth := range []int{maxParseDepth + 50, 200000} {
		src := strings.Repeat("(", depth) + "x" + strings.Repeat(")", depth)
		tokens, _ := LexPos(src)
		tree, errs := ParsePos(tokens) // must return, not crash
		if len(tree) == 0 {
			t.Fatalf("depth %d: expected a top-level form", depth)
		}
		if !hasMessageContaining(errs, "nested too deeply") {
			t.Errorf("depth %d: expected a depth-cap error, got %#v", depth, errs)
		}
		// The cap error is reported exactly once, not once per token.
		n := 0
		for _, e := range errs {
			if strings.Contains(e.Message, "nested too deeply") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("depth %d: depth-cap error reported %d times, want 1", depth, n)
		}
	}
}

// Sigil nesting (`&&…`) routes through parsePrimary too, so it's capped
// the same way.
func TestParseDepthCapSigils(t *testing.T) {
	src := strings.Repeat("&", 200000) + "x"
	tokens, _ := LexPos(src)
	if _, errs := ParsePos(tokens); !hasMessageContaining(errs, "nested too deeply") {
		t.Errorf("expected depth-cap error for deep sigil nesting, got %#v", errs)
	}
}

// Normal nesting depth is unaffected.
func TestParseDepthCapAllowsRealCode(t *testing.T) {
	src := strings.Repeat("(", 50) + "x" + strings.Repeat(")", 50)
	tokens, _ := LexPos(src)
	if _, errs := ParsePos(tokens); len(errs) != 0 {
		t.Errorf("50-deep nesting must parse cleanly, got %#v", errs)
	}
}
