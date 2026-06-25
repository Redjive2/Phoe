package lint

import (
	"path/filepath"
	"testing"
)

// Inline type-signature syntax — Phase 1 (surface forms, runtime-erased).
// See Doc/PlanV1/TypeSignatures.md. Phase 1 only RECOGNIZES the forms (so a
// typed binding resolves and lints clean); type CHECKING against the declared
// type is Phase 3.

// A typed binding `(const (Type x) v)` / `(var (Type x) v)` binds the name and
// erases the type — the name resolves like any other, and the form lints clean.
func TestTypedBindingRecognized(t *testing.T) {
	clean := []string{
		"(const (Number n) 5)\n(+ n 1)",
		"(var (String s) 'hi')\ns.Size",
		"(var (Number a) 1 (Number b) 2)\n(+ a b)",
		"(const ((Or Number String) u) 5)\nu", // a type-FORM in the type slot
		"(const x 5)\nx",                      // untyped still clean
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean for %q, got %v", src, d)
		}
	}
}

// The bound name in a typed binding is the SECOND element; referencing the
// type name as if it were the binding is still unresolved.
func TestTypedBindingNameIsSecond(t *testing.T) {
	// `n` is the binding; `Nope` is not defined.
	d := AnalyzeFile("t.pho", []byte("(const (Number n) 5)\n(+ Nope 1)"))
	if !hasDiagWithName(d, "unresolved-identifier", "Nope") {
		t.Fatalf("expected Nope unresolved, got %v", d)
	}
}

// A function SIGNATURE `(fun add (T…) R)` is recognized (types in the param
// list, a type return) and erased: only the implementation binds the name, so
// a sig + impl pair lints clean regardless of order.
func TestFunSignatureRecognized(t *testing.T) {
	clean := []string{
		"(fun add (Number Number) Number)\n(fun add (a b) (+ a b))\n(add 1 2)",
		"(fun add (a b) (+ a b))\n(fun add (Number Number) Number)\n(add 1 2)", // impl first
		"(fun id (Self) Self)\n(fun id (x) x)\n(id 5)",
		"(fun pick () (Or Number Nil))\n(fun pick () Nil)\n(pick)", // type-form return
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean for %q, got %v", src, d)
		}
	}
}

// The casing/connective heuristic must NOT mistake an implementation for a
// signature: a body that is a CALL to a capitalized function `(Helper)`, or a
// capitalized VALUE literal (Nil/True/False), is an impl, not a return type.
func TestFunImplNotMistakenForSig(t *testing.T) {
	// `(use)` resolves only if `(fun use () (Helper))` was registered as an impl.
	clean := []string{
		"(fun Helper () 1)\n(fun use () (Helper))\n(use)",
		"(fun ok () True)\n(ok)",
		"(fun no () Nil)\n(no)",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean (impl, not sig) for %q, got %v", src, d)
		}
	}
}

// A method SIGNATURE `(method Recv.Name (Self …) Ret)` (receiver type in param
// 0) is recognized and erased; the `self`-bodied implementation registers it.
func TestMethodSignatureRecognized(t *testing.T) {
	clean := []string{
		"(struct P X)\n(method P.Show (Self) Number)\n(method P.Show (self) self.X)\n(var p P.{ X 5 })\np.Show",
		"(struct P X)\n(method P.Show (self) self.X)\n(var p P.{ X 5 })\np.Show", // impl-only
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean for %q, got %v", src, d)
		}
	}
}

// --- Phase 2: hoisting + missing-implementation + §3 casing enforcement ---

// A signature with no matching implementation is a static error, at the sig's
// span; a sig + impl pair is clean regardless of order (signatures hoist).
func TestMissingImplementation(t *testing.T) {
	missing := []string{
		"(fun add (Number Number) Number)",                // lone fun sig
		"(struct P X)\n(method P.Show (Self) Number)",     // lone method sig
		"(fun add (Number Number) Number)\n(const add 5)", // wrong kind (const, not fun)
	}
	for _, src := range missing {
		if d := AnalyzeFile("t.pho", []byte(src)); !hasDiag(d, "missing-implementation") {
			t.Errorf("expected missing-implementation for %q, got %v", src, d)
		}
	}
	clean := []string{
		"(fun add (Number Number) Number)\n(fun add (a b) (+ a b))", // sig then impl
		"(fun add (a b) (+ a b))\n(fun add (Number Number) Number)", // impl then sig (hoist)
		"(struct P X)\n(method P.Show (Self) Number)\n(method P.Show (self) self.X)",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); hasDiag(d, "missing-implementation") {
			t.Errorf("unexpected missing-implementation for %q, got %v", src, d)
		}
	}
}

// An implementation in a sibling file of the same package satisfies a sig; a
// sig with no implementation anywhere is flagged.
func TestSigImplCrossFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/a.phl": "(fun Add (Number Number) Number)\n", // sig
		"lib/b.phl": "(fun Add (x y) (+ x y))\n",          // impl in sibling
	})
	a := filepath.Join(root, "lib/a.phl")
	if d := AnalyzeFile(a, []byte("(fun Add (Number Number) Number)\n")); hasDiag(d, "missing-implementation") {
		t.Errorf("sibling-file impl should satisfy the sig, got %v", d)
	}

	root2 := writeTree(t, map[string]string{"lib/c.phl": "(fun Solo (Number) Number)\n"})
	c := filepath.Join(root2, "lib/c.phl")
	if d := AnalyzeFile(c, []byte("(fun Solo (Number) Number)\n")); !hasDiag(d, "missing-implementation") {
		t.Errorf("a sig with no impl anywhere should be flagged, got %v", d)
	}
}

// §3 enforcement: a Capitalized identifier used as an implementation parameter
// name is flagged (it reads as a type — a probable mistaken signature). The
// receiver name `Self` and the value literals are excluded.
func TestCapitalizedParamFlagged(t *testing.T) {
	if d := AnalyzeFile("t.pho", []byte("(fun add (Number b) (+ b 1))")); !hasDiag(d, "capitalized-param") {
		t.Errorf("expected capitalized-param, got %v", d)
	}
	clean := []string{
		"(fun add (a b) (+ a b))",
		"(fun add (Number Number) Number)\n(fun add (a b) (+ a b))",                 // the sig is skipped, not flagged
		"(struct C N)\n(static property C.Zero get (method C (Self) Self.{ N 0 }))", // Self ok
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); hasDiag(d, "capitalized-param") {
			t.Errorf("unexpected capitalized-param for %q, got %v", src, d)
		}
	}
}

// --- Phase 3: inline signatures feed the gradual checker (additive) ---

// An inline fun signature is checked like `--@ (~sig …)` was: call arguments
// and the implementation's return are validated against it.
func TestInlineFunSigChecks(t *testing.T) {
	bad := []string{
		"(fun f (Number) Number)\n(fun f (n) n)\n(f 'hi')", // arg mismatch
		"(fun f (Number) String)\n(fun f (n) n)",           // return mismatch
	}
	for _, src := range bad {
		if !hasDiag(AnalyzeFile("t.pho", []byte(src)), "type-mismatch") {
			t.Errorf("expected type-mismatch for %q", src)
		}
	}
	good := []string{
		"(fun f (Number) Number)\n(fun f (n) n)\n(f 5)",
		"(fun f (Number) Number)\n(fun f (n) n)",
		"(fun g (a) a)\n(g 'hi')", // un-typed: gradual, never fires
	}
	for _, src := range good {
		if hasDiag(AnalyzeFile("t.pho", []byte(src)), "type-mismatch") {
			t.Errorf("unexpected type-mismatch for %q", src)
		}
	}
}

// An inline typed binding's value is checked against the declared type.
func TestInlineTypedBindingChecks(t *testing.T) {
	if !hasDiag(AnalyzeFile("t.pho", []byte("(const (Number n) 'hi')")), "type-mismatch") {
		t.Error("expected type-mismatch for (const (Number n) 'hi')")
	}
	if hasDiag(AnalyzeFile("t.pho", []byte("(const (Number n) 5)")), "type-mismatch") {
		t.Error("unexpected type-mismatch for (const (Number n) 5)")
	}
}

// An inline method signature checks method-call arguments; param 0 is the
// receiver type, excluded from the call signature.
func TestInlineMethodSigChecks(t *testing.T) {
	base := "(struct P X)\n(method P.Take (Self Number) Number)\n(method P.Take (self n) n)\n(var p P.{ X 1 })\n"
	if !hasDiag(AnalyzeFile("t.pho", []byte(base+"(p.Take 'hi')")), "type-mismatch") {
		t.Error("expected type-mismatch for (p.Take 'hi')")
	}
	if hasDiag(AnalyzeFile("t.pho", []byte(base+"(p.Take 5)")), "type-mismatch") {
		t.Error("unexpected type-mismatch for (p.Take 5)")
	}
}
