package builtins

import "testing"

// A literal value in a type position is its SINGLETON type, so numbers,
// strings, and bools double as enum members the same way atoms do — the runtime
// face of core.{Num,Str,Bool}Singleton via the asType coercion in typeval.go.
func TestLiteralSingletonRuntime(t *testing.T) {
	// Numbers. The idiomatic surface is the dot form `value.Is? type`.
	wantBool(t, "(5.is? 5)", true)
	wantBool(t, "(5.is? 6)", false)
	wantBool(t, "(5.is? Number)", true) // still a subtype of bare Number
	wantBool(t, "(200.is? (Or 200 404 500))", true)
	wantBool(t, "(403.is? (Or 200 404 500))", false)

	// Strings — string-discriminated unions (the common case).
	wantBool(t, `('GET'.is? 'GET')`, true)
	wantBool(t, `('GET'.is? 'POST')`, false)
	wantBool(t, `('GET'.is? (Or 'GET' 'POST'))`, true)
	wantBool(t, `('DELETE'.is? (Or 'GET' 'POST'))`, false)
	wantBool(t, `('GET'.is? String)`, true)

	// Bools.
	wantBool(t, "(true.is? true)", true)
	wantBool(t, "(false.is? true)", false)
	wantBool(t, "(true.is? Boolean)", true)

	// Cross-primitive: a literal never inhabits another primitive's singleton.
	wantBool(t, `(5.is? '5')`, false)
	wantBool(t, "(:ok.is? 5)", false)
	wantBool(t, `('GET'.is? 5)`, false)

	// subtype? over literal singletons: exact set relations.
	wantBool(t, "(subtype? 5 Number)", true)
	wantBool(t, "(subtype? Number 5)", false)
	wantBool(t, "(subtype? 200 (Or 200 404))", true)
	wantBool(t, `(subtype? (Or 'GET' 'POST') String)`, true)
	wantBool(t, "(subtype? 5 6)", false)

	// The full bool set is just Boolean (interned identity).
	wantBool(t, "(subtype? Boolean (Or true false))", true)
	wantBool(t, "(subtype? (Or true false) Boolean)", true)
}

// `Nil` in a type position is the nil type (NilT): it is the sole value of that
// type, so `(x.Is? Nil)` is the natural nil test (used across the stdlib, e.g.
// os.phl's Guard / Open). The named `NilT` still works too.
func TestNilAsTypeRuntime(t *testing.T) {
	wantBool(t, "(none.is? none)", true)
	wantBool(t, "(5.is? none)", false)
	wantBool(t, "(none.is? NilT)", true)
	wantBool(t, "(none.is? (Or Number none))", true) // nil arm of an optional union
	wantBool(t, "(5.is? (Or Number none))", true)
}
