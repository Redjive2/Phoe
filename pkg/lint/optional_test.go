package lint

import "testing"

// Optionality is declared in the SIGNATURE — `(optional Type)` — while the
// clause binds a plain name for the slot. The binder resolves in the body like
// any parameter: no unresolved-identifier false positive.
func TestOptionalParamResolvesInBody(t *testing.T) {
	diags := analyze(t, "(fun f (Number (optional Number)) Number)\n(let f (a b) = (+ a b))\n")
	if hasDiag(diags, "unresolved-identifier") {
		t.Fatalf("the optional slot's clause binder must resolve in the body, got %#v", diags)
	}
}

// The retired impl-side `(optional name)` param slot gets a pointed redirect to
// the signature form.
func TestOptionalImplSlotRetired(t *testing.T) {
	diags := analyze(t, "(let f (a (optional b)) = (+ a b))\n")
	if !hasDiag(diags, "bad-default-param") {
		t.Fatalf("an impl-side (optional b) slot should flag bad-default-param, got %#v", diags)
	}
}

// The `optional` marker in a SIGNATURE highlights as a keyword, its inner slot
// as a type, and the matching clause binder as a parameter.
func TestOptionalSemanticTokens(t *testing.T) {
	src := "(fun f (Number (optional Number)) Number)\n(let f (a b) = b)\n"
	got := SemanticTokens("opt.phl", []byte(src))

	var sawKeyword, sawType, sawParam bool
	for _, tk := range got {
		seg := src[lineColToByte(src, tk.Span.StartLine, tk.Span.StartCol):lineColToByte(src, tk.Span.EndLine, tk.Span.EndCol)]
		if seg == "optional" && tk.Type == SemTokKeyword {
			sawKeyword = true
		}
		if seg == "Number" && tk.Type == SemTokType {
			sawType = true
		}
		// the clause's binder `b` (line 2) as a parameter
		if seg == "b" && tk.Type == SemTokParameter && tk.Span.StartLine == 2 {
			sawParam = true
		}
	}
	if !sawKeyword {
		t.Errorf("expected `optional` classified as SemTokKeyword, tokens: %+v", got)
	}
	if !sawType {
		t.Errorf("expected the optional's type `Number` classified as SemTokType, tokens: %+v", got)
	}
	if !sawParam {
		t.Errorf("expected the clause binder `b` classified as SemTokParameter, tokens: %+v", got)
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
