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
	base := "(struct Point.{ X Number Y Number })\n(static method Point.At (x y) Self.{ X x Y y })\n"
	staticEq(t, base+"(const p (Point.At 1 2)) p.X", "1")
	staticEq(t, base+"(const p (Point.At 7 9)) p.Y", "9")
}

func TestStaticProperty(t *testing.T) {
	src := "(struct Counter.{ N Number })\n" +
		"(static property Counter.Zero get (method Counter (Self) Self.{ N 0 }))\n"
	staticEq(t, src+"Counter.Zero.N", "0")
}

func TestStaticMethodNotOnInstance(t *testing.T) {
	// A static member is on the TYPE, not an instance — reaching it through an
	// instance is an error, not a silent hit.
	_, diags := evalProgramDiag(t, "(struct Point.{ X Number })\n"+
		"(static method Point.Origin () Self.{ X 0 })\n"+
		"(const p Point.{ X 5 })\n(p.Origin)")
	if len(diags) == 0 {
		t.Fatalf("expected an error reaching a static member through an instance")
	}
}
