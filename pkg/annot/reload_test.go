package annot

import (
	"os"
	"path/filepath"
	"testing"
)

// A long-lived host (the LSP) must be able to recover from a broken macro
// library once it's fixed on disk, without a restart. The package loader
// keeps a negative cache — a parse failure is remembered for the whole run —
// so a plain re-init keeps failing even after the fix; ReloadDefault exists to
// invalidate that cache. This pins the broken → fixed → reload sequence.
func TestReloadRecoversFromBrokenLibrary(t *testing.T) {
	// InitDefault/ReloadDefault mutate the process-wide evaluator; restore it
	// so this test doesn't leak macros into others. The temp dir is unique, so
	// its loader-cache entries can't collide with another test's library.
	old := Default()
	t.Cleanup(func() { SetDefault(old) })

	dir := t.TempDir()
	lib := filepath.Join(dir, "annot.phl")

	// A minimal real annotation macro (mirrors std/annot's `type`: bare
	// params and body, attaching via the phoAnnot sink). The broken variant
	// has an unterminated string — a lex error, so the package fails to
	// parse.
	const broken = "(goimport (\"phoAnnot\" meta))\n(fun type (t) \"oops)\n"
	const good = "(goimport ('phoAnnot' meta))\n" +
		"(fun type (t)\n    (meta.Attach 'type' t))\n"

	// 1. A library that doesn't parse: InitDefault fails and leaves Default's
	//    macros untouched (annotations would degrade to "macro not defined").
	if err := os.WriteFile(lib, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InitDefault(dir); err == nil {
		t.Fatalf("InitDefault should fail on a library with a parse error")
	}

	// 2. Fix the file on disk — but the loader's negative cache still
	//    remembers the failure, so a plain re-init keeps failing. This is the
	//    edge that needs ReloadDefault: a fix alone isn't picked up.
	if err := os.WriteFile(lib, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InitDefault(dir); err == nil {
		t.Fatalf("InitDefault should still fail: the parse failure is cached until the package is invalidated")
	}

	// 3. ReloadDefault invalidates the cache and loads the fixed library, so
	//    the macro now resolves and attaches its metadata cleanly.
	ReloadDefault(dir)
	res := Default().Evaluate(`(~type Foo)`, parseForm(t, `(~type Foo)`))
	if len(res.Diags) != 0 {
		t.Fatalf("after reload the macro should resolve cleanly, got diags: %v", diagMsgs(res))
	}
	if got := entryString(t, res, "type"); got != "Foo" {
		t.Fatalf("type entry = %q, want Foo", got)
	}
}
