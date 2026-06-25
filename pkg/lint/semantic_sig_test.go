package lint

import "testing"

// Inline signatures paint their type slots @type, not @parameter/code — the
// LSP polish for TypeSignatures.md Phase 5.
func TestSemanticTokensSignatureTypes(t *testing.T) {
	count := func(toks []SemanticToken, line int) (nType, nParam int) {
		for _, tk := range toks {
			if tk.Span.StartLine != line {
				continue
			}
			switch tk.Type {
			case SemTokType:
				nType++
			case SemTokParameter:
				nParam++
			}
		}
		return
	}

	t.Run("fun sig", func(t *testing.T) {
		// line 1 is the signature, line 2 the implementation.
		src := "(fun add (Number Number) Number)\n(fun add (x y) (+ x y))"
		toks := SemanticTokens("t.phl", []byte(src))
		if nType, nParam := count(toks, 1); nType != 3 || nParam != 0 {
			t.Errorf("sig line: @type=%d @parameter=%d, want 3/0", nType, nParam)
		}
		if _, nParam := count(toks, 2); nParam < 2 {
			t.Errorf("impl line: @parameter=%d, want >=2 (x, y)", nParam)
		}
	})

	t.Run("method sig", func(t *testing.T) {
		src := "(struct R v)\n(method R.take (R Number) Boolean)\n(method R.take (self n) true)"
		toks := SemanticTokens("t.phl", []byte(src))
		if nType, nParam := count(toks, 2); nType < 3 || nParam != 0 {
			t.Errorf("method-sig line: @type=%d @parameter=%d, want >=3/0", nType, nParam)
		}
	})

	t.Run("compound typed binding", func(t *testing.T) {
		src := "(let ((Or Number String) id) = 5)"
		if nType, _ := count(SemanticTokens("t.phl", []byte(src)), 1); nType != 3 {
			t.Errorf("typed-binding: @type=%d, want 3 (Or, Number, String)", nType)
		}
	})
}
