package builtins

import (
	"testing"

	"pho/pkg/core"
)

// The named `(trait Name …)` form builds + binds a trait in one step (the
// `(type Name (Trait …))` shorthand), with optional `()` extends and parse-only
// static members.
func TestNamedTraitRuntime(t *testing.T) {
	eq := func(src, want string) {
		t.Helper()
		if got := core.Stringify(evalProgram(t, src)); got != want {
			t.Errorf("%s\n = %q, want %q", src, got, want)
		}
	}
	circle := "(struct Circle.{ r Number })\n(method Circle.draw (self) self.r)\n"
	eq(circle+"(trait Drawable (method self.draw (self)))\n(Circle.{ r 5 }.is? Drawable)", "True")
	eq("(struct Square.{ s Number })\n(trait Drawable (method self.draw (self)))\n(Square.{ s 1 }.is? Drawable)", "False")
	eq(circle+"(trait Drawable () (method self.draw (self)))\n(Circle.{ r 5 }.is? Drawable)", "True") // empty extends
	eq("(struct P.{ x Number })\n(trait Memo (static method self.calc (self) P) (static property self.cached get))\ntrue", "True")
}
