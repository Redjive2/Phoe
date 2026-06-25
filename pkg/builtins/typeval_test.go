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

// Every primitive kind inhabits its type (there is no typeof — a value inhabits
// many types, so membership is the right question).
func TestPrimitiveMembership(t *testing.T) {
	wantBool(t, "(5.Is? Number)", true)
	wantBool(t, `('hi'.Is? String)`, true)
	wantBool(t, "(True.Is? Boolean)", true)
	wantBool(t, "(:ok.Is? Atom)", true)
	wantBool(t, "([1 2 3].Is? List)", true)
	wantBool(t, `({ 'k' 1 }.Is? Map)`, true)
	wantBool(t, "(Nil.Is? NilT)", true)
	wantBool(t, "((fun (x) x).Is? Function)", true)
	wantBool(t, "(Number.Is? Type)", true) // a type value inhabits Type
	// Distinct kinds.
	wantBool(t, "(5.Is? String)", false)
	wantBool(t, `('hi'.Is? Number)`, false)
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
	wantStr(t, `'%Number'`, "Number")
	wantStr(t, `'%List'`, "List")
	wantStr(t, `'%Unknown'`, "Unknown")
	wantStr(t, `'%None'`, "None")
	wantStr(t, `'%NilT'`, "Nil")
}

// (x.Is? T) is membership: x inhabits T.
func TestIsMembership(t *testing.T) {
	wantBool(t, "(5.Is? Number)", true)
	wantBool(t, "(5.Is? String)", false)
	wantBool(t, `('hi'.Is? String)`, true)
	wantBool(t, "(:ok.Is? Atom)", true)
	// Everything inhabits Unknown (⊤); nothing inhabits None (⊥).
	wantBool(t, "(5.Is? Unknown)", true)
	wantBool(t, `('x'.Is? Unknown)`, true)
	wantBool(t, "(5.Is? None)", false)
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

// A struct instance inhabits its own struct type, not another's, and inhabits
// Unknown; the struct type prints as its name.
func TestStructMembership(t *testing.T) {
	const decls = "(struct Point x y)\n(struct Line a b)\n"
	wantBool(t, decls+"(Point.{ x 1 y 2 }.Is? Point)", true)
	wantBool(t, decls+"(Point.{ x 1 y 2 }.Is? Line)", false)
	wantBool(t, decls+"(Point.{ x 1 y 2 }.Is? Unknown)", true)
	wantBool(t, decls+"(subtype? Point Unknown)", true)
	wantStr(t, decls+`'%Point'`, "Point")
}

// After Stage A2 a struct's NAME is itself a first-class type value (KindType),
// so it works in typeof/Is?/subtype? — not only on instances. The same name is
// still constructible.
func TestStructTypeFirstClass(t *testing.T) {
	const d = "(struct Point X Y)\n(struct Line a b)\n(var p Point.{ X 1 Y 2 })\n"
	// The struct name is a type; an instance inhabits it.
	wantBool(t, d+"(p.Is? Point)", true)
	wantBool(t, d+"(p.Is? Line)", false)
	// A struct type value itself inhabits Type; struct types are subtypes of
	// Unknown and distinct from one another.
	wantBool(t, d+"(Point.Is? Type)", true)
	wantBool(t, d+"(subtype? Point Unknown)", true)
	wantBool(t, d+"(subtype? Point Line)", false)
	wantBool(t, d+"(== Point Point)", true)
	wantBool(t, d+"(== Point Line)", false)
	// Construction still works and the instance is real.
	wantBool(t, d+"(== p.X 1)", true)
	wantStr(t, d+`'%Point'`, "Point")
}

// A struct type stays constructible and dispatches its methods after the
// KindConstructor→KindType migration.
func TestStructTypeConstructsAndDispatches(t *testing.T) {
	const d = "(struct Vec x y)\n(method Vec.Sum (self) (+ self.x self.y))\n(var v Vec.{ x 3 y 4 })\n"
	wantBool(t, d+"(== (v.Sum) 7)", true)
	wantBool(t, d+"(v.Is? Vec)", true)
}

// Calling a non-struct type (a primitive type value) is a not-callable error,
// since only struct types carry a constructor.
func TestNonStructTypeNotConstructible(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(Number 5)"); !hasCode(codes, core.ErrNotCallable) {
		t.Errorf("(Number 5) should be a not-callable error; got codes %v", codes)
	}
}

// The set-theoretic connectives build composite types that Is?/subtype? then
// decide via the emptiness algebra.
func TestConnectives(t *testing.T) {
	// Union membership.
	wantBool(t, "(5.Is? (Or Number String))", true)
	wantBool(t, `('x'.Is? (Or Number String))`, true)
	wantBool(t, "(True.Is? (Or Number String))", false)
	// Subtyping over unions.
	wantBool(t, "(subtype? Number (Or Number String))", true)
	wantBool(t, "(subtype? (Or Number String) Number)", false)
	// Negation.
	wantBool(t, "(5.Is? (Not String))", true)
	wantBool(t, "(5.Is? (Not Number))", false)
	// Identities via structural (interned) equality.
	wantBool(t, "(== (And (Or Number String) Number) Number)", true)
	wantBool(t, "(== (Diff (Or Number String) String) Number)", true)
	wantBool(t, "(== (Or) None)", true)     // empty union = ⊥
	wantBool(t, "(== (And) Unknown)", true) // empty intersection = ⊤
	wantBool(t, "(== (Not Unknown) None)", true)
	wantBool(t, "(== (Not None) Unknown)", true)
	// Collection and Dynamic.
	wantBool(t, "([1 2].Is? Collection)", true)
	wantBool(t, `('s'.Is? Collection)`, true)
	wantBool(t, "(5.Is? Collection)", false)
	wantBool(t, "(5.Is? Dynamic)", true)
	wantBool(t, `('x'.Is? Dynamic)`, true)
}

// A connective applied to a genuinely non-type argument is a type error — but a
// LITERAL (atom/number/string/bool) is a valid singleton type, so `(Or Number
// 5)` means `Number | 5` (= Number), not an error.
func TestConnectiveTypeError(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(Or Number 5)"); hasCode(codes, core.ErrType) {
		t.Errorf("(Or Number 5) is valid (5 is the singleton type 5); got error %v", codes)
	}
	if _, codes := evalProgramDiag(t, `(Or Number :ok)`); hasCode(codes, core.ErrType) {
		t.Errorf("(Or Number :ok) is valid (an atom singleton); got error %v", codes)
	}
	// A list value is not a type and not a literal singleton — still an error.
	if _, codes := evalProgramDiag(t, "(Or Number [1 2])"); !hasCode(codes, core.ErrType) {
		t.Errorf("(Or Number [1 2]) should be a type error; got %v", codes)
	}
}

// A user-defined method on the generic Collection type (String | List | Map)
// dispatches on every member kind from a single declaration — registered under
// each member's extension key.
func TestGenericCollectionMethod(t *testing.T) {
	num := func(recv string, want float64) {
		t.Helper()
		v := evalInPackage(t, "(method Collection.Doubled (self) (* self.Size 2))\n"+recv, nil)
		if v.Kind != core.KindNum || v.Val.(float64) != want {
			t.Errorf("%s = %v (%s), want %v", recv, v.Val, v.Kind, want)
		}
	}
	num("([1 2 3].Doubled)", 6)   // list
	num(`('ab'.Doubled)`, 4)      // string
	num(`({ 'k' 1 }.Doubled)`, 2) // map
}

// Attaching a method to a non-enumerable type (the empty type / a cofinite or
// gradual type) is rejected.
func TestCollectionMethodBadReceiver(t *testing.T) {
	var codes []string
	evalInPackage(t, "(method None.X (self) self)", func(c string) { codes = append(codes, c) })
	if !hasCode(codes, core.ErrType) {
		t.Errorf("(method None.X …) should be a type error; got %v", codes)
	}
}

// Stage F: parametric (List T) / (Map K V) — covariant subtyping and structural
// membership, while typeof and bare-type usage are unchanged.
func TestParametricCollectionTypes(t *testing.T) {
	// (List T) membership is a structural element check.
	wantBool(t, "([1 2 3].Is? (List Number))", true)
	wantBool(t, `([1 'x'].Is? (List Number))`, false)
	wantBool(t, "([].Is? (List Number))", true) // empty list inhabits any (List T)
	wantBool(t, "(5.Is? (List Number))", false) // not a list
	wantBool(t, `(['a' 'b'].Is? (List String))`, true)
	// Covariant subtyping.
	wantBool(t, "(subtype? (List Number) (List Number))", true)
	wantBool(t, "(subtype? (List Number) (List Unknown))", true)
	wantBool(t, "(subtype? (List Unknown) (List Number))", false)
	wantBool(t, "(subtype? (List Number) (List String))", false)
	wantBool(t, "(subtype? (List Number) List)", true) // refined <: bare
	// (Map K V).
	wantBool(t, `({ 'k' 1 }.Is? (Map String Number))`, true)
	wantBool(t, `({ 'k' 'v' }.Is? (Map String Number))`, false)
	// A list still inhabits the bare List type; List remains a type constant.
	wantBool(t, "([1 2 3].Is? List)", true)
	// A union with a parametric member.
	wantBool(t, "([1 2].Is? (Or (List Number) NilT))", true)
	wantBool(t, "(Nil.Is? (Or (List Number) NilT))", true)
	wantBool(t, `(['x'].Is? (Or (List Number) NilT))`, false)
	// Stringify renders the element type.
	wantStr(t, `'%(List Number)'`, "[Number]")
}

// Stage F: function arrows. Contravariant parameters, covariant result.
// Runtime membership only checks "is a function" (closures carry no signature).
func TestFunctionArrowTypes(t *testing.T) {
	wantBool(t, "(subtype? (Fun [Number] Number) (Fun [Number] Number))", true)
	wantBool(t, "(subtype? (Fun [Number] Number) Function)", true)                      // specific <: bare
	wantBool(t, "(subtype? Function (Fun [Number] Number))", false)                     // bare not specific
	wantBool(t, "(subtype? (Fun [Number] Number) (Fun [Number] Unknown))", true)        // covariant result
	wantBool(t, "(subtype? (Fun [Unknown] Number) (Fun [Number] Number))", true)        // contravariant param
	wantBool(t, "(subtype? (Fun [Number] Number) (Fun [Unknown] Number))", false)       // wrong variance
	wantBool(t, "(subtype? (Fun [Number] Number) (Fun [Number Number] Number))", false) // arity
	wantBool(t, "((fun (x) x).Is? (Fun [Number] Number))", true)                        // degraded: just "is a function"
	wantBool(t, "(5.Is? (Fun [Number] Number))", false)
	wantStr(t, `'%(Fun [Number Number] Boolean)'`, "(Fun [Number Number] Boolean)")
}

// Interned type values are valid, value-comparable dict keys.
func TestTypeAsDictKey(t *testing.T) {
	wantStr(t, `(get { Number 'n' String 's' } Number)`, "n")
	wantStr(t, `(get { Number 'n' String 's' } String)`, "s")
}
