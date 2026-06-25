package builtins

import (
	"testing"

	"pho/pkg/core"
)

// `static method`/`static property` declare TYPE-level members reached through
// the struct's type value (Point.At), not an instance. In a static method's
// body `Self` is the receiver type, so `Self.{ … }` constructs an instance.

func staticEq(t *testing.T, src, want string) {
	t.Helper()
	if got := core.Stringify(evalProgram(t, src)); got != want {
		t.Fatalf("%s\n = %q, want %q", src, got, want)
	}
}

func TestStaticMethod(t *testing.T) {
	base := "(struct Point.{ x Number y Number })\n(static method Point.at (x y) self.{ x x y y })\n"
	staticEq(t, base+"(let p = (Point.at 1 2)) p.x", "1")
	staticEq(t, base+"(let p = (Point.at 7 9)) p.y", "9")
}

func TestStaticProperty(t *testing.T) {
	src := "(struct Counter.{ n Number })\n" +
		"(static property Counter.zero get (method Counter (self) self.{ n 0 }))\n"
	staticEq(t, src+"Counter.Zero.N", "0")
}

func TestStaticMethodNotOnInstance(t *testing.T) {
	// A static member is on the TYPE, not an instance — reaching it through an
	// instance is an error, not a silent hit.
	_, diags := evalProgramDiag(t, "(struct Point.{ x Number })\n"+
		"(static method Point.origin () self.{ x 0 })\n"+
		"(let p = Point.{ x 5 })\n(p.origin)")
	if len(diags) == 0 {
		t.Fatalf("expected an error reaching a static member through an instance")
	}
}
