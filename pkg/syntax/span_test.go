package syntax

import (
	"testing"

	"pho/pkg/core"
)

// TestLowerAttachesSpans pins the wrapper insertion: every lowered form
// carries the source span of the PNode it came from; bare leaves don't.
func TestLowerAttachesSpans(t *testing.T) {
	tokens, _ := LexPos(`(+ 1 (f 2))`)
	tree, _ := ParsePos(tokens)
	top, ok := Lower(tree).(core.Branch)
	if !ok || len(top) != 1 {
		t.Fatalf("Lower shape: %#v", Lower(tree))
	}

	sp, ok := core.SpanOf(top[0])
	if !ok {
		t.Fatal("top-level call form has no span")
	}
	if sp.StartLine != 1 || sp.StartCol != 1 || sp.EndCol != 12 {
		t.Errorf("outer span = %+v, want 1:1..1:12", sp)
	}

	outer, _ := core.AsBranch(top[0])
	if _, ok := outer[0].(core.Leaf); !ok {
		t.Errorf("head leaf must stay unwrapped, got %T", outer[0])
	}
	inner, ok := core.SpanOf(outer[2])
	if !ok {
		t.Fatal("nested call form has no span")
	}
	if inner.StartCol != 6 || inner.EndCol != 11 {
		t.Errorf("inner span = %+v, want cols 6..11", inner)
	}
}

// TestDereprTransfersSpans pins the quote round-trip: a quoted form
// (as a fun body would be) keeps its source span through Derepr.
func TestDereprTransfersSpans(t *testing.T) {
	tokens, _ := LexPos(`'(+ n 1)`)
	tree, _ := ParsePos(tokens)
	top := Lower(tree).(core.Branch)

	// The quoted tree itself is wrapped with the (+ n 1) form's span.
	quoted := top[0]
	qsp, ok := core.SpanOf(quoted)
	if !ok {
		t.Fatal("quoted form has no span")
	}

	body := Derepr(quoted)
	bsp, ok := core.SpanOf(body)
	if !ok {
		t.Fatal("Derepr dropped the span")
	}
	if bsp != qsp {
		t.Errorf("span changed through Derepr: %+v -> %+v", qsp, bsp)
	}
	if br, ok := core.AsBranch(body); !ok || len(br) != 3 {
		t.Errorf("Derepr shape wrong: %s", core.Inspect(body))
	}
}

// TestNoSpansEnvDisablesWrapping documents the PHO_NO_SPANS escape
// hatch. The env var is read at package init, so this test can only
// assert the current process's mode is consistent with the env — the
// real A/B coverage is the subprocess test in main_test.go.
func TestNoSpansEnvDisablesWrapping(t *testing.T) {
	tokens, _ := LexPos(`(f 1)`)
	tree, _ := ParsePos(tokens)
	top := Lower(tree).(core.Branch)
	_, wrapped := core.SpanOf(top[0])
	if wrapped == noSpans {
		t.Errorf("wrapped=%v inconsistent with noSpans=%v", wrapped, noSpans)
	}
}
