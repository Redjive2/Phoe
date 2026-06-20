package lint

import "testing"

// identity is a one-argument builtin that returns its argument unchanged. The
// linter must (a) recognize it as predeclared, so a call doesn't trip the
// unresolved-identifier checker, and (b) classify it as a builtin function —
// SemTokFunction, which the LSP/tree-sitter paint as @function.builtin (the
// same "orange" scope as len/drop/inspect), not a keyword or operator.
func TestIdentityBuiltin(t *testing.T) {
	const src = "(identity 5)\n"

	for _, d := range AnalyzeFile("id.pho", []byte(src)) {
		t.Errorf("unexpected diagnostic for (identity 5): [%s] %s", d.Code, d.Message)
	}

	toks := SemanticTokens("id.pho", []byte(src))
	if len(toks) != 1 {
		t.Fatalf("want exactly one semantic token (identity), got %d: %+v", len(toks), toks)
	}
	if toks[0].Type != SemTokFunction {
		t.Errorf("identity classified as %v, want SemTokFunction (the orange @function.builtin scope)", toks[0].Type)
	}
}
