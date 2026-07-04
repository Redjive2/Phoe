package lint

import "testing"

// A clause set whose signature exists in the file but not DIRECTLY above draws
// impl-not-adjacent; the adjacent layout is clean.
func TestImplNotAdjacent(t *testing.T) {
	bad := "(fun f (Number) Number)\n(let unrelated = 5)\n(let f (n) = n)\n"
	if d := AnalyzeFile("t.pho", []byte(bad)); !hasDiag(d, "impl-not-adjacent") {
		t.Fatalf("separated sig + clauses should draw impl-not-adjacent, got %v", d)
	}
	good := "(fun f (Number) Number)\n(let f (n) = n)\n(let unrelated = 5)\n"
	if d := AnalyzeFile("t.pho", []byte(good)); hasDiag(d, "impl-not-adjacent") {
		t.Fatalf("adjacent layout must be clean, got %v", d)
	}
	// §9 overloads: each sig directly followed by its clauses — clean.
	overloads := "(fun f (Number) Number)\n(let f (n) = n)\n" +
		"(fun f (String) String)\n(let f (s) = s)\n"
	if d := AnalyzeFile("t.pho", []byte(overloads)); hasDiag(d, "impl-not-adjacent") {
		t.Fatalf("per-overload adjacency must be clean, got %v", d)
	}
}

// A .phl library requires a signature for every clause set.
func TestSignatureRequiredInLibrary(t *testing.T) {
	if d := AnalyzeFile("lib.phl", []byte("(let f (a) = a)\n")); !hasDiag(d, "signature-required") {
		t.Fatalf("a sig-less .phl clause set must draw signature-required, got %v", d)
	}
	src := "(fun f (Number) Number)\n(let f (a) = a)\n"
	if d := AnalyzeFile("lib.phl", []byte(src)); hasDiag(d, "signature-required") {
		t.Fatalf("a sig'd .phl clause set is fine, got %v", d)
	}
}

// A sibling file's signature satisfies a .phl clause set (no false positive).
func TestSignatureRequiredCrossFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/a.phl": "(fun add (Number Number) Number)\n",
		"lib/b.phl": "(let add (x y) = (+ x y))\n",
	})
	src := "(let add (x y) = (+ x y))\n"
	if d := AnalyzeFile(root+"/lib/b.phl", []byte(src)); hasDiag(d, "signature-required") {
		t.Fatalf("a sibling-file sig satisfies the clause set, got %v", d)
	}
}

// In a .pho script a `let` clause set with no signature always draws
// signature-required — an implementation must be preceded by its declaration,
// scripts and libraries alike. This holds even when the clause patterns fully
// constrain every slot: an inferable shape is not a written contract, and the
// decl/impl split requires the contract to be explicit.
func TestSignatureRequiredInScript(t *testing.T) {
	// A partially-constrained set (a bare-binder slot) needs a sig.
	bad := "(let f (a 0) = a)\n(let f (a b) = b)\n"
	if d := AnalyzeFile("t.pho", []byte(bad)); !hasDiag(d, "signature-required") {
		t.Fatalf("a sig-less script clause set must draw signature-required, got %v", d)
	}
	// A fully-constrained set (every slot pinned by a pattern) STILL needs a sig
	// — inference no longer substitutes for a written declaration.
	good := "(let g ((Number a) 0) = a)\n(let g ((Number a) b) = b)\n"
	if d := AnalyzeFile("t.pho", []byte(good)); !hasDiag(d, "signature-required") {
		t.Fatalf("even fully-constrained clauses need a signature, got %v", d)
	}
	// A lone all-binder clause set likewise.
	if d := AnalyzeFile("t.pho", []byte("(let h (x) = x)\n")); !hasDiag(d, "signature-required") {
		t.Fatalf("an all-binder sig-less script clause draws signature-required, got %v", d)
	}
	// With a signature nothing fires.
	sigd := "(fun h (Number) Number)\n(let h (x) = x)\n"
	if d := AnalyzeFile("t.pho", []byte(sigd)); hasDiag(d, "signature-required") {
		t.Fatalf("a sig'd script clause set is fine, got %v", d)
	}
}

// Exhaustiveness: a set with literal patterns and no catch-all is flagged;
// adding an unguarded all-binder clause clears it.
func TestNonExhaustiveClauses(t *testing.T) {
	bad := "(fun fib (Number) Number)\n(let fib (0) = 0)\n(let fib (1) = 1)\n"
	if d := AnalyzeFile("t.pho", []byte(bad)); !hasDiag(d, "non-exhaustive-clauses") {
		t.Fatalf("literal-only clauses must draw non-exhaustive-clauses, got %v", d)
	}
	good := bad + "(let fib (n) = (+ (fib (- n 1)) (fib (- n 2))))\n"
	if d := AnalyzeFile("t.pho", []byte(good)); hasDiag(d, "non-exhaustive-clauses") {
		t.Fatalf("a catch-all clause makes the set exhaustive, got %v", d)
	}
}

// A guarded all-binder clause is NOT a catch-all; an unguarded one is.
func TestGuardedClauseNotCatchAll(t *testing.T) {
	bad := "(fun f (Number) Number)\n(let f (n) where (> n 0) = n)\n(let f (0) = 0)\n"
	if d := AnalyzeFile("t.pho", []byte(bad)); !hasDiag(d, "non-exhaustive-clauses") {
		t.Fatalf("guarded + literal clauses have a gap, got %v", d)
	}
}

// Boolean literal coverage {true false} is exhaustive.
func TestBoolCoverageExhaustive(t *testing.T) {
	src := "(fun toggle (Boolean) Boolean)\n(let toggle (true) = false)\n(let toggle (false) = true)\n"
	if d := AnalyzeFile("t.pho", []byte(src)); hasDiag(d, "non-exhaustive-clauses") {
		t.Fatalf("true+false cover Boolean, got %v", d)
	}
}

// Full coverage of a signature's closed atom union is exhaustive; a missing
// atom is a gap.
func TestAtomUnionCoverage(t *testing.T) {
	full := "(fun mode ((Or :read :write)) Number)\n(let mode (:read) = 1)\n(let mode (:write) = 2)\n"
	if d := AnalyzeFile("t.pho", []byte(full)); hasDiag(d, "non-exhaustive-clauses") {
		t.Fatalf("all atoms covered, got %v", d)
	}
	gap := "(fun mode ((Or :read :write)) Number)\n(let mode (:read) = 1)\n"
	if d := AnalyzeFile("t.pho", []byte(gap)); !hasDiag(d, "non-exhaustive-clauses") {
		t.Fatalf(":write is uncovered, got %v", d)
	}
}

// A `(const T)` signature slot moves exhaustiveness to the CALL SITES: the set
// itself is clean, a call must pass a parse-time constant, and the constant
// must match an implemented clause literal.
func TestConstSlotDispatch(t *testing.T) {
	src := "(fun conv ((const Type) Number) Number)\n" +
		"(let conv (Number n) = n)\n" +
		"(let conv (String n) = n)\n"
	if d := AnalyzeFile("t.pho", []byte(src)); hasDiag(d, "non-exhaustive-clauses") {
		t.Fatalf("const-slot dispatch is checked at call sites, got %v", d)
	}
	// A constant that matches an implemented literal — clean.
	if d := AnalyzeFile("t.pho", []byte(src+"(conv Number 5)\n")); len(d) != 0 {
		t.Fatalf("matching const call should be clean, got %v", d)
	}
	// A runtime value in the const slot — const-arg-not-static.
	runtimeArg := src + "(fun g (Type) Number)\n(let g (t) = (conv t 5))\n"
	if d := AnalyzeFile("t.pho", []byte(runtimeArg)); !hasDiag(d, "const-arg-not-static") {
		t.Fatalf("a runtime value in a const slot must be flagged, got %v", d)
	}
	// A constant matching no clause literal — no-impl-for-const.
	if d := AnalyzeFile("t.pho", []byte(src+"(conv Boolean 5)\n")); !hasDiag(d, "no-impl-for-const") {
		t.Fatalf("an unimplemented constant must be flagged, got %v", d)
	}
	// A binder clause at the const slot leaves it open — any constant is fine.
	open := src + "(let conv (t n) = n)\n(conv Boolean 5)\n"
	if d := AnalyzeFile("t.pho", []byte(open)); hasDiag(d, "no-impl-for-const") {
		t.Fatalf("an open const slot accepts any constant, got %v", d)
	}
}
