package lint

import "testing"

// The '=' suffix separates SELF/value mutation from environmental ('!') effects.
// A `=`-method call is classified by its RECEIVER, exactly like an assignment:
// mutating a local is contained, self propagates a '=', a module var propagates
// a '!'.
func TestEffectSelfMutationSuffix(t *testing.T) {
	enableEffectCheck(t)
	appendM := "(let List.append= ((var self) e) = (= self (self.append e)))\n"

	// Calling append= on a LOCAL var is contained — the caller stays pure.
	concat := appendM + "(let concat (a b) = do\n  (let var result = a)\n  (foreach x in b do (result.append= x))\n  result)\n"
	if d := analyze(t, concat); hasDiag(d, "missing-bang") || hasDiag(d, "missing-equals") {
		t.Fatalf("append= on a local is contained — concat should be pure, got %#v", d)
	}

	// append= itself mutates (var self) → needs '=' (has it → clean).
	if d := analyze(t, appendM); hasDiag(d, "missing-equals") || hasDiag(d, "spurious-equals") || hasDiag(d, "spurious-bang") {
		t.Fatalf("append= is a clean self-mutator, got %#v", d)
	}

	// Calling append= on SELF propagates a self-mutation → the caller needs '='.
	self := appendM + "(let List.push ((var self) e) = (self.append= e))\n"
	if !hasDiagWithName(analyze(t, self), "missing-equals", "push") {
		t.Fatalf("push mutates self via append= — missing-equals expected")
	}
	if d := analyze(t, appendM+"(let List.push= ((var self) e) = (self.append= e))\n"); hasDiag(d, "missing-equals") || hasDiag(d, "spurious-equals") {
		t.Fatalf("push= is a clean self-mutator, got %#v", d)
	}

	// Calling append= on a MODULE var is environmental → the caller needs '!'.
	free := appendM + "(let var registry = [])\n(let record (x) = (registry.append= x))\n"
	if !hasDiagWithName(analyze(t, free), "missing-bang", "record") {
		t.Fatalf("mutating a module var via append= is environmental — missing-bang expected")
	}
}

// A '=' on a name that mutates nothing it was given is spurious.
func TestEffectSpuriousEquals(t *testing.T) {
	enableEffectCheck(t)
	if !hasDiagWithName(analyze(t, "(let pure-fn= (x) = (+ x 1))\n"), "spurious-equals", "pure-fn=") {
		t.Fatalf("pure-fn= has no self/value mutation — spurious-equals expected")
	}
}
