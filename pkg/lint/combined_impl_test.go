package lint

import "testing"

// The combined signature+implementation form is no longer valid: `fun`/`method`
// declare only SIGNATURES; the implementation is a separate `(= …)`. A named
// `fun`/`method` with a value (non-type) parameter list is the old combined form
// and must fire `combined-impl`.
func TestCombinedImplRejected(t *testing.T) {
	fires := []string{
		"(fun add (a b) (+ a b))\n",                               // combined fun impl
		"(fun greet (name) name)\n",                               // single value param
		"(struct P { Number x })\n(method P.get (self) self.x)\n", // combined method impl
		"(fun run () do (let x = 1) x)\n",                         // empty params, do body (not a sig)
	}
	for _, src := range fires {
		if !hasDiag(AnalyzeFile("t.pho", []byte(src)), "combined-impl") {
			t.Errorf("expected combined-impl for %q", src)
		}
	}
}

// Signatures, the `=` implementation form, and anonymous `fun` values are all
// still valid — none should fire combined-impl.
func TestCombinedImplAccepted(t *testing.T) {
	clean := []string{
		"(fun add (Number Number) Number)\n",                            // signature only
		"(fun add (Number Number) Number)\n(let add (a b) = (+ a b))\n", // split sig + impl
		"(let add (a b) = (+ a b))\n",                                   // `=` impl, no sig
		"(fun run () None)\n",                                           // empty-param signature
		"(struct P { Number x })\n(method P.get (Self) Number)\n(let P.get (self) = self.x)\n", // method split
		"(fun (x) (+ x 1))\n(fun () 5)\n", // anonymous fun values
	}
	for _, src := range clean {
		if hasDiag(AnalyzeFile("t.pho", []byte(src)), "combined-impl") {
			t.Errorf("did not expect combined-impl for %q", src)
		}
	}
}

// The error message names the impl and points the writer at the `=` form.
func TestCombinedImplMessage(t *testing.T) {
	d := AnalyzeFile("t.pho", []byte("(fun add (a b) (+ a b))\n"))
	var msg string
	for _, di := range d {
		if di.Code == "combined-impl" {
			msg = di.Message
		}
	}
	if msg == "" {
		t.Fatal("no combined-impl diagnostic")
	}
	for _, want := range []string{"(let add", "signature"} {
		if !contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

// The old `(= name (params) body)` define form is no longer recognized: `=` is
// reassignment only, so the 3-arg form is just a wrong-arity `=` (bad-form-arity),
// not a special "retired impl" — the implementation form is a `let` clause.
func TestRetiredEqImplNotRecognized(t *testing.T) {
	for _, src := range []string{
		"(= add (a b) (+ a b))\n",
		"(struct P { Number x })\n(= P.get (self) self.x)\n",
	} {
		d := AnalyzeFile("t.pho", []byte(src))
		if !hasDiag(d, "bad-form-arity") {
			t.Errorf("expected bad-form-arity (not recognized) for %q, got %v", src, d)
		}
		if hasDiag(d, "retired-impl") {
			t.Errorf("the '=' define form must not draw a special retired-impl diagnostic: %q", src)
		}
	}
	// A plain reassignment stays a valid reassignment — no arity error.
	if d := AnalyzeFile("t.pho", []byte("(let var x = 1)\n(= x 2)\n")); hasDiag(d, "bad-form-arity") {
		t.Errorf("reassignment must stay valid, got %v", d)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
