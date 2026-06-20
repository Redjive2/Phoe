package lint

import "testing"

// TestIfArmVarHoists: a bare var/const declared directly in an if-arm
// leaks into the enclosing scope at runtime (arms run in this scope, no
// frame), so a later reference must resolve — no false unresolved.
func TestIfArmVarHoists(t *testing.T) {
	src := "(fun F () (identity do\n" +
		"    (if True then (var x 5))\n" +
		"    (const y x)\n" +
		"))\n"
	diags := AnalyzeFile("t.phl", []byte(src))
	if hasDiag(diags, "unresolved-identifier") {
		t.Errorf("if-arm var not hoisted: spurious unresolved-identifier\n%+v", diags)
	}
}

// TestDoBodyVarsResolve guards do-notation scoping for the LSP: vars/consts
// declared in a bare `(fun … do …)` body (post de-sigiling) leak into the
// surrounding scope, so later references — in the same block, after it, or in
// a nested loop's own do-block — must resolve with no spurious unresolved.
// This is the regression for "do-notation declarations show up as unresolved".
func TestDoBodyVarsResolve(t *testing.T) {
	cases := map[string]string{
		"fun body const":  "(fun f (a b) do\n  (const sum (+ a b))\n  (+ sum 1))\n",
		"var then rebind": "(fun g () do\n  (var n 0)\n  (= n (+ n 1))\n  n)\n",
		"nested for-do":   "(fun h (xs) do\n  (var acc 0)\n  (foreach x in xs do\n    (var step (+ x 1))\n    (= acc (+ acc step)))\n  acc)\n",
	}
	for name, src := range cases {
		diags := AnalyzeFile("t.phl", []byte(src))
		if hasDiag(diags, "unresolved-identifier") {
			t.Errorf("%s: do-block declarations not recognized — spurious unresolved-identifier\n%+v", name, diags)
		}
	}
}

// TestStringIndexWriteRejected: the runtime's `=` has no string case, so
// writing into a string index must be a static error.
func TestStringIndexWriteRejected(t *testing.T) {
	src := "(fun F () (identity do\n" +
		"    (var s \"hello\")\n" +
		"    (= s.0 9)\n" +
		"))\n"
	diags := AnalyzeFile("t.phl", []byte(src))
	if !hasDiag(diags, "invalid-member-access") {
		t.Errorf("string index write not flagged\n%+v", diags)
	}
}

// TestNestedClosureDoesNotRetargetOuterShape: a nested closure reassigning
// an enclosing function's local must invalidate (not retarget) that
// binding's shape, so the outer's member access isn't checked against the
// closure's value — no false invalid-member-access.
func TestNestedClosureDoesNotRetargetOuterShape(t *testing.T) {
	src := "(struct P Field)\n" +
		"(fun outer () (identity do\n" +
		"    (var x P.{ Field 1 })\n" +
		"    (fun inner () (= x 5))\n" +
		"    (const y x.Field)\n" +
		"))\n"
	diags := AnalyzeFile("t.phl", []byte(src))
	if hasDiag(diags, "invalid-member-access") {
		t.Errorf("outer struct shape corrupted by nested closure reassignment\n%+v", diags)
	}
}

// TestChainedDotCompletionOffersNothing: after a member-access chain like
// `p.X.`, the receiver's type isn't tracked, so completion must offer
// nothing rather than dumping the whole scope.
func TestChainedDotCompletionOffersNothing(t *testing.T) {
	src := "(struct P X)\n" +
		"(var p P.{ X 1 })\n" +
		"(p.X.\n"
	// Cursor right after the second dot on line 3: "(p.X." -> col 6.
	defs := CompletionsAt("t.pho", []byte(src), 3, 6)
	if len(defs) != 0 {
		t.Errorf("chained-dot completion dumped %d names, want 0:\n%+v", len(defs), defs)
	}
}
