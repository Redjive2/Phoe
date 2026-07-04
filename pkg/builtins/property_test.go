package builtins

import "testing"

func TestPropertyAndAnonymousMethods(t *testing.T) {
	// Free-standing property: get/set are (get …)/(set …) accessors over `backing`.
	free := "(let var backing = 10)\n" +
		"(property counter\n  (get () backing)\n  (set (v) (= backing v)))\n" +
		"(let var a = counter)\n(= counter 42)\n(let var b = counter)\n(+ a b)"
	if got := evalProgram(t, free).Val; got != float64(52) {
		t.Fatalf("free-standing property: got %v, want 52", got)
	}

	// Struct-field property: get/set accessors are self-methods; self.x is private.
	field := "(struct P #x)\n" +
		"(property P.doubled\n  (get (self) (* self.#x 2))\n  (set (self v) (= self.#x (/ v 2))))\n" +
		"(let var p = P.{ #x = 5 })\n(let var a = p.doubled)\n(= p.doubled 30)\n(let var b = p.doubled)\n(+ a b)"
	if got := evalProgram(t, field).Val; got != float64(40) {
		t.Fatalf("struct-field property: got %v, want 40", got)
	}
}
