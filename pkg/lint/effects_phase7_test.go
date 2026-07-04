package lint

import "testing"

// An effectful predicate carries both suffixes as `name?!`: the trailing '!'
// still marks it effectful, so a `?`-only predicate that calls a `!`-function is
// flagged missing-bang while the `?!` form is clean.
func TestEffectPredicateBang(t *testing.T) {
	enableEffectCheck(t)
	sink := "(fun sink! (Number) None)\n(let sink! (n) = none)\n"
	bad := analyze(t, sink+"(fun ready? (Number) Boolean)\n(let ready? (n) = (sink! n))\n")
	if !hasDiagWithName(bad, "missing-bang", "ready?") {
		t.Fatalf("ready? calls a `!` fn but lacks '!' — missing-bang expected, got %#v", bad)
	}
	good := analyze(t, sink+"(fun ready?! (Number) Boolean)\n(let ready?! (n) = (sink! n))\n")
	if hasDiag(good, "missing-bang") {
		t.Fatalf("ready?! correctly marks its effect — clean, got %#v", good)
	}
}

// A property getter is auto-invoked on read, so an effect inside it (calling a
// `!`-function) is flagged effect-in-pure-context.
func TestPureContextGetterEffect(t *testing.T) {
	enableEffectCheck(t)
	callsBang := analyze(t, "(fun boom! () Number)\n(let boom! () = 1)\n(struct Box #v)\n"+
		"(property Box.peek (get (self) (boom!)))\n")
	if !hasDiag(callsBang, "effect-in-pure-context") {
		t.Fatalf("a getter calling a `!` function must be flagged, got %#v", callsBang)
	}
}

// A pure getter (just reads a field / constructs a value) is clean.
func TestPureContextGetterClean(t *testing.T) {
	enableEffectCheck(t)
	clean := analyze(t, "(struct Box #v)\n"+
		"(property Box.peek (get (self) self.#v))\n")
	if hasDiag(clean, "effect-in-pure-context") {
		t.Fatalf("a field-reading getter must be clean, got %#v", clean)
	}
}

// The receiver's mutability is declared in the SIGNATURE's `(var Self)`, not the
// clause — a clause always names its receiver plainly (`self`), never
// `(var self)`. So a mutating method declares `(var Self)` in its SIGNATURE only,
// and there is no impl-vs-sig receiver mismatch to check: the sig decides.
func TestReceiverMutabilityFromSignature(t *testing.T) {
	enableEffectCheck(t)

	// SIG declares `(var Self)`; the clause uses a plain `self` and mutates it —
	// clean (the `(var self)` is NOT repeated in the clause).
	ok := analyze(t, "(struct L n)\n"+
		"(method L.reset= ((var Self)) None)\n"+
		"(let L.reset= (self) = (= self.n 0))\n")
	if hasDiag(ok, "effect-through-readonly") {
		t.Fatalf("a (var Self) sig makes a plain-self mutating clause clean, got %#v", ok)
	}

	// SIG declares a read-only `Self`; a clause that mutates `self` is rejected —
	// the SIGNATURE, not the clause, decides receiver mutability.
	ro := analyze(t, "(struct L n)\n"+
		"(method L.reset (Self) None)\n"+
		"(let L.reset (self) = (= self.n 0))\n")
	if !hasDiag(ro, "effect-through-readonly") {
		t.Fatalf("a read-only sig must reject self-mutation in the clause, got %#v", ro)
	}
}
