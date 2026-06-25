package lint

import "testing"

// The revised block notation binds an implicit `it` inside `&expr` / `&do …`,
// so the body resolves `it` cleanly — but `it` must not leak outside a block.
func TestBlockItResolution(t *testing.T) {
	clean := []string{
		"(fun apply (x f) (f x))\n(apply 3 &(+ it 1))",
		"(fun apply (x f) (f x))\n(apply 3 &do (let y = (+ it 1)) (* y 2))",
		"(fun apply (x f) (f x))\n(apply 3 &none)",
		// nested blocks each get their own `it`
		"(fun apply (x f) (f x))\n(apply 1 &(apply 2 &(* it it)))",
	}
	for _, src := range clean {
		for _, d := range AnalyzeFile("t.pho", []byte(src)) {
			t.Errorf("%q should lint clean, got %s: %s", src, d.Code, d.Message)
		}
	}

	// `it` outside any block is unresolved.
	if !hasDiag(AnalyzeFile("t.pho", []byte("(+ it 1)")), "unresolved-identifier") {
		t.Errorf("`it` outside a block should be flagged unresolved")
	}
}

// Editor niceties for `it`: completion offers it inside a `&` block (but not
// outside), and semantic tokens paint it @parameter.
func TestBlockItEditorFeatures(t *testing.T) {
	src := []byte("(fun apply (x f) (f x))\n(apply 3 &(+ it 1))")

	// Completion at the cursor just past `it` inside the block.
	if defs := CompletionsAt("t.pho", src, 2, 15); !containsName(defs, "it") {
		t.Errorf("completion inside a `&` block should offer 'it': %v", defNames(defs))
	}
	// Not offered outside any block (inside the fun arg list area).
	if defs := CompletionsAt("t.pho", src, 1, 10); containsName(defs, "it") {
		t.Errorf("completion outside a block should NOT offer 'it'")
	}

	// Semantic token: `it` is @parameter.
	lines := []string{"(fun apply (x f) (f x))", "(apply 3 &(+ it 1))"}
	var got bool
	for _, tk := range SemanticTokens("t.pho", src) {
		s := lines[tk.Span.StartLine-1]
		if tk.Span.StartCol-1 >= 0 && tk.Span.EndCol-1 <= len(s) &&
			s[tk.Span.StartCol-1:tk.Span.EndCol-1] == "it" {
			got = true
			if tk.Type != SemTokParameter {
				t.Errorf("`it` token type = %v, want SemTokParameter", tk.Type)
			}
		}
	}
	if !got {
		t.Errorf("no semantic token emitted for `it`")
	}
}
