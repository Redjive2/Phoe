package lint

import "testing"

// Operator overloading — Features.md §7. The linter treats `(operator Recv.OP
// (Self …) Ret)` as a method-shaped signature: the keyword resolves, `Self`
// binds, and the adjacent `(let Recv.OP …)` clauses attach to it.

// A well-formed symbol-operator overload draws no diagnostics — no unresolved
// `operator`/`Self`, and the clause finds its signature.
func TestOperatorOverloadClean(t *testing.T) {
	src := "(struct My-Num v)\n" +
		"(operator My-Num.+ (Self Number) My-Num)\n" +
		"(let My-Num.+ (self other) = My-Num.{ v = (+ self.v other) })\n"
	d := analyze(t, src)
	for _, bad := range []string{"unresolved-identifier", "signature-required", "phl-side-effect"} {
		if hasDiag(d, bad) {
			t.Fatalf("clean operator overload drew %s: %#v", bad, d)
		}
	}
}

// The index operators `[]` (read) and `[]=` (write) lint cleanly too, including
// the `(var Self)` receiver on the write form.
func TestOperatorIndexClean(t *testing.T) {
	src := "(struct Box items)\n" +
		"(operator Box.[] (Self Number) Number)\n" +
		"(let Box.[] (self i) = self.items.[i])\n" +
		"(operator Box.[]= ((var Self) Number Number) Number)\n" +
		"(let Box.[]= (self i v) = (= self.items.[i] v))\n"
	d := analyze(t, src)
	for _, bad := range []string{"unresolved-identifier", "signature-required", "var-self-needs-equals"} {
		if hasDiag(d, bad) {
			t.Fatalf("clean index operators drew %s: %#v", bad, d)
		}
	}
}

// An operator IMPLEMENTATION with no adjacent signature is flagged, exactly like
// a method clause — the `operator` sig is required.
func TestOperatorImplNeedsSig(t *testing.T) {
	d := analyze(t, "(struct My-Num v)\n(let My-Num.+ (self other) = self)\n")
	if !hasDiag(d, "signature-required") {
		t.Fatalf("an operator impl without a sig should draw signature-required, got %#v", d)
	}
}
