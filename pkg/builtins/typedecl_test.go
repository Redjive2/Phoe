package builtins

import (
	"testing"

	"pho/pkg/core"
)

// (type Name T) binds a named type alias usable anywhere a type is: membership
// (x.Is? Name), connectives (Or Name …), subtype?, and composing further
// aliases. It is a constant KindType binding — non-recursive (Name is not yet
// in scope while T evaluates).
func TestTypeDeclaration(t *testing.T) {
	// A named enum over literal singletons.
	wantBool(t, "(type Status (Or 200 404 500))\n(200.is? Status)", true)
	wantBool(t, "(type Status (Or 200 404 500))\n(403.is? Status)", false)
	wantBool(t, `(type Method (Or 'GET' 'POST'))`+"\n"+`('GET'.is? Method)`, true)
	wantBool(t, `(type Method (Or 'GET' 'POST'))`+"\n"+`('DELETE'.is? Method)`, false)

	// A named alias of a builtin/composite, and a literal alias.
	wantBool(t, "(type num Number)\n(5.is? num)", true)
	wantBool(t, "(type Five 5)\n(5.is? Five)", true)
	wantBool(t, "(type Five 5)\n(6.is? Five)", false)

	// Aliases compose: a later alias may reference an earlier one.
	const opt = "(type Status (Or 200 404))\n(type Maybe-Status (Or Status None))\n"
	wantBool(t, opt+"(200.is? Maybe-Status)", true)
	wantBool(t, opt+"(none.is? Maybe-Status)", true)
	wantBool(t, opt+"(500.is? Maybe-Status)", false)

	// subtype? over named types.
	wantBool(t, "(type Status (Or 200 404))\n(subtype? Status Number)", true)
	wantBool(t, "(type Status (Or 200 404))\n(subtype? Number Status)", false)

	// A named alias is the SAME interned type as its definition (an alias, not
	// a distinct nominal type).
	wantBool(t, "(type Status (Or 200 404))\n(== Status (Or 200 404))", true)
}

// `type` rejects a non-type value and a bad arity, and cannot shadow a builtin.
func TestTypeDeclarationErrors(t *testing.T) {
	if _, codes := evalProgramDiag(t, "(type Bad (fun (x) x))"); !hasCode(codes, core.ErrType) {
		t.Errorf("(type Bad <function>) should be a type error; got %v", codes)
	}
	if _, codes := evalProgramDiag(t, "(type Status 200 404)"); !hasCode(codes, core.ErrArity) {
		t.Errorf("(type Status 200 404) should be an arity error; got %v", codes)
	}
}
