package builtins

import (
	"testing"

	"pho/pkg/core"
)

// A Trait is a structural, implicit interface: a value's type satisfies it by
// providing the required members — no `implements` anywhere. A property
// requirement is met by a FIELD of the same name.
func TestTraitMembership(t *testing.T) {
	greeter := "(type Greeter (Trait ()\n  (method self.hi (self))\n  (method self.bye (self))))\n"
	wantBool(t, greeter+"(struct P x)\n(let P.hi (self) = 1)\n(let P.bye (self) = 2)\n(let p = P.{ x = 1 })\n(p.is? Greeter)", true)
	wantBool(t, greeter+"(struct Q x)\n(let Q.hi (self) = 1)\n(let q = Q.{ x = 1 })\n(q.is? Greeter)", false)

	// A property requirement satisfied by a field of the same name.
	hasName := "(type Has-Name (Trait () (property self.name get)))\n"
	wantBool(t, hasName+"(struct R name)\n(let r = R.{ name = 'x' })\n(r.is? Has-Name)", true)
	wantBool(t, hasName+"(struct S x)\n(let s = S.{ x = 1 })\n(s.is? Has-Name)", false)

	// A mutable-property requirement (get set) is also met by a field.
	mut := "(type Mut (Trait () (property self.v get set)))\n"
	wantBool(t, mut+"(struct B v)\n(let b = B.{ v = 1 })\n(b.is? Mut)", true)

	// extends: a sub-trait inherits the supertrait's requirements.
	ext := "(type Drawable (Trait () (method self.draw (self))))\n" +
		"(type Shape (Trait (Drawable) (method self.area (self))))\n"
	wantBool(t, ext+"(struct C x)\n(let C.draw (self) = 1)\n(let C.area (self) = 2)\n(let c = C.{ x = 1 })\n(c.is? Shape)", true)
	wantBool(t, ext+"(struct D x)\n(let D.area (self) = 2)\n(let d = D.{ x = 1 })\n(d.is? Shape)", false) // missing Draw
}

// A default implementation is auto-injected on a value that satisfies the trait
// but doesn't define the member itself; an own member wins; defaults take args.
func TestTraitDefaults(t *testing.T) {
	wantStr(t, "(type Greet (Trait () (let self.hi (self) = 'hello')))\n(struct P x)\n(let p = P.{ x = 1 })\n(p.hi)", "hello")
	wantStr(t, "(type Greet (Trait () (let self.hi (self) = 'def')))\n(struct Q x)\n(let Q.hi (self) = 'own')\n(let q = Q.{ x = 1 })\n(q.hi)", "own")
	wantBool(t, "(type Add (Trait () (let self.inc (self n) = (+ n 1))))\n(struct R x)\n(let r = R.{ x = 1 })\n(== (r.inc 41) 42)", true)

	// A property getter default is auto-injected and called immediately.
	wantBool(t, "(type Zero (Trait () (property self.z get (method self (self) 0))))\n(struct S x)\n(let s = S.{ x = 1 })\n(== s.z 0)", true)

	// A value that does NOT satisfy the trait's other requirements gets no default.
	if _, codes := evalProgramDiag(t, "(type Two (Trait () (method self.A (self)) (let self.B (self) = 'd')))\n(struct T x)\n(let t = T.{ x = 1 })\n(t.B)"); !hasCode(codes, core.ErrField) {
		t.Errorf("T lacks A ⇒ doesn't satisfy Two ⇒ B default must NOT inject; got %v", codes)
	}

	// Two satisfied traits defaulting the same member ⇒ ambiguity error.
	amb := "(type A (Trait () (let self.m (self) = 1)))\n(type B (Trait () (let self.m (self) = 2)))\n" +
		"(struct U x)\n(let u = U.{ x = 1 })\n"
	if _, codes := evalProgramDiag(t, amb+"(u.m)"); !hasCode(codes, core.ErrField) {
		t.Errorf("two traits default M and U satisfies both ⇒ ambiguous; got %v", codes)
	}
}
