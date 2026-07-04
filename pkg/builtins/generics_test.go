package builtins

import (
	"testing"

	"pho/pkg/core"
)

// Phase 1 generics runtime ERASURE: `template`, the `{}` generic struct, and a
// generic method all run with types erased. `template` binds each type
// parameter to the top type Unknown; a generic struct builds a plain struct
// with the declared fields (types dropped); a generic method whose receiver is
// a type parameter erases to a UNIVERSAL method, callable on any value.
func TestGenericsPhase1Runtime(t *testing.T) {
	num := func(src string, want float64) {
		t.Helper()
		var codes []string
		v := evalInPackage(t, src, func(c string) { codes = append(codes, c) })
		if len(codes) != 0 {
			t.Errorf("unexpected diagnostics %v for %q", codes, src)
		}
		if v.Kind != core.KindNum {
			t.Fatalf("eval(%q): got kind %q, want num", src, v.Kind)
		}
		if got := v.Val.(float64); got != want {
			t.Errorf("eval(%q) = %v, want %v", src, got, want)
		}
	}

	// A generic struct constructs and exposes its fields (types erased).
	num("(struct Box { U v })\n(let b = Box.{ v = 7 })\nb.v", 7)

	// A generic method on a type-parameter receiver is universal: it dispatches
	// on any value. `(x.bind fn)` runs `(fn x)` for a Number and a String alike;
	// the sig form is erased so it doesn't collide with the impl.
	num("(template I O)\n"+
		"(method I.bind (Self (fun (I) O)) O)\n"+
		"(let I.bind (self fn) = (fn self))\n"+
		"(5.bind (fun (n) (+ n 1)))", 6)
	num("(template I O)\n"+
		"(let I.bind (self fn) = (fn self))\n"+
		"('hi'.bind (fun (s) s.size))", 2)

	// The full template program (bounded param, private field, generic method
	// sig + impl) runs with no diagnostics.
	var codes []string
	evalInPackage(t, "(template U (Some-Type B))\n"+
		"(struct Container { U u B #b })\n"+
		"(template I O)\n"+
		"(method I.bind (Self (fun (I) O)) O)\n"+
		"(let I.bind (self fn) = (fn self))\n", func(c string) { codes = append(codes, c) })
	if len(codes) != 0 {
		t.Errorf("the full generic program should run clean; got diagnostics %v", codes)
	}
}
