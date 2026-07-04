package lint

import "testing"

// enableEffectCheck turns the gated Phase-3 effect diagnostics on for one test.
func enableEffectCheck(t *testing.T) {
	t.Helper()
	EffectCheck = true
	t.Cleanup(func() { EffectCheck = false })
}

const counter = "(struct Counter n)\n"

// A method that mutates self but whose name lacks '=' is flagged missing-equals
// (self-mutation is a '=' effect, not '!'; the SIGNATURE's `(var Self)` receiver
// makes the mutation itself legal).
func TestEffectMissingBangOnSelfMutation(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, counter+"(method Counter.Bump ((var Self) Number) None)\n(let Counter.Bump (self by) = (= self.n (+ self.n by)))\n")
	if !hasDiag(d, "missing-equals") {
		t.Fatalf("expected missing-equals on Bump, got %#v", d)
	}
	if hasDiag(d, "missing-bang") {
		t.Fatalf("self-mutation is a '=' effect, not '!' — got a missing-bang: %#v", d)
	}
	if hasDiag(d, "effect-through-readonly") {
		t.Fatalf("(var Self) receiver must not trip effect-through-readonly, got %#v", d)
	}
}

// A function that calls a '!' function must itself end in '!'.
func TestEffectMissingBangViaCall(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, "(let grow! () = 1)\n(let doit () = (grow!))\n")
	if !hasDiagWithName(d, "missing-bang", "doit") {
		t.Fatalf("expected missing-bang on doit (calls grow!), got %#v", d)
	}
}

// Mutating self through a read-only receiver (the SIGNATURE declares a plain
// `Self`) is effect-through-readonly.
func TestEffectThroughReadonlyReceiver(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, counter+"(method Counter.Bump! (Self Number) None)\n(let Counter.Bump! (self by) = (= self.n (+ self.n by)))\n")
	if !hasDiag(d, "effect-through-readonly") {
		t.Fatalf("expected effect-through-readonly (bare self mutated), got %#v", d)
	}
	// The '!' is present, so missing-bang must NOT also fire.
	if hasDiag(d, "missing-bang") {
		t.Fatalf("Bump! has '!' — missing-bang should not fire, got %#v", d)
	}
}

// The correct shape — a self-mutating method named with '=' whose SIGNATURE
// declares a `(var Self)` receiver — is fully clean.
func TestEffectCleanMutatingMethod(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, counter+"(method Counter.Bump= ((var Self) Number) None)\n(let Counter.Bump= (self by) = (= self.n (+ self.n by)))\n")
	if hasDiag(d, "missing-bang") || hasDiag(d, "effect-through-readonly") ||
		hasDiag(d, "missing-equals") || hasDiag(d, "spurious-bang") {
		t.Fatalf("Bump= ((var Self) …) must be clean, got %#v", d)
	}
}

// A pure function draws no effect diagnostics; a '!' fn calling a '!' fn is fine.
func TestEffectCleanCases(t *testing.T) {
	enableEffectCheck(t)
	pure := analyze(t, "(let add (x y) = (+ x y))\n")
	if hasDiag(pure, "missing-bang") {
		t.Fatalf("pure add must not be flagged, got %#v", pure)
	}
	chain := analyze(t, "(let grow! () = 1)\n(let doit! () = (grow!))\n")
	if hasDiag(chain, "missing-bang") {
		t.Fatalf("doit! calling grow! is correctly marked, got %#v", chain)
	}
}

// With the gate OFF (the default), effect diagnostics never fire — the stdlib
// isn't `!`-migrated yet.
func TestEffectGatedOff(t *testing.T) {
	// EffectCheck deliberately left at its false default.
	d := analyze(t, counter+"(let Counter.Bump (self by) = (= self.n by))\n")
	if hasDiag(d, "missing-bang") || hasDiag(d, "effect-through-readonly") {
		t.Fatalf("effect diagnostics must be gated off by default, got %#v", d)
	}
}
