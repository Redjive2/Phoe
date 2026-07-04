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
	wantBool(t, "(5.is? Number)", true)
	wantBool(t, `('hi'.is? String)`, true)
	wantBool(t, "(true.is? Boolean)", true)
	wantBool(t, "(:ok.is? Atom)", true)
	wantBool(t, "([1 2 3].is? List)", true)
	wantBool(t, `([ 'k' -> 1 ].is? Map)`, true)
	wantBool(t, "(none.is? None)", true)
	wantBool(t, "((fun (x) x).is? Function)", true)
	wantBool(t, "(Number.is? Type)", true) // a type value inhabits Type
	// Distinct kinds.
	wantBool(t, "(5.is? String)", false)
	wantBool(t, `('hi'.is? Number)`, false)
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
	wantStr(t, `'%None'`, "None") // None is the type of the `none` value
	wantStr(t, `'%Never'`, "Never")
}

// (x.Is? T) is membership: x inhabits T.
func TestIsMembership(t *testing.T) {
	wantBool(t, "(5.is? Number)", true)
	wantBool(t, "(5.is? String)", false)
	wantBool(t, `('hi'.is? String)`, true)
	wantBool(t, "(:ok.is? Atom)", true)
	// Everything inhabits Unknown (⊤); nothing inhabits Never (⊥).
	wantBool(t, "(5.is? Unknown)", true)
	wantBool(t, `('x'.is? Unknown)`, true)
	wantBool(t, "(5.is? Never)", false)
	// `none` inhabits None (its type); a non-nil value does not.
	wantBool(t, "(none.is? None)", true)
	wantBool(t, "(5.is? None)", false)
}

// (subtype? S T) is set inclusion, with Unknown as ⊤ and Never as ⊥.
func TestSubtype(t *testing.T) {
	wantBool(t, "(subtype? Number Number)", true)
	wantBool(t, "(subtype? Number Unknown)", true)
	wantBool(t, "(subtype? Number Never)", false)
	wantBool(t, "(subtype? Never Number)", true) // ⊥ ⊆ everything
	wantBool(t, "(subtype? Unknown Number)", false)
	wantBool(t, "(subtype? Unknown Unknown)", true)
	wantBool(t, "(subtype? Never Never)", true)
}

// A struct instance inhabits its own struct type, not another's, and inhabits
// Unknown; the struct type prints as its name.
func TestStructMembership(t *testing.T) {
	const decls = "(struct Point #x #y)\n(struct Line #a #b)\n"
	wantBool(t, decls+"(Point.{ #x = 1 #y = 2 }.is? Point)", true)
	wantBool(t, decls+"(Point.{ #x = 1 #y = 2 }.is? Line)", false)
	wantBool(t, decls+"(Point.{ #x = 1 #y = 2 }.is? Unknown)", true)
	wantBool(t, decls+"(subtype? Point Unknown)", true)
	wantStr(t, decls+`'%Point'`, "Point")
}

// After Stage A2 a struct's NAME is itself a first-class type value (KindType),
// so it works in typeof/Is?/subtype? — not only on instances. The same name is
// still constructible.
func TestStructTypeFirstClass(t *testing.T) {
	const d = "(struct Point x y)\n(struct Line #a #b)\n(let var p = Point.{ x = 1 y = 2 })\n"
	// The struct name is a type; an instance inhabits it.
	wantBool(t, d+"(p.is? Point)", true)
	wantBool(t, d+"(p.is? Line)", false)
	// A struct type value itself inhabits Type; struct types are subtypes of
	// Unknown and distinct from one another.
	wantBool(t, d+"(Point.is? Type)", true)
	wantBool(t, d+"(subtype? Point Unknown)", true)
	wantBool(t, d+"(subtype? Point Line)", false)
	wantBool(t, d+"(== Point Point)", true)
	wantBool(t, d+"(== Point Line)", false)
	// Construction still works and the instance is real.
	wantBool(t, d+"(== p.x 1)", true)
	wantStr(t, d+`'%Point'`, "Point")
}

// A struct type stays constructible and dispatches its methods after the
// KindConstructor→KindType migration.
func TestStructTypeConstructsAndDispatches(t *testing.T) {
	const d = "(struct Vec #x #y)\n(let Vec.sum (self) = (+ self.#x self.#y))\n(let var v = Vec.{ #x = 3 #y = 4 })\n"
	wantBool(t, d+"(== (v.sum) 7)", true)
	wantBool(t, d+"(v.is? Vec)", true)
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
	wantBool(t, "(5.is? (Or Number String))", true)
	wantBool(t, `('x'.is? (Or Number String))`, true)
	wantBool(t, "(true.is? (Or Number String))", false)
	// Subtyping over unions.
	wantBool(t, "(subtype? Number (Or Number String))", true)
	wantBool(t, "(subtype? (Or Number String) Number)", false)
	// Negation.
	wantBool(t, "(5.is? (Not String))", true)
	wantBool(t, "(5.is? (Not Number))", false)
	// Identities via structural (interned) equality.
	wantBool(t, "(== (And (Or Number String) Number) Number)", true)
	wantBool(t, "(== (Diff (Or Number String) String) Number)", true)
	wantBool(t, "(== (Or) Never)", true)    // empty union = ⊥
	wantBool(t, "(== (And) Unknown)", true) // empty intersection = ⊤
	wantBool(t, "(== (Not Unknown) Never)", true)
	wantBool(t, "(== (Not Never) Unknown)", true)
	// Collection and Dynamic.
	wantBool(t, "([1 2].is? Collection)", true)
	wantBool(t, `('s'.is? Collection)`, true)
	wantBool(t, "(5.is? Collection)", false)
	wantBool(t, "(5.is? Dynamic)", true)
	wantBool(t, `('x'.is? Dynamic)`, true)
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
		v := evalInPackage(t, "(let Collection.doubled (self) = (* self.size 2))\n"+recv, nil)
		if v.Kind != core.KindNum || v.Val.(float64) != want {
			t.Errorf("%s = %v (%s), want %v", recv, v.Val, v.Kind, want)
		}
	}
	num("([1 2 3].doubled)", 6)      // list
	num(`('ab'.doubled)`, 4)         // string
	num(`([ 'k' -> 1 ].doubled)`, 2) // map
}

// Attaching a method to a non-enumerable type (the empty type / a cofinite or
// gradual type) is rejected.
func TestCollectionMethodBadReceiver(t *testing.T) {
	var codes []string
	evalInPackage(t, "(let Never.x (self) = self)", func(c string) { codes = append(codes, c) })
	if !hasCode(codes, core.ErrType) {
		t.Errorf("(method Never.X …) should be a type error; got %v", codes)
	}
}

// Stage F: parametric (List T) / (Map K V) — covariant subtyping and structural
// membership, while typeof and bare-type usage are unchanged.
func TestParametricCollectionTypes(t *testing.T) {
	// (List T) membership is a structural element check.
	wantBool(t, "([1 2 3].is? (List Number))", true)
	wantBool(t, `([1 'x'].is? (List Number))`, false)
	wantBool(t, "([].is? (List Number))", true) // empty list inhabits any (List T)
	wantBool(t, "(5.is? (List Number))", false) // not a list
	wantBool(t, `(['a' 'b'].is? (List String))`, true)
	// Covariant subtyping.
	wantBool(t, "(subtype? (List Number) (List Number))", true)
	wantBool(t, "(subtype? (List Number) (List Unknown))", true)
	wantBool(t, "(subtype? (List Unknown) (List Number))", false)
	wantBool(t, "(subtype? (List Number) (List String))", false)
	wantBool(t, "(subtype? (List Number) List)", true) // refined <: bare
	// (Map K V).
	wantBool(t, `([ 'k' -> 1 ].is? (Map String Number))`, true)
	wantBool(t, `([ 'k' -> 'v' ].is? (Map String Number))`, false)
	// A list still inhabits the bare List type; List remains a type constant.
	wantBool(t, "([1 2 3].is? List)", true)
	// A union with a parametric member.
	wantBool(t, "([1 2].is? (Or (List Number) None))", true)
	wantBool(t, "(none.is? (Or (List Number) None))", true)
	wantBool(t, `(['x'].is? (Or (List Number) None))`, false)
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
	wantBool(t, "((fun (x) x).is? (Fun [Number] Number))", true)                        // degraded: just "is a function"
	wantBool(t, "(5.is? (Fun [Number] Number))", false)
	wantStr(t, `'%(Fun [Number Number] Boolean)'`, "(Fun [Number Number] Boolean)")
}

// Interned type values are valid, value-comparable dict keys.
func TestTypeAsDictKey(t *testing.T) {
	wantStr(t, `(get [ Number -> 'n' String -> 's' ] Number)`, "n")
	wantStr(t, `(get [ Number -> 'n' String -> 's' ] String)`, "s")
}
