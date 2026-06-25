package builtins

import (
	"testing"

	"pho/pkg/core"
)

// wantBool evaluates src and asserts the last form is the given boolean.
func wantBool(t *testing.T, src string, want bool) {
	t.Helper()
	v := evalProgram(t, src)
	if v.Kind != core.KindBool {
		t.Fatalf("eval(%q): want bool, got kind %q (%v)", src, v.Kind, v.Val)
	}
	if v.Val.(bool) != want {
		t.Errorf("eval(%q) = %v, want %v", src, v.Val, want)
	}
}

// Atoms compare by interned identity: same name → equal, regardless of how
// the atom was constructed; distinct names (including leading zeros) differ;
// an atom never equals a string of the same text.
func TestAtomEquality(t *testing.T) {
	wantBool(t, "(== :foo :foo)", true)
	wantBool(t, "(== :foo :bar)", false)
	wantBool(t, `(== :foo 'foo')`, false) // distinct kinds
	wantBool(t, "(== :01 :1)", false)     // leading zeros are significant
	wantBool(t, "(== :123 :123)", true)
	// (atom "foo") and a literal of the same name intern to one *Atom, so
	// they are pointer-equal.
	wantBool(t, `(== (atom 'foo') :foo)`, true)
}

func TestAtomPredicateAndConversions(t *testing.T) {
	wantBool(t, "(atom? :fast)", true)
	wantBool(t, `(atom? 'fast')`, false)
	wantBool(t, "(atom? 5)", false)

	if v := evalProgram(t, "(atomName :fast)"); v.Kind != core.KindStr || v.Val.(string) != "fast" {
		t.Errorf("(atomName :fast) = %q (%v), want str \"fast\"", v.Kind, v.Val)
	}
	if v := evalProgram(t, `(atom 'fast')`); v.Kind != core.KindAtom || v.Val.(*core.Atom).Name() != "fast" {
		t.Errorf("(atom \"fast\") = %q (%v), want atom :fast", v.Kind, v.Val)
	}
}

// Atoms are legal scalar dict keys — usable both as the key and the value.
func TestAtomAsDictKey(t *testing.T) {
	v := evalProgram(t, "{:mode :fast :retries 3}.[:mode]")
	if v.Kind != core.KindAtom || v.Val.(*core.Atom).Name() != "fast" {
		t.Errorf("{:mode :fast ...}.[:mode] = %q (%v), want atom :fast", v.Kind, v.Val)
	}
}

// A trailing '?' makes a valid identifier (the predicate convention): the
// builtin `atom?` resolves as one identifier at runtime, not as `atom` + `?`.
func TestTrailingQuestionIdentifier(t *testing.T) {
	if v := evalProgram(t, "atom?"); v.Kind != core.KindFun {
		t.Errorf("`atom?` resolved to kind %q (%v), want a function", v.Kind, v.Val)
	}
}

// Spaced slices keep working unchanged.
func TestSpacedSliceStillWorks(t *testing.T) {
	if v := evalProgram(t, `'abcdef'.[1 : 3]`); v.Kind != core.KindStr || v.Val.(string) != "bc" {
		t.Errorf(`"abcdef".[1 : 3] = %q (%v), want "bc"`, v.Kind, v.Val)
	}
}

func TestAtomErrors(t *testing.T) {
	// A malformed atom literal (digits then letters) is rejected.
	if _, codes := evalProgramDiag(t, ":12abc"); !hasCode(codes, core.ErrBadLiteral) {
		t.Errorf("`:12abc` should raise %q; got %v", core.ErrBadLiteral, codes)
	}
	// (atom ...) validates the string is a legal atom form.
	if _, codes := evalProgramDiag(t, `(atom 'not ok')`); !hasCode(codes, core.ErrBadLiteral) {
		t.Errorf("(atom ...) of a non-atom string should raise %q; got %v", core.ErrBadLiteral, codes)
	}
	// (atom ...) requires a string argument.
	if _, codes := evalProgramDiag(t, "(atom 5)"); !hasCode(codes, core.ErrType) {
		t.Errorf("(atom ...) of a non-string should raise %q; got %v", core.ErrType, codes)
	}
}
