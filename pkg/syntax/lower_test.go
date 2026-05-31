package syntax

import (
	"strings"
	"testing"

	"pho/pkg/core"
)

// dump renders a tree as a flat string. Used for shape checks where
// the exact ttbranch nesting would be tedious to spell out.
func dumpTree(n core.Node) string {
	if lf, ok := n.(core.Leaf); ok {
		return string(lf)
	}
	br := n.(core.Branch)
	parts := make([]string, len(br))
	for i, c := range br {
		parts[i] = dumpTree(c)
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func lower(src string) core.Node {
	tokens, _ := LexPos(src)
	tree, _ := ParsePos(tokens)
	return Lower(tree)
}

// Sigil sugar — `&body` lowers to (block <body-quoted>).
func TestLowerBlockSigil(t *testing.T) {
	got := dumpTree(lower("&(+ 1 2)"))
	if !strings.Contains(got, "block") {
		t.Fatalf("expected lowered &(...) to contain 'block', got: %s", got)
	}
}

// Quote sugar — `'leaf` lowers to "leaf"; `'(...)` lowers to (slice ...).
func TestLowerQuoteSigil(t *testing.T) {
	got := dumpTree(lower("'x"))
	if !strings.Contains(got, "\"x\"") {
		t.Fatalf("expected 'x to contain \"x\", got: %s", got)
	}
	got = dumpTree(lower("'(a b c)"))
	if !strings.Contains(got, "slice") {
		t.Fatalf("expected '(...) to wrap with slice, got: %s", got)
	}
}

// Dot accessor — `a.b.c` lowers to nested core.Dot calls.
func TestLowerDotChain(t *testing.T) {
	got := dumpTree(lower("(io.PrintLine self.x)"))
	// Should have two distinct dot subtrees (io.PrintLine and self.x).
	if strings.Count(got, core.Dot) < 2 {
		t.Fatalf("expected two dot subtrees, got: %s", got)
	}
}

// Macro call — `(name! a b)` lowers to (resume (name 'a 'b)).
func TestLowerMacroCall(t *testing.T) {
	got := dumpTree(lower("(my! a b)"))
	if !strings.Contains(got, "resume") {
		t.Fatalf("expected lowered macro call to contain 'resume', got: %s", got)
	}
}

// Plain string literals with no `%` markers stay plain leaves — the
// interpolation path should not allocate or rewrite them.
func TestLowerStringNoInterp(t *testing.T) {
	got := dumpTree(lower(`"hello"`))
	if !strings.Contains(got, `"hello"`) || strings.Contains(got, core.Strinterp) {
		t.Errorf("expected plain leaf for non-interpolated string, got %s", got)
	}
}

// Single-name interpolation: `"hi %who"` → (Strinterp "hi " (Strcoerce who)).
func TestLowerInterpName(t *testing.T) {
	got := dumpTree(lower(`"hi %who"`))
	if !strings.Contains(got, core.Strinterp) {
		t.Fatalf("expected Strinterp in lowered output, got %s", got)
	}
	if !strings.Contains(got, core.Strcoerce+" who") {
		t.Errorf("expected (Strcoerce who) wrapping the interpolation, got %s", got)
	}
	if !strings.Contains(got, `"hi "`) {
		t.Errorf("expected literal chunk \"hi \", got %s", got)
	}
}

// Dot-chain interpolation: `"%a.b.c"` → (Strinterp (Strcoerce (Dot (Dot a b) c))).
func TestLowerInterpDotChain(t *testing.T) {
	got := dumpTree(lower(`"%a.b.c"`))
	if !strings.Contains(got, core.Strinterp) {
		t.Fatalf("expected Strinterp, got %s", got)
	}
	if strings.Count(got, core.Dot) != 2 {
		t.Errorf("expected two dot subtrees in chain, got %s", got)
	}
}

// Paren interpolation: `"%(len items)"` → ... (Strcoerce (len items)).
func TestLowerInterpParen(t *testing.T) {
	got := dumpTree(lower(`"%(len items)"`))
	if !strings.Contains(got, "len items") {
		t.Fatalf("expected inner (len items) preserved, got %s", got)
	}
	if !strings.Contains(got, core.Strcoerce) {
		t.Errorf("expected Strcoerce wrapping, got %s", got)
	}
}

// `\%` escapes the marker — no Strinterp produced.
func TestLowerInterpEscape(t *testing.T) {
	got := dumpTree(lower(`"\%name stays literal"`))
	if strings.Contains(got, core.Strinterp) {
		t.Errorf("expected no Strinterp for \\%%-escaped string, got %s", got)
	}
}

// `%(...)` containing an inner `"..."` must not terminate the outer
// string at the inner quote — exercises scanInterpExpr's recursive
// scanString call.
func TestLowerInterpInnerString(t *testing.T) {
	got := dumpTree(lower(`"got %(io.Sprint "x") here"`))
	if !strings.Contains(got, core.Strinterp) {
		t.Fatalf("expected Strinterp, got %s", got)
	}
	if !strings.Contains(got, `"x"`) {
		t.Errorf("expected inner string literal preserved, got %s", got)
	}
	if !strings.Contains(got, "here") {
		t.Errorf("expected literal tail \" here\" preserved, got %s", got)
	}
}

// Bracket / brace literals expand to (slice ...) / (map ...).
func TestLowerArrayDictLiterals(t *testing.T) {
	got := dumpTree(lower("[1 2 3]"))
	if !strings.HasPrefix(got, "((slice 1 2 3))") {
		t.Fatalf("expected [1 2 3] to lower to (slice 1 2 3), got: %s", got)
	}
	got = dumpTree(lower(`{"a" 1}`))
	if !strings.Contains(got, "map") {
		t.Fatalf("expected {...} to lower to map, got: %s", got)
	}
}
