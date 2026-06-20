package builtins

import "testing"

func TestPropertyAndAnonymousMethods(t *testing.T) {
	// Free-standing property: get/set delegate to anonymous funs over `backing`.
	free := "(var backing 10)\n" +
		"(property Counter\n  get (fun () backing)\n  set (fun (v) (= backing v)))\n" +
		"(var a Counter)\n(= Counter 42)\n(var b Counter)\n(+ a b)"
	if got := evalProgram(t, free).Val; got != float64(52) {
		t.Fatalf("free-standing property: got %v, want 52", got)
	}

	// Struct-field property: get/set are anonymous METHODS; self.x is private.
	field := "(struct P x)\n" +
		"(property P.Doubled\n  get (method P (self) (* self.x 2))\n  set (method P (self v) (= self.x (/ v 2))))\n" +
		"(var p P.{ x 5 })\n(var a p.Doubled)\n(= p.Doubled 30)\n(var b p.Doubled)\n(+ a b)"
	if got := evalProgram(t, field).Val; got != float64(40) {
		t.Fatalf("struct-field property: got %v, want 40", got)
	}
}
