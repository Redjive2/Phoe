package lint

import "testing"

// Linter support for pattern matching in `let` assignment (P3): destructuring
// targets, grouped `(Type name)` typed bindings, `(var …)` binders, and the
// struct-field `()` capture operator all resolve their binders into scope; the
// retired ungrouped `Type name = value` form is rejected.
func TestLetPatternLint(t *testing.T) {
	clean := func(name, src string) {
		t.Run(name, func(t *testing.T) {
			d := analyze(t, src)
			for _, code := range []string{"unresolved-identifier", "bad-form-shape", "bad-form-arity"} {
				if hasDiag(d, code) {
					t.Errorf("expected no %s, got %#v", code, d)
				}
			}
		})
	}

	// List destructure — every binder resolves.
	clean("list destructure", "(let [a b c] = [1 2 3])\n(+ a (+ b c))")
	// Grouped typed binding — no bad-form-shape; name resolves.
	clean("grouped typed", "(let (Number n) = 5)\n(+ n 1)")
	// Compound grouped type.
	clean("compound grouped type", "(let ((Or Number String) id) = 5)\nid")
	// Nested + per-element var.
	clean("nested + var element", "(let [(var x) [y z]] = [1 [2 3]])\n(= x 9)\n(+ x (+ y z))")
	// Top-level var over a destructure.
	clean("top-level var destructure", "(let var [p q] = [1 2])\n(= p 9)\n(+ p q)")
	// Struct-field capture: both the capture and the destructured names resolve.
	clean("struct capture", "(struct Bag items)\n(let Bag.{ (items) = [p q] } = Bag.{ items = [1 2] })\n(+ p (+ q items.size))")

	// The retired ungrouped `Type name = value` form is rejected with a pointer
	// to the grouped replacement.
	t.Run("ungrouped rejected", func(t *testing.T) {
		d := analyze(t, "(let Number x = 5)\nx")
		if !hasDiag(d, "bad-form-shape") {
			t.Errorf("ungrouped 'Type name = value' should be rejected, got %#v", d)
		}
	})
}
