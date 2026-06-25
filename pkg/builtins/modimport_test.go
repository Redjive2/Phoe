package builtins

import (
	"testing"

	"pho/pkg/core"
	"pho/pkg/diag"
	"pho/pkg/syntax"
)

// loweredImportArgs lowers a single (import ...) / (goimport ...) form and
// returns the call's argument nodes (everything after the head leaf).
func loweredImportArgs(t *testing.T, src string) []core.Node {
	t.Helper()
	tokens, _ := syntax.LexPos(src)
	tree, _ := syntax.ParsePos(tokens)
	lowered, ok := syntax.Lower(tree).(core.Branch)
	if !ok || len(lowered) == 0 {
		t.Fatalf("Lower did not return forms for %q", src)
	}
	call, ok := core.AsBranch(lowered[0])
	if !ok || len(call) < 2 {
		t.Fatalf("expected an import call with args for %q", src)
	}
	return call[1:]
}

func importTestCtx() core.Context {
	env := NewEnv()
	file := &core.File{Mode: core.ModeProgram, Imports: map[string]core.Value{}}
	ctx := core.Context{Env: &env, File: file, Diag: diag.NewSession()}
	ctx.PushFrame()
	return ctx
}

// The aliased import form is the parenthesized pair ("path" 'alias); the
// bare form takes the path's last segment as the alias.
func TestParseImportRequestsForms(t *testing.T) {
	cases := []struct {
		src   string
		path  string
		alias string
	}{
		{`(import ('std/core' c))`, "std/core", "c"},
		{`(import 'std/core')`, "std/core", "core"},
		{`(goimport ('stdDependencies' dep))`, "stdDependencies", "dep"},
	}
	for _, tc := range cases {
		argv := loweredImportArgs(t, tc.src)
		reqs := parseImportRequests(importTestCtx(), argv, "import")
		if len(reqs) != 1 {
			t.Fatalf("%s: got %d requests, want 1", tc.src, len(reqs))
		}
		if reqs[0].PackagePath != tc.path || reqs[0].Alias != tc.alias {
			t.Errorf("%s: got {%q %q}, want {%q %q}", tc.src, reqs[0].PackagePath, reqs[0].Alias, tc.path, tc.alias)
		}
	}
}

// Hard cutover: the alias must be a bare name. The old bracket form, a
// string alias, and a non-pair are all rejected (no request produced).
func TestParseImportRequestsRejectsLegacyForms(t *testing.T) {
	for _, src := range []string{
		`(import ['std/core' c])`,   // old bracket delimiter
		`(import ('std/core' 'c'))`, // string alias
		`(import ('a' 'b' 'c'))`,    // 3-element pair is malformed
	} {
		reqs := parseImportRequests(importTestCtx(), loweredImportArgs(t, src), "import")
		if len(reqs) != 0 {
			t.Errorf("%s: expected 0 requests (rejected), got %d", src, len(reqs))
		}
	}
}
