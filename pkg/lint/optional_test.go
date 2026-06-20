package lint

import "testing"

// An (optional name) parameter binds the name like a regular param, so
// body references resolve — no unresolved-identifier false positive.
func TestOptionalParamResolvesInBody(t *testing.T) {
	diags := analyze(t, "(fun f (a (optional b)) (+ a b))\n")
	if hasDiag(diags, "unresolved-identifier") {
		t.Fatalf("optional param must resolve in the body, got %#v", diags)
	}
}

// The `optional` marker highlights as a keyword and its name as a
// parameter — mirroring how `spread` is classified.
func TestOptionalSemanticTokens(t *testing.T) {
	src := "(fun f (a (optional b)) b)\n"
	got := SemanticTokens("opt.phl", []byte(src))

	var sawKeyword, sawParam bool
	for _, tk := range got {
		seg := src[lineColToByte(src, tk.Span.StartLine, tk.Span.StartCol):lineColToByte(src, tk.Span.EndLine, tk.Span.EndCol)]
		if seg == "optional" && tk.Type == SemTokKeyword {
			sawKeyword = true
		}
		// the optional's bound name `b` in the param list (line 1) as a parameter
		if seg == "b" && tk.Type == SemTokParameter {
			sawParam = true
		}
	}
	if !sawKeyword {
		t.Errorf("expected `optional` classified as SemTokKeyword, tokens: %+v", got)
	}
	if !sawParam {
		t.Errorf("expected the optional's name `b` classified as SemTokParameter, tokens: %+v", got)
	}
}

// lineColToByte converts a 1-based (line, col) to a byte offset in src.
func lineColToByte(src string, line, col int) int {
	curLine, i := 1, 0
	for i < len(src) && curLine < line {
		if src[i] == '\n' {
			curLine++
		}
		i++
	}
	return i + col - 1
}
