package builtins

import (
	"testing"

	"pho/pkg/core"
)

// wantStr evaluates src and asserts it produces the given string value.
func wantStr(t *testing.T, src, want string) {
	t.Helper()
	v := evalProgram(t, src)
	if v.Kind != core.KindStr {
		t.Fatalf("eval(%q): want str, got kind %q (%v)", src, v.Kind, v.Val)
	}
	if v.Val.(string) != want {
		t.Errorf("eval(%q) = %q, want %q", src, v.Val, want)
	}
}

// typeof returns the right type value for every primitive kind, and that
// value's printed form is its type name.
func TestTypeofPrimitives(t *testing.T) {
	wantBool(t, "(== (typeof 5) Number)", true)
	wantBool(t, `(== (typeof "hi") String)`, true)
	wantBool(t, "(== (typeof True) Boolean)", true)
	wantBool(t, "(== (typeof :ok) Atom)", true)
	wantBool(t, "(== (typeof [1 2 3]) List)", true)
	wantBool(t, `(== (typeof { "k" 1 }) Dict)`, true)
	wantBool(t, "(== (typeof Nil) NilT)", true)
	wantBool(t, "(== (typeof (fun (x) x)) Function)", true)
	// A type value's own type is Type.
	wantBool(t, "(== (typeof Number) Type)", true)
	// Cross-kind typeof is distinct.
	wantBool(t, "(== (typeof 5) String)", false)
	wantBool(t, `(== (typeof "hi") Number)`, false)
}

// Type values are interned: equal structures are pointer-equal, so `==`
// works and `(typeof 1) == Number` holds.
func TestTypeValueIdentity(t *testing.T) {
	wantBool(t, "(== Number Number)", true)
	wantBool(t, "(== Number String)", false)
	wantBool(t, "(== Unknown Unknown)", true)
	wantBool(t, "(== None None)", true)
	wantBool(t, "(== Unknown None)", false)
}

// A type's printed (Stringify) form is its name — exercised via string
// interpolation, which coerces through Stringify.
func TestTypeStringify(t *testing.T) {
	wantStr(t, `"%(typeof 5)"`, "Number")
	wantStr(t, `"%(typeof "x")"`, "String")
	wantStr(t, `"%Number"`, "Number")
	wantStr(t, `"%List"`, "List")
	wantStr(t, `"%Unknown"`, "Unknown")
	wantStr(t, `"%None"`, "None")
	wantStr(t, `"%(typeof Nil)"`, "Nil")
}

// (Is? x T) is membership: x inhabits T.
func TestIsMembership(t *testing.T) {
	wantBool(t, "(Is? 5 Number)", true)
	wantBool(t, "(Is? 5 String)", false)
	wantBool(t, `(Is? "hi" String)`, true)
	wantBool(t, "(Is? :ok Atom)", true)
	// Everything inhabits Unknown (⊤); nothing inhabits None (⊥).
	wantBool(t, "(Is? 5 Unknown)", true)
	wantBool(t, `(Is? "x" Unknown)`, true)
	wantBool(t, "(Is? 5 None)", false)
}

// (subtype? S T) is set inclusion, with Unknown as ⊤ and None as ⊥.
func TestSubtype(t *testing.T) {
	wantBool(t, "(subtype? Number Number)", true)
	wantBool(t, "(subtype? Number Unknown)", true)
	wantBool(t, "(subtype? Number None)", false)
	wantBool(t, "(subtype? None Number)", true) // ⊥ ⊆ everything
	wantBool(t, "(subtype? Unknown Number)", false)
	wantBool(t, "(subtype? Unknown Unknown)", true)
	wantBool(t, "(subtype? None None)", true)
}

// typeof on struct instances yields a per-struct type: equal for instances of
// the same struct, distinct across structs, a subtype of Unknown, and printing
// as the struct's name.
func TestTypeofStruct(t *testing.T) {
	const decls = "(struct Point x y)\n(struct Line a b)\n"
	wantBool(t, decls+"(== (typeof Point.{ x 1 y 2 }) (typeof Point.{ x 3 y 4 }))", true)
	wantBool(t, decls+"(== (typeof Point.{ x 1 y 2 }) (typeof Line.{ a 1 b 2 }))", false)
	wantBool(t, decls+"(Is? Point.{ x 1 y 2 } Unknown)", true)
	wantBool(t, decls+"(subtype? (typeof Point.{ x 1 y 2 }) Unknown)", true)
	wantStr(t, decls+`"%(typeof Point.{ x 1 y 2 })"`, "Point")
}

// After Stage A2 a struct's NAME is itself a first-class type value (KindType),
// so it works in typeof/Is?/subtype? — not only on instances. The same name is
// still constructible.
func TestStructTypeFirstClass(t *testing.T) {
	const d = "(struct Point X Y)\n(struct Line a b)\n(var p Point.{ X 1 Y 2 })\n"
	// The struct name is a type; an instance inhabits it.
	wantBool(t, d+"(== (typeof p) Point)", true)
	wantBool(t, d+"(Is? p Point)", true)
	wantBool(t, d+"(Is? p Line)", false)
	// A type value's own type is Type; struct types are subtypes of Unknown
	// and distinct from one another.
	wantBool(t, d+"(== (typeof Point) Type)", true)
	wantBool(t, d+"(subtype? Point Unknown)", true)
	wantBool(t, d+"(subtype? Point Line)", false)
	wantBool(t, d+"(== Point Point)", true)
	wantBool(t, d+"(== Point Line)", false)
	// Construction still works and the instance is real.
	wantBool(t, d+"(== p.X 1)", true)
	wantStr(t, d+`"%Point"`, "Point")
}

// A struct type stays constructible and dispatches its methods after the
// KindConstructor→KindType migration.
func TestStructTypeConstructsAndDispatches(t *testing.T) {
	const d = "(struct Vec x y)\n(method Vec.Sum (self) (+ self.x self.y))\n(var v Vec.{ x 3 y 4 })\n"
	wantBool(t, d+"(== (v.Sum) 7)", true)
	wantBool(t, d+"(Is? v Vec)", true)
}

// Calling a non-struct type (a primitive type value) is a not-callable error,
// since only struct types carry a constructor.
func TestNonStructTypeNotConstructible(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(Number 5)"); !hasCode(codes, core.ErrNotCallable) {
		t.Errorf("(Number 5) should be a not-callable error; got codes %v", codes)
	}
}

// Interned type values are valid, value-comparable dict keys.
func TestTypeAsDictKey(t *testing.T) {
	wantStr(t, `(get { Number "n" String "s" } (typeof 5))`, "n")
	wantStr(t, `(get { Number "n" String "s" } (typeof "x"))`, "s")
}
