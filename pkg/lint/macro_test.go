package lint

import (
	"strings"
	"testing"
)

// Semantic tokens (LSP highlighting): the `macro` keyword, the declared
// macro name, its parameters, and the call-site name all get the right
// classification — keyword, @macro, @parameter, @macro — so editors paint
// macros distinctly from functions.
func TestSemanticTokensMacro(t *testing.T) {
	src := "(macro ~twice (e) (+ e e))\n(~twice 5)\n"
	got := SemanticTokens("m.phl", []byte(src))
	lines := strings.Split(src, "\n")
	has := func(text string, typ SemanticTokenType, line int) bool {
		for _, g := range got {
			if g.Span.StartLine != line {
				continue
			}
			seg := lines[g.Span.StartLine-1][g.Span.StartCol-1 : g.Span.EndCol-1]
			if seg == text && g.Type == typ {
				return true
			}
		}
		return false
	}
	if !has("macro", SemTokKeyword, 1) {
		t.Errorf("`macro` keyword not classified as keyword; got %+v", got)
	}
	if !has("twice", SemTokMacro, 1) {
		t.Errorf("macro declaration name not classified as @macro; got %+v", got)
	}
	if !has("e", SemTokParameter, 1) {
		t.Errorf("macro parameter not classified as @parameter; got %+v", got)
	}
	if !has("twice", SemTokMacro, 2) {
		t.Errorf("macro call-site name not classified as @macro; got %+v", got)
	}
}

// The `!` call syntax is reserved for macros and required for them: a macro
// must be invoked with `!`, and a function must not be.
func TestMacroCallSyntaxEnforced(t *testing.T) {
	// A macro called WITHOUT `!` is flagged.
	d := AnalyzeFile("test.pho", []byte("(macro ~m (e) e)\n(m 1)"))
	if !hasDiag(d, "macro-needs-prefix") {
		t.Errorf("calling a macro without '!' should be flagged, got %#v", d)
	}

	// A function called WITH `!` is flagged.
	d = AnalyzeFile("test.pho", []byte("(fun f (e) e)\n(~f 1)"))
	if !hasDiag(d, "not-a-macro") {
		t.Errorf("calling a function with '!' should be flagged, got %#v", d)
	}

	// Correct usage — macro with `!`, function without — is clean.
	d = AnalyzeFile("test.pho", []byte("(macro ~m (e) e)\n(fun f (e) e)\n(~m 1)\n(f 1)"))
	if hasDiag(d, "macro-needs-prefix") || hasDiag(d, "not-a-macro") {
		t.Errorf("correct macro/function call syntax should be clean, got %#v", d)
	}
}

// A `(macro ~name (params) body)` declaration lints clean and registers the
// macro so references inside its body and at call sites resolve.
func TestMacroDeclLintsClean(t *testing.T) {
	d := AnalyzeFile("test.pho", []byte("(macro ~twice (e) ['+' e e])\n(let var n = 5)\n(~twice n)"))
	if hasDiag(d, "unresolved-identifier") || hasDiag(d, "bad-form-shape") {
		t.Errorf("a well-formed macro declaration + call should lint clean, got %#v", d)
	}
}
