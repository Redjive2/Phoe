package lint

import "testing"

// Phase 1 generics: a `(template …)` declaration, a `{}`-brace generic struct,
// and a generic method (sig + impl) all lint cleanly — the type parameters are
// recognized as gradual type names, so they neither read as undefined
// identifiers nor make the method sig look like a duplicate of its impl.
func TestGenericsPhase1LintsClean(t *testing.T) {
	src := "(template U (Some-Type B))\n" +
		"(struct Container { U u B #b })\n" +
		"(template I O)\n" +
		"(method I.bind (Self (fun (I) O)) O)\n" +
		"(let I.bind (self fn) = (fn self))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
		t.Errorf("generic template/struct/method should lint clean; got %#v", d)
	}
}

// Recognizing generics is not blanket suppression: a genuine typo in a generic
// method's body still fires.
func TestGenericsStillFlagsErrors(t *testing.T) {
	src := "(template I O)\n" +
		"(method I.map (Self (fun (I) O)) O)\n" +
		"(let I.map (self fn) = (bogus-fn self))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); !hasDiag(d, "unresolved-identifier") {
		t.Errorf("a typo in a generic method body should still fire; got %#v", d)
	}
}
