package builtins

import "testing"

// An atom value in a type position is its SINGLETON type, so `:ok` doubles as
// an enum member type. (Or :ok :error) builds a tagged union; membership and
// subtyping are exact. This is the runtime face of core.AtomSingleton — the
// asType coercion in typeval.go is what lets a bare atom stand in for a type.
func TestAtomSingletonRuntime(t *testing.T) {
	// `:ok` as a type: membership is exact, not "any atom". The idiomatic
	// surface is the dot form `value.Is? type` (the universal Unknown.Is?
	// method), which delegates to the Is? membership builtin.
	wantBool(t, "(:ok.is? :ok)", true)
	wantBool(t, "(:ok.is? :error)", false)
	wantBool(t, "(:ok.is? Atom)", true) // still a subtype of bare Atom

	// (Or :ok :error) — a tagged union over singletons.
	wantBool(t, "(:ok.is? (Or :ok :error))", true)
	wantBool(t, "(:error.is? (Or :ok :error))", true)
	wantBool(t, "(:other.is? (Or :ok :error))", false)

	// A non-atom never inhabits an atom singleton; the string "ok" ≠ atom :ok.
	wantBool(t, "(5.is? :ok)", false)
	wantBool(t, `('ok'.is? :ok)`, false)

	// subtype? over singletons: exact set relations.
	wantBool(t, "(subtype? :ok (Or :ok :error))", true)
	wantBool(t, "(subtype? :ok Atom)", true)
	wantBool(t, "(subtype? :ok :error)", false)
	wantBool(t, "(subtype? Atom :ok)", false) // bare Atom ⊄ a singleton
	wantBool(t, "(subtype? (Or :ok :error) :ok)", false)

	// An enum mixed with a primitive resolves both arms.
	wantBool(t, "(:ok.is? (Or :ok Number))", true)
	wantBool(t, "(5.is? (Or :ok Number))", true)
	wantBool(t, `('x'.is? (Or :ok Number))`, false)
}
