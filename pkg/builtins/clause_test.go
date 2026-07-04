package builtins

import (
	"testing"

	"pho/pkg/core"
)

// evalNum evaluates a program and asserts a numeric result.
func evalNum(t *testing.T, src string, want float64) {
	t.Helper()
	v := evalProgram(t, src)
	if v.Kind != core.KindNum {
		t.Fatalf("eval(%q): kind %q (%v), want num", src, v.Kind, v.Val)
	}
	if got := v.Val.(float64); got != want {
		t.Errorf("eval(%q) = %v, want %v", src, got, want)
	}
}

// §1 — guarded multi-implementation clauses, tried in order.
func TestClauseGuards(t *testing.T) {
	src := "(fun add (Number Number) Number)\n" +
		"(let add (a b) where (== b 0) = a)\n" +
		"(let add (a b) where (== a 0) = b)\n" +
		"(let add (a b) = (+ a b))\n"
	evalNum(t, src+"(add 5 0)", 5)
	evalNum(t, src+"(add 0 7)", 7)
	evalNum(t, src+"(add 2 3)", 5)
}

// §2 — literal patterns in parameter lists.
func TestClauseLiteralPatterns(t *testing.T) {
	src := "(fun add (Number Number) Number)\n" +
		"(let add (a 0) = a)\n" +
		"(let add (0 b) = b)\n" +
		"(let add (a b) = (+ a b))\n"
	evalNum(t, src+"(add 5 0)", 5)
	evalNum(t, src+"(add 0 7)", 7)
	evalNum(t, src+"(add 2 3)", 5)
}

// §2 — struct destructuring in a method clause (private field, receiver
// privilege) and list patterns.
func TestClauseStructPattern(t *testing.T) {
	src := "(struct Deep-Box #inner)\n" +
		"(method Deep-Box.get-data (Self) Number)\n" +
		"(let Deep-Box.get-data (Deep-Box.{ #inner = value }) = value)\n" +
		"(let b = Deep-Box.{ #inner = 42 })\n"
	evalNum(t, src+"(b.get-data)", 42)
}

// Method clauses: plain receiver binder + literal dispatch on an argument.
func TestClauseMethodPatterns(t *testing.T) {
	src := "(struct Box n)\n" +
		"(method Box.pick (Self Number) Number)\n" +
		"(let Box.pick (self 0) = self.n)\n" +
		"(let Box.pick (self k) = k)\n" +
		"(let bx = Box.{ n = 9 })\n"
	evalNum(t, src+"(bx.pick 0)", 9)
	evalNum(t, src+"(bx.pick 3)", 3)
}

// Sig `else` defaults: none-coalescing (omitted OR explicit none), real 0 kept.
func TestClauseSigDefaults(t *testing.T) {
	src := "(fun add (Number (optional Number else 10)) Number)\n" +
		"(let add (a b) = (+ a b))\n"
	evalNum(t, src+"(add 5)", 15)
	evalNum(t, src+"(add 5 none)", 15)
	evalNum(t, src+"(add 5 3)", 8)
	evalNum(t, src+"(add 5 0)", 5)
}

// §9 — type-directed overloading: two sigs, dispatch by runtime arg types.
func TestClauseOverloading(t *testing.T) {
	src := "(fun pick (Number) Number)\n" +
		"(let pick (n) = (+ n 1))\n" +
		"(fun pick (String) Number)\n" +
		"(let pick (s) = 100)\n"
	evalNum(t, src+"(pick 5)", 6)
	evalNum(t, src+"(pick 'hi')", 100)
}

// §9 — most-specific overload wins when several accept.
func TestClauseMostSpecific(t *testing.T) {
	src := "(fun size (Unknown) Number)\n" +
		"(let size (x) = 0)\n" +
		"(fun size (Number) Number)\n" +
		"(let size (n) = 1)\n"
	evalNum(t, src+"(size 'str')", 0)
	evalNum(t, src+"(size 42)", 1)
}

// disc-style type-value dispatch as a literal pattern + wildcard clause.
func TestClauseTypeValuePattern(t *testing.T) {
	src := "(fun conv ((const Type) Number) Number)\n" +
		"(let conv (Number x) = (+ x 1))\n" +
		"(let conv (t x) = 0)\n"
	evalNum(t, src+"(conv Number 5)", 6)
	evalNum(t, src+"(conv String 5)", 0)
}

// A trailing (spread T) in the SIGNATURE makes the clause's final parameter
// collect the tail — whether the clause writes an explicit (spread name) or a
// plain binder. The signature is authoritative; the clause needn't repeat it
// (mirrors (var Self) — Doc/PlanV1/DeclImplSplit.md).
func TestClauseSpread(t *testing.T) {
	// Explicit (spread rest) in the clause.
	explicit := "(fun total (Number (spread Number)) Number)\n" +
		"(let total (a (spread rest)) = (+ a rest.size))\n"
	evalNum(t, explicit+"(total 5)", 5)
	evalNum(t, explicit+"(total 5 1 2 3)", 8)

	// Plain binder `rest` — the sig's (spread Number) drives collection.
	plain := "(fun total (Number (spread Number)) Number)\n" +
		"(let total (a rest) = (+ a rest.size))\n"
	evalNum(t, plain+"(total 5)", 5)
	evalNum(t, plain+"(total 5 1 2 3)", 8)

	// A pure-spread signature with a single plain binder (the target example
	// shape): `nums` receives the whole collected list, including the empty
	// call. (Uses .size — a bare-env List member — in place of the doc's
	// stdlib .fold, which this unit env doesn't load.)
	add := "(fun add ((spread Number)) Number)\n" +
		"(let add (nums) = nums.size)\n"
	evalNum(t, add+"(add)", 0)
	evalNum(t, add+"(add 1 2 3 4)", 4)
}

// No clause matches → runtime error (nil result + reported diagnostic).
func TestClauseNoMatchErrors(t *testing.T) {
	src := "(fun pick (Number) Number)\n" +
		"(let pick (0) = 1)\n" +
		"(pick 5)"
	v, diags := evalProgramDiag(t, src)
	if v.Kind != core.KindNil {
		t.Fatalf("no-match call: kind %q, want nil", v.Kind)
	}
	if len(diags) == 0 {
		t.Fatal("no-match call should report a diagnostic")
	}
}

// The retired forms steer to the new syntax.
// NOTE: these fixtures are INTENTIONALLY the retired forms — do not migrate
// them to `let` clauses; the test asserts they now error.
func TestRetiredImplForms(t *testing.T) {
	for _, src := range []string{
		"(= f (a) a)",                    // old `=` impl
		"(fun g (a) a)",                  // old combined fun impl
		"(fun h (a (or b 0)) Number)",    // (or …) param
		"(struct P x)\n(= P.m (self) 1)", // old method impl
	} {
		_, diags := evalProgramDiag(t, src)
		if len(diags) == 0 {
			t.Errorf("%q should be rejected with a pointer to the new form", src)
		}
	}
}

// §3 — the select match expression: first case wins, binders scoped to the
// arm, list patterns destructure, `do` results stop at the next case.
func TestSelectExpression(t *testing.T) {
	sel := "(fun add (Number Number) Number)\n" +
		"(let add (a b) =\n" +
		"    (select [a b]\n" +
		"        case [0 rhs] -> rhs\n" +
		"        case [lhs 0] -> lhs\n" +
		"        case [lhs rhs] -> (+ lhs rhs)\n" +
		"    )\n" +
		")\n"
	evalNum(t, sel+"(add 0 7)", 7)
	evalNum(t, sel+"(add 5 0)", 5)
	evalNum(t, sel+"(add 2 3)", 5)

	// do-result stops at the next case; literal + type-test patterns work.
	evalNum(t, "(select 5\n case 0 -> do 1 2\n case (Number n) -> do (let d = (* n 2)) d\n)", 10)
	evalNum(t, "(select 'x'\n case (Number n) -> n\n case s -> 9\n)", 9)
}

// select with no matching case errors.
func TestSelectNoMatch(t *testing.T) {
	v, diags := evalProgramDiag(t, "(select 5 case 0 -> 1)")
	if v.Kind != core.KindNil || len(diags) == 0 {
		t.Fatalf("unmatched select should error; kind=%q diags=%d", v.Kind, len(diags))
	}
}

// Value lets keep working untouched.
func TestValueLetUnaffected(t *testing.T) {
	evalNum(t, "(let x = 5)\nx", 5)
	evalNum(t, "(let var y = 1)\n(= y 7)\ny", 7)
	evalNum(t, "(let a = 1 b = 2)\n(+ a b)", 3)
}

// A `(var self)` method that rebinds its receiver to a whole new value
// (`(= self v)`, as opposed to an in-place field write) propagates that value
// back to the caller's binding — the `=`-suffix self-mutation contract
// (Effects.md). This must hold for VALUE receivers, not just reference types:
// the write-back targets the caller's lvalue, so a rebound number lands too.
// (evalInPackage, not evalProgram: type extensions on a primitive need a
// package to live in.)
func TestVarSelfWriteback(t *testing.T) {
	wantNum := func(src string, want float64) {
		t.Helper()
		v := evalInPackage(t, src, nil)
		if v.Kind != core.KindNum || v.Val.(float64) != want {
			t.Errorf("eval(%q) = %v (%s), want %v", src, v.Val, v.Kind, want)
		}
	}

	// Value-type receiver: a rebound Number reaches the caller's `var`.
	numBump := "(method Number.bump= ((var Self)) Number)\n" +
		"(let Number.bump= ((var self)) = (= self (+ self 1)))\n"
	wantNum(numBump+"(let var n = 5)\n(n.bump=)\n(n.bump=)\nn", 7)

	// Struct receiver, whole-value rebind (a fresh instance) propagates.
	counter := "(struct Counter n)\n" +
		"(method Counter.bump= ((var Self)) None)\n" +
		"(let Counter.bump= ((var self)) = (= self Counter.{ n = (+ self.n 1) }))\n"
	wantNum(counter+"(let var c = Counter.{ n = 0 })\n(c.bump=)\n(c.bump=)\nc.n", 2)

	// In-place field mutation still works (shares the pointer, no rebind) —
	// the write-back path must not disturb it.
	inplace := "(struct Counter n)\n" +
		"(method Counter.inc! ((var Self)) None)\n" +
		"(let Counter.inc! ((var self)) = (= self.n (+ self.n 10)))\n"
	wantNum(inplace+"(let var c = Counter.{ n = 5 })\n(c.inc!)\nc.n", 15)

	// Write-back reaches a LOCAL var receiver inside a fun body (this is how
	// stdlib List.concat grows its `joined` accumulator via append=).
	acc := numBump + "(fun add3 (Number) Number)\n" +
		"(let add3 (x) = do\n" +
		"    (let var a = x)\n" +
		"    (a.bump=)\n(a.bump=)\n(a.bump=)\n" +
		"    a)\n"
	wantNum(acc+"(add3 10)", 13)

	// A non-lvalue receiver (a literal) has no assignable home: the rebind is
	// discarded, not an error and not misapplied — the program runs on.
	wantNum(numBump+"(5.bump=)\n99", 99)
}
