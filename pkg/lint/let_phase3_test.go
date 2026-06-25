package lint

import "testing"

// TestLetLintsAsConstVar confirms the parse-time let/let-var desugar preserves
// the const/var distinction for the linter: `let` binds a constant (reassigning
// it flags set-on-constant), `let var` binds mutable state, and the infix
// `(name = value)` reassignment resolves to the binding (Doc/PlanV1/Syntax.md,
// Phase 3).
func TestLetLintsAsConstVar(t *testing.T) {
	// A `let` binding resolves like any const — no unresolved-identifier on read.
	d := AnalyzeFile("t.pho", []byte("(let = = 1)\nx"))
	if hasDiag(d, "unresolved-identifier") || hasDiag(d, "parse-error") || hasDiag(d, "set-on-constant") {
		t.Fatalf("clean let program produced unexpected diags: %v", d)
	}

	// `let var` is mutable — infix reassignment is allowed.
	d = AnalyzeFile("t.pho", []byte("(let var var c = = 1)\n(c = 2)"))
	if hasDiag(d, "set-on-constant") || hasDiag(d, "parse-error") {
		t.Fatalf("reassigning a let var produced unexpected diags: %v", d)
	}

	// `let` (no var) is constant — reassigning it flags set-on-constant.
	d = AnalyzeFile("t.pho", []byte("(let = = 1)\n(x = 2)"))
	if !hasDiag(d, "set-on-constant") {
		t.Fatalf("reassigning a let constant did not flag set-on-constant; got %v", d)
	}
}
