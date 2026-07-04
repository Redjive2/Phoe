package lint

import (
	"strings"
	"testing"
)

// Effects are inferred over the UNION of a callable's clauses and reported on
// its SIGNATURE — not per clause. This pins the three linked properties:
//   - a pure clause of an otherwise-effectful callable draws NO spurious-bang
//     (the multi-clause `print-vertical!` bug: one clause `= None`, one calls a
//     `!`-function — together the callable is environmental);
//   - the single contract diagnostic lands on the SIGNATURE line, not the impl;
//   - the message names the SPECIFIC effect (fine-grained), not a coarse bucket.
func TestEffectAggregatedOnSignature(t *testing.T) {
	enableEffectCheck(t)

	// Clause 1 is effect-free; clause 2 calls a `!`-function. The `!` on `pv!` is
	// justified by the union, so the pure clause must not draw spurious-bang.
	multi := "(fun pv! ((spread Unknown)) None)\n" +
		"(let pv! ([1 2 3]) = None)\n" +
		"(let pv! (values) = (foreach v in values (print-line! v)))\n"
	if d := analyze(t, multi); hasDiag(d, "spurious-bang") {
		t.Fatalf("a pure clause of an effectful callable must not draw spurious-bang, got %#v", d)
	}

	// missing-bang anchors on the SIGNATURE (line 1), not the clause (line 2),
	// and names the specific effect it inherits (the called `!`-function).
	src := "(fun leak (Unknown) None)\n(let leak (x) = (print-line! x))\n"
	var found bool
	for _, d := range analyze(t, src) {
		if d.Code != "missing-bang" {
			continue
		}
		found = true
		if d.Span.StartLine != 1 {
			t.Errorf("missing-bang should anchor on the sig (line 1), got line %d", d.Span.StartLine)
		}
		if !strings.Contains(d.Message, "print-line!") {
			t.Errorf("message should name the effect 'print-line!', got %q", d.Message)
		}
	}
	if !found {
		t.Fatalf("expected a missing-bang for 'leak'")
	}

	// The effect is named for the specific `!`-function called (freeform), not a
	// coarse bucket.
	prim := "(fun other! (Unknown) None)\n(let other! (n) = none)\n" +
		"(fun w (Unknown) None)\n(let w (n) = (other! n))\n"
	var msg string
	for _, d := range analyze(t, prim) {
		if d.Code == "missing-bang" && strings.Contains(d.Message, "'w'") {
			msg = d.Message
		}
	}
	if !strings.Contains(msg, "other!") {
		t.Errorf("effect should be named 'other!', got %q", msg)
	}
}

// A clause's `where` guard runs during dispatch, so it must be pure: an
// effectful guard (a `!` call, io, a module write) is flagged guard-effect.
func TestGuardMustBePure(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, "(let noisy! () = 1)\n(let f (a b) where (== (noisy!) 0) = a)\n(let f (a b) = b)\n")
	if !hasDiag(d, "guard-effect") {
		t.Fatalf("an effectful guard must draw guard-effect, got %#v", d)
	}
	clean := analyze(t, "(let f (a b) where (== b 0) = a)\n(let f (a b) = b)\n")
	if hasDiag(clean, "guard-effect") {
		t.Fatalf("a pure guard must not draw guard-effect, got %#v", clean)
	}
}

// Clause PATTERN binders shadow module vars for effect classification: a
// destructured binder named like a module var is a locally-owned (pure) write
// target, not a mutates-free effect.
func TestClausePatternBindersShadowModuleVars(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, "(let var counter = 0)\n(let f ([counter rest]) = (= counter 5))\n")
	if hasDiag(d, "missing-bang") {
		t.Fatalf("a pattern binder shadowing a module var is a pure local write, got %#v", d)
	}
	// The module var written through a NON-shadowing clause still counts.
	d = analyze(t, "(let var counter = 0)\n(let g (x) = (= counter x))\n")
	if !hasDiagWithName(d, "missing-bang", "g") {
		t.Fatalf("writing the module var from a clause is mutates-free, got %#v", d)
	}
}

// A `(Type name)` pattern's binder shadows too.
func TestClauseTypePatternBinderShadows(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, "(let var total = 0)\n(let h ((Number total)) = (= total 1))\n")
	if hasDiag(d, "missing-bang") {
		t.Fatalf("(Number total) binds total locally — pure write, got %#v", d)
	}
}
