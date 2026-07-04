package lint

import "testing"

// A type parameter bounded by a STRUCT is checked against that bound (a value of
// the wrong shape fires), and a value typed as the bounded parameter can use the
// bound's FIELD members — `b.field` types as the field's declared type. (This
// relies on template bounds resolving after struct records exist.)
func TestStructBoundAndMemberAccess(t *testing.T) {
	base := "(template (Shape B))\n(struct Shape.{ Number k })\n(fun f (B) None)\n"
	gStr := "(fun g (String) None)\n(let g (s) = None)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = None)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// Bound member access: b.k resolves to Number via the bound Shape.
		{"bound field access typed via bound (vs String)", base + gStr + "(let f (b) = (g b.k))", true},
		{"bound field access typed via bound (vs Number)", base + gNum + "(let f (b) = (g b.k))", false},
		// Struct-bound enforcement at a call site.
		{"struct bound violated by a non-Shape arg", base + "(let use () = (f 'x'))", true},
		{"struct bound satisfied by a Shape value", base + "(let s = Shape.{ k = 1 })\n(let use () = (f s))", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}

// A method call on a struct-bounded type-variable value resolves through the
// bound struct's method table, so `(b.method …)` carries the method's declared
// result type downstream.
func TestBoundMethodAccess(t *testing.T) {
	base := "(template (Shape B))\n(struct Shape.{ Number k })\n" +
		"(method Shape.area (Self) Number)\n(let Shape.area (self) = self.k)\n(fun f (B) None)\n"
	gStr := "(fun g (String) None)\n(let g (s) = None)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = None)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"bound method result (Number) vs String", base + gStr + "(let f (b) = (g (b.area)))", true},
		{"bound method result (Number) vs Number", base + gNum + "(let f (b) = (g (b.area)))", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}

// A type parameter bounded by a TRAIT (the idiomatic generic constraint)
// resolves the trait's required methods on a value of the parameter type — the
// method's declared result type carries downstream.
func TestTraitBoundMethodAccess(t *testing.T) {
	base := "(trait Drawable (method self.area (self) Number))\n(template (Drawable B))\n(fun f (B) None)\n"
	gStr := "(fun g (String) None)\n(let g (s) = None)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = None)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"trait method result (Number) vs String", base + gStr + "(let f (b) = (g (b.area)))", true},
		{"trait method result (Number) vs Number", base + gNum + "(let f (b) = (g (b.area)))", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}

// Inside a polymorphic body, a member access on a bounded type-variable value
// is checked against the bound: a member the bound provides (or a universal one)
// is fine, but a member the bound does NOT provide is a typo — flagged. Sound
// because the value is only known to be a subtype of the bound. Fires only when
// the bound is fully enumerable (a local struct / a trait).
func TestBoundBadMemberDetection(t *testing.T) {
	structB := "(struct Shape.{ Number k })\n(method Shape.area (Self) Number)\n(let Shape.area (self) = 5)\n(template (Shape B))\n(fun f (B) None)\n"
	traitB := "(trait Drawable (method self.area (self) Number))\n(template (Drawable B))\n(fun f (B) None)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"struct bound: a real field is fine", structB + "(let f (b) = b.k)", false},
		{"struct bound: a real method is fine", structB + "(let f (b) = (b.area))", false},
		{"struct bound: a universal member is fine", structB + "(let f (b) = (b.is? Shape))", false},
		{"struct bound: a bad member is flagged", structB + "(let f (b) = b.nope)", true},
		{"trait bound: a real method is fine", traitB + "(let f (b) = (b.area))", false},
		{"trait bound: a bad member is flagged", traitB + "(let f (b) = b.nope)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "unknown-member"); got != c.wantErr {
				t.Errorf("unknown-member=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}

// A trait's required PROPERTY carries its declared type, so a bound-trait
// property access `b.name` types as that property's type.
func TestTraitBoundPropertyAccess(t *testing.T) {
	base := "(trait Named (property (String self.name) get))\n(template (Named B))\n(fun f (B) None)\n"
	gStr := "(fun g (String) None)\n(let g (s) = None)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = None)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"trait property (String) vs Number", base + gNum + "(let f (b) = (g b.name))", true},
		{"trait property (String) vs String", base + gStr + "(let f (b) = (g b.name))", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
