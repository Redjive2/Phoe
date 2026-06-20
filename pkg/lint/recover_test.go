package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// Every analysis entrypoint is panic-safe by contract: a panic deep in
// analysis is recovered, the call returns its zero value, and PanicHook
// is notified (so the host can still log it). We force a deterministic
// panic by making the source reader — which PackageScope/collectHits
// consult for sibling files — blow up.
func TestEntrypointsRecoverFromPanic(t *testing.T) {
	var fired []string
	PanicHook = func(op string, r any, stack []byte) { fired = append(fired, op) }
	defer func() { PanicHook = nil }()
	SetSourceReader(func(string) ([]byte, error) { panic("boom from source reader") })
	defer SetSourceReader(nil)

	dir := t.TempDir()
	// A sibling library forces PackageScope to call the (panicking) reader.
	if err := os.WriteFile(filepath.Join(dir, "sib.phl"), []byte("(fun Helper () 1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.phl")
	src := []byte("(fun Use () (Helper))\n")

	// None of these may panic; each must return its zero value.
	if site, ok := DefinitionAt(path, src, 1, 17); ok || site != (DefSite{}) {
		t.Errorf("DefinitionAt: expected zero result on panic, got %v %v", site, ok)
	}
	if md, _, ok := HoverAt(path, src, 1, 17); ok || md != "" {
		t.Errorf("HoverAt: expected empty result on panic, got %q %v", md, ok)
	}
	if got := ReferencesAt(dir, path, src, 1, 17); got != nil {
		t.Errorf("ReferencesAt: expected nil on panic, got %v", got)
	}
	if got := CompletionsAt(path, src, 1, 17); got != nil {
		t.Errorf("CompletionsAt: expected nil on panic, got %v", got)
	}
	if got := SemanticTokens(path, src); got != nil {
		t.Errorf("SemanticTokens: expected nil on panic, got %v", got)
	}

	if len(fired) == 0 {
		t.Fatal("expected PanicHook to fire (the reader panic must have been recovered, not swallowed silently)")
	}
}
