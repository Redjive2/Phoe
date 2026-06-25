package lint

import "testing"

// The bare aliased-import form `(import ("path" alias))` is what the LSP
// must understand: the alias registers as a binding (so an unused one is
// flagged and a used one is not), and it classifies as a namespace token.
func TestImportAliasBareForm(t *testing.T) {
	// Used alias: registered, so no false unresolved/unused, and the
	// malformed-path checker stays quiet on the paren pair.
	used := []byte("(import ('std/io' myio))\n(myio.print_line 1)\n")
	for _, d := range AnalyzeFile("test.pho", used) {
		switch d.Code {
		case "non-string-import-path", "unused-import", "unresolved-identifier":
			t.Errorf("unexpected %s on bare aliased import: %s", d.Code, d.Message)
		}
	}

	// Unused alias is still flagged — proof the bare alias actually
	// registered as a DefImport.
	unused := []byte("(import ('std/io' myio))\n")
	if !hasDiag(AnalyzeFile("test.pho", unused), "unused-import") {
		t.Errorf("expected unused-import for an unreferenced bare alias")
	}

	// Semantic tokens: the bare alias classifies as a namespace.
	toks := SemanticTokens("test.phl", used)
	foundNS := false
	for _, tk := range toks {
		if tk.Type == SemTokNamespace {
			foundNS = true
		}
	}
	if !foundNS {
		t.Errorf("expected a namespace semantic token for the bare import alias, got %+v", toks)
	}
}
