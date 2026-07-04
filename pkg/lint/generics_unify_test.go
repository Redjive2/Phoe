package lint

import "testing"

// Parametric result instantiation: a generic function whose RESULT is a type
// parameter that also appears in its PARAMETERS has its result type fixed by the
// matching argument(s) at each call — `(fun id (I) I)` makes `(id 5)` a Number.
// When the variable appears in several parameter positions the result is their
// join, which is sound (the returned value is one of those arguments).
func TestGenericResultInstantiation(t *testing.T) {
	id := "(template I)\n(fun id (I) I)\n(let id (x) = x)\n"
	snd := "(template I O)\n(fun snd (I O) O)\n(let snd (a b) = b)\n"
	same := "(template I)\n(fun same (I I) I)\n(let same (a b) = a)\n"
	gStr := "(fun g (String) None)\n(let g (s) = none)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = none)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"id result (Number) vs String", id + gStr + "(const a (id 5))\n(g a)", true},
		{"id result (Number) vs Number", id + gNum + "(const a (id 5))\n(g a)", false},
		{"id result (String) vs Number", id + gNum + "(const a (id 'x'))\n(g a)", true},
		// snd returns its SECOND argument's type.
		{"snd result (String) vs String", snd + gStr + "(const a (snd 5 'x'))\n(g a)", false},
		{"snd result (String) vs Number", snd + gNum + "(const a (snd 5 'x'))\n(g a)", true},
		// same joins both args; {5,6} ⊆ Number, so a Number expectation is clean.
		{"same joined result ⊆ Number", same + gNum + "(const a (same 5 6))\n(g a)", false},
		{"same joined result vs String", same + gStr + "(const a (same 5 6))\n(g a)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}

// Composite-parameter unification: a higher-order generic whose result variable
// is the RESULT of a function-typed parameter `(fun (I) O)` instantiates its
// result to the argument function's result type — the `apply`/`bind`/`map`
// pattern. The argument lambda's result is inferred from its body.
func TestCompositeParamUnification(t *testing.T) {
	apply := "(template I O)\n(fun apply (I (fun (I) O)) O)\n(let apply (x fn) = (fn x))\n"
	gStr := "(fun g (String) None)\n(let g (s) = None)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = None)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// The lambda returns a String, so apply's result is String.
		{"apply result (String) vs Number", apply + gNum + "(const r (apply 5 (fun (n) 'hi')))\n(g r)", true},
		{"apply result (String) vs String", apply + gStr + "(const r (apply 5 (fun (n) 'hi')))\n(g r)", false},
		// The lambda returns a Number literal, so apply's result is Number.
		{"apply result (Number) vs String", apply + gStr + "(const r (apply 5 (fun (n) 99)))\n(g r)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
