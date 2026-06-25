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
	circle := "(struct Circle.{ R Number })\n(method Circle.Draw (self) self.R)\n"
	eq(circle+"(trait Drawable (method Self.Draw (self)))\n(Circle.{ R 5 }.Is? Drawable)", "True")
	eq("(struct Square.{ S Number })\n(trait Drawable (method Self.Draw (self)))\n(Square.{ S 1 }.Is? Drawable)", "False")
	eq(circle+"(trait Drawable () (method Self.Draw (self)))\n(Circle.{ R 5 }.Is? Drawable)", "True") // empty extends
	eq("(struct P.{ X Number })\n(trait Memo (static method Self.Calc (Self) P) (static property Self.Cached get))\nTrue", "True")
}
