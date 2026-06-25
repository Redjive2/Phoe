package builtins

import (
	"testing"

	"pho/pkg/core"
)

// A Trait is a structural, implicit interface: a value's type satisfies it by
// providing the required members — no `implements` anywhere. A property
// requirement is met by a FIELD of the same name.
func TestTraitMembership(t *testing.T) {
	greeter := "(type Greeter (Trait ()\n  (method Self.Hi (self))\n  (method Self.Bye (self))))\n"
	wantBool(t, greeter+"(struct P X)\n(method P.Hi (self) 1)\n(method P.Bye (self) 2)\n(const p P.{ X 1 })\n(p.Is? Greeter)", true)
	wantBool(t, greeter+"(struct Q X)\n(method Q.Hi (self) 1)\n(const q Q.{ X 1 })\n(q.Is? Greeter)", false)

	// A property requirement satisfied by a field of the same name.
	hasName := "(type HasName (Trait () (property Self.Name get)))\n"
	wantBool(t, hasName+"(struct R Name)\n(const r R.{ Name 'x' })\n(r.Is? HasName)", true)
	wantBool(t, hasName+"(struct S X)\n(const s S.{ X 1 })\n(s.Is? HasName)", false)

	// A mutable-property requirement (get set) is also met by a field.
	mut := "(type Mut (Trait () (property Self.V get set)))\n"
	wantBool(t, mut+"(struct B V)\n(const b B.{ V 1 })\n(b.Is? Mut)", true)

	// extends: a sub-trait inherits the supertrait's requirements.
	ext := "(type Drawable (Trait () (method Self.Draw (self))))\n" +
		"(type Shape (Trait (Drawable) (method Self.Area (self))))\n"
	wantBool(t, ext+"(struct C X)\n(method C.Draw (self) 1)\n(method C.Area (self) 2)\n(const c C.{ X 1 })\n(c.Is? Shape)", true)
	wantBool(t, ext+"(struct D X)\n(method D.Area (self) 2)\n(const d D.{ X 1 })\n(d.Is? Shape)", false) // missing Draw
}

// A default implementation is auto-injected on a value that satisfies the trait
// but doesn't define the member itself; an own member wins; defaults take args.
func TestTraitDefaults(t *testing.T) {
	wantStr(t, "(type Greet (Trait () (method Self.Hi (self) 'hello')))\n(struct P X)\n(const p P.{ X 1 })\n(p.Hi)", "hello")
	wantStr(t, "(type Greet (Trait () (method Self.Hi (self) 'def')))\n(struct Q X)\n(method Q.Hi (self) 'own')\n(const q Q.{ X 1 })\n(q.Hi)", "own")
	wantBool(t, "(type Add (Trait () (method Self.Inc (self n) (+ n 1))))\n(struct R X)\n(const r R.{ X 1 })\n(== (r.Inc 41) 42)", true)

	// A property getter default is auto-injected and called immediately.
	wantBool(t, "(type Zero (Trait () (property Self.Z get (method Self (self) 0))))\n(struct S X)\n(const s S.{ X 1 })\n(== s.Z 0)", true)

	// A value that does NOT satisfy the trait's other requirements gets no default.
	if _, codes := evalProgramDiag(t, "(type Two (Trait () (method Self.A (self)) (method Self.B (self) 'd')))\n(struct T X)\n(const t T.{ X 1 })\n(t.B)"); !hasCode(codes, core.ErrField) {
		t.Errorf("T lacks A ⇒ doesn't satisfy Two ⇒ B default must NOT inject; got %v", codes)
	}

	// Two satisfied traits defaulting the same member ⇒ ambiguity error.
	amb := "(type A (Trait () (method Self.M (self) 1)))\n(type B (Trait () (method Self.M (self) 2)))\n" +
		"(struct U X)\n(const u U.{ X 1 })\n"
	if _, codes := evalProgramDiag(t, amb+"(u.M)"); !hasCode(codes, core.ErrField) {
		t.Errorf("two traits default M and U satisfies both ⇒ ambiguous; got %v", codes)
	}
}
