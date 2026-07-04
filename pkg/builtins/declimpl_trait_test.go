package builtins

import "testing"

// A trait DEFAULT method implementation written with the decl/impl-split `=`
// form is registered and injected just like the `(method …)` form
// (trait.go addTraitMember + isTraitMemberForm). Real-tree coverage so the fix
// can't silently regress under sibling churn.
//
// NOTE: trait definitions register in process-GLOBAL state, so the trait names
// here are unique across the package's tests to avoid cross-test pollution.
func TestDeclImplTraitDefaultEqForm(t *testing.T) {
	// With an explicit `()` extends-list: addTraitMember must accept the `=` member.
	wantStr(t, "(type EqGreeter (Trait () (let self.eqhi (self) = 'hello')))\n(struct EqBox x)\n(let b = EqBox.{ x = 1 })\n(b.eqhi)", "hello")

	// A struct's OWN `=` method still overrides the `=` default.
	wantStr(t, "(type EqGreeter2 (Trait () (let self.eqho (self) = 'def')))\n(struct EqOwn x)\n(let EqOwn.eqho (self) = 'own')\n(let o = EqOwn.{ x = 1 })\n(o.eqho)", "own")

	// WITHOUT an extends-list, a leading `=` default must classify as a MEMBER
	// (isTraitMemberForm), NOT be mistaken for the extends-list — which would
	// error "a trait can only extend other traits". Defining it must be clean.
	// (Bare-no-extends defaults don't auto-inject — same as the `method` form —
	// so we assert only that the definition classifies without an extend error.)
	if _, codes := evalProgramDiag(t, "(type EqBare (Trait (let self.eqbare (self) = 'bare')))"); len(codes) != 0 {
		t.Errorf("bare `=` trait member should classify as a member, got diags: %v", codes)
	}
}
