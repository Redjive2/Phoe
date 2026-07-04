package builtins

import (
	"testing"

	"pho/pkg/core"
)

// The grouped typed-let form `(let [var] (Type name) = value)` binds the name to
// the value at runtime; the type is a static annotation, erased here. (The
// ungrouped `Type name = value` form was retired in favor of this one.)
func TestTypedLetRuntime(t *testing.T) {
	eq := func(src, want string) {
		t.Helper()
		if got := core.Stringify(evalProgram(t, src)); got != want {
			t.Errorf("%q = %q, want %q", src, got, want)
		}
	}
	eq("(let (Number x) = 5)\nx", "5")
	eq("(let var (String s) = 'hi')\n(= s 'bye')\ns", "bye")
	eq("(let (Number a) = 1  (String b) = 'z')\n[a b]", "[1 z]")
	eq("(let ((Or Number String) id) = 42)\nid", "42") // compound type annotation
	eq("(let x = 9)\nx", "9")                          // untyped still works
}
