package annot

import (
	"testing"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/goop"
	"pho/pkg/syntax"
)

func parseForm(t *testing.T, src string) ast.PNode {
	t.Helper()
	toks, lexErrs := syntax.LexPos(src)
	tree, parseErrs := syntax.ParsePos(toks)
	if len(lexErrs)+len(parseErrs) != 0 {
		t.Fatalf("parse errors for %q: %v %v", src, lexErrs, parseErrs)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 form from %q, got %d", src, len(tree))
	}
	return tree[0]
}

// noteOverlay supplies a `note` special form that attaches (evaluated arg0 as
// key, evaluated arg1 as value) to the in-flight annotation — a stand-in for
// a real macro until the .phl harvest lands. *calls records how many times
// note actually ran, so a memo hit is observable.
func noteOverlay(calls *int) map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"note": {Val: core.TvFun(func(ctx core.Context, argv []core.Node) core.Value {
			*calls++
			key := argv[0].Evaluate(ctx)
			val := argv[1].Evaluate(ctx)
			k, _ := key.Val.(string)
			theHost.Attach(k, val)
			return core.TvNil
		}), IsConstant: true},
	}
}

// A plain annotation call reaches the sink and produces an entry, with no
// diagnostics.
func TestEvaluateAttaches(t *testing.T) {
	calls := 0
	ev := New(noteOverlay(&calls))
	res := ev.Evaluate(`(note "greeting" "hi")`, parseForm(t, `(note "greeting" "hi")`))
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	if len(res.Entries) != 1 || res.Entries[0].Key != "greeting" {
		t.Fatalf("expected one 'greeting' entry, got %#v", res.Entries)
	}
	if got, _ := res.Entries[0].Value.Val.(string); got != "hi" {
		t.Fatalf("expected value 'hi', got %#v", res.Entries[0].Value)
	}
}

// prepare strips the macro `resume` wrapper: `(sig! ...)` lowers to
// `(resume (sig ...))`, and we must evaluate the inner `(sig ...)` directly.
func TestPrepareUnwrapsResume(t *testing.T) {
	node := prepare(parseForm(t, "(sig! Num Num)"))
	br, ok := core.AsBranch(node)
	if !ok {
		t.Fatalf("expected a branch after prepare, got %T", node)
	}
	head, ok := core.AsLeaf(br[0])
	if !ok || string(head) != "sig" {
		t.Fatalf("expected head 'sig' (resume unwrapped), got %#v", br[0])
	}
}

// End-to-end of the bang path: `(note! foo bar)` lowers to
// `(resume (note 'foo 'bar))`; after unwrap the macro runs exactly once with
// its arguments quoted to strings, and attaches them.
func TestEvaluateMacroCallUnwrapped(t *testing.T) {
	calls := 0
	ev := New(noteOverlay(&calls))
	res := ev.Evaluate(`(note! foo bar)`, parseForm(t, `(note! foo bar)`))
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	if calls != 1 {
		t.Fatalf("expected note called exactly once, got %d", calls)
	}
	if len(res.Entries) != 1 || res.Entries[0].Key != "foo" {
		t.Fatalf("expected one 'foo' entry, got %#v", res.Entries)
	}
	if got, _ := res.Entries[0].Value.Val.(string); got != "bar" {
		t.Fatalf("expected value 'bar', got %q", got)
	}
}

// Each annotation gets its own env: a binding declared in one is invisible to
// the next.
func TestIsolationBetweenAnnotations(t *testing.T) {
	ev := New(nil)
	a := ev.Evaluate(`(const shared 5)`, parseForm(t, `(const shared 5)`))
	if len(a.Diags) != 0 {
		t.Fatalf("annotation A should be clean, got diags: %v", a.Diags)
	}
	b := ev.Evaluate(`(const other shared)`, parseForm(t, `(const other shared)`))
	if len(b.Diags) == 0 {
		t.Fatalf("annotation B must not see A's 'shared'; expected an unresolved-identifier error")
	}
}

// A stray control-flow signal in an annotation becomes a diagnostic, not a
// host crash.
func TestRecoverGuardReturn(t *testing.T) {
	ev := New(nil)
	res := ev.Evaluate(`(return 5)`, parseForm(t, `(return 5)`))
	if len(res.Diags) == 0 {
		t.Fatalf("expected a diagnostic for top-level 'return' in an annotation")
	}
}

// Identical annotation text is evaluated once and served from the memo.
func TestMemoization(t *testing.T) {
	calls := 0
	ev := New(noteOverlay(&calls))
	const raw = `(note "k" "v")`
	first := ev.Evaluate(raw, parseForm(t, raw))
	second := ev.Evaluate(raw, parseForm(t, raw))
	if calls != 1 {
		t.Fatalf("expected note evaluated once (memo hit on 2nd run), got %d calls", calls)
	}
	if len(first.Entries) != 1 || len(second.Entries) != 1 {
		t.Fatalf("both results should carry the one entry; got %d and %d",
			len(first.Entries), len(second.Entries))
	}
}

// The phoAnnot module marshals (string, value) into host.Attach via goop's
// reflective dispatch — the path a real macro takes through (meta.Attach ...).
func TestAttachViaGoop(t *testing.T) {
	New(nil) // ensure phoAnnot is exposed
	mod := goop.GoModules["phoAnnot"]
	if mod == nil {
		t.Fatalf("phoAnnot module not exposed")
	}
	var entries []Entry
	theHost.current = &entries
	defer func() { theHost.current = nil }()

	if _, err := goop.Call(mod, "Attach", []core.Value{core.TvStr("k"), core.TvNum(42)}); err != nil {
		t.Fatalf("goop.Call(Attach) failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "k" {
		t.Fatalf("expected one 'k' entry, got %#v", entries)
	}
	if n, _ := entries[0].Value.Val.(float64); n != 42 {
		t.Fatalf("expected value 42, got %#v", entries[0].Value)
	}
}
