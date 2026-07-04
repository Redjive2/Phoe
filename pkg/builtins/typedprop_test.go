package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Typed properties `(property (Type name) …)` and typed struct fields in
// `Type name` order: the declared type is erased at runtime, so binding,
// construction, and dispatch behave exactly as the untyped forms.
func TestTypedPropertyAndFieldRuntime(t *testing.T) {
	eq := func(src, want string) {
		t.Helper()
		if got := core.Stringify(evalProgram(t, src)); got != want {
			t.Errorf("%s\n = %q, want %q", src, got, want)
		}
	}
	// free-standing typed property
	eq("(let n = 5)\n(property (Number twice) (get () (* n 2)))\ntwice", "10")
	// typed struct fields (Type name) + attached typed property
	eq("(struct Box.{ Number n })\n(property (Number Box.area) (get (self) (* self.n self.n)))\n(let b = Box.{ n = 4 })\nb.area", "16")
	// typed property with a setter
	eq("(let var n = 0)\n(property (Number prop) (get () n) (set (v) (= n v)))\n(= prop 7)\nprop", "7")
	// untyped property and field still work
	eq("(let n = 3)\n(property twice (get () (* n 2)))\ntwice", "6")
	eq("(struct P.{ Number a String b })\n(let p = P.{ a = 1 b = 'hi' })\np.b", "hi")
}
