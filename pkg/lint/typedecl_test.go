package lint

import (
	"testing"

	"pho/pkg/annot"
)

// A named type alias `(type Name T)` lints clean, is defined for later
// references, and a genuinely-unknown type name still flags.
func TestTypeDeclLintsClean(t *testing.T) {
	clean := []string{
		"(type Status (Or 200 404 500))\n(let x = (200.is? Status))\n",
		"(type Status (Or 200 404))\n(type Maybe-Status (Or Status None))\n(let x = (none.is? Maybe-Status))\n",
		"(type Method (Or 'GET' 'POST'))\n(let x = ('GET'.is? Method))\n",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.phl", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean, got %#v\n  src: %q", d, src)
		}
	}
	// A genuinely-unknown type name in a guard still flags as unresolved.
	if d := AnalyzeFile("t.phl", []byte("(let x = (5.is? nope))\n")); len(d) == 0 {
		t.Errorf("unknown type name Nope should flag")
	}
}

// The gradual checker resolves user-declared type aliases alongside builtins:
// a named enum used in a `~sig`/`~type` annotation catches a provable mismatch,
// and a named type narrows in an occurrence-typing guard.
func TestNamedTypeChecking(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// A named enum in a function signature.
		{"sig arg in named enum", "(type Method (Or 'GET' 'POST'))\n" +
			"(fun handle (Method) Method)\n(let handle (m) = m)\n(handle 'GET')", false},
		{"sig arg outside named enum", "(type Method (Or 'GET' 'POST'))\n" +
			"(fun handle (Method) Method)\n(let handle (m) = m)\n(handle 'DELETE')", true},
		// A named alias in a (~type …) var annotation.
		{"type annot named alias clean", "(type Status (Or 200 404))\n(let var (Status s) = 200)", false},
		{"type annot named alias mismatch", "(type Status (Or 200 404))\n(let var (Status s) = 500)", true},
		// Occurrence typing narrows a named-union binding in each arm.
		{"narrow named union then/else ok", "(type Ns (Or Number String))\n" +
			"(fun need-n (Number) Number)\n(fun need-n (n) n)\n" +
			"(fun need-s (String) String)\n(fun need-s (s) s)\n" +
			"(let var (Ns x) = 5)\n(if (x.is? Number) then (need-n x) else (need-s x))", false},
		{"narrow named union wrong arm", "(type Ns (Or Number String))\n" +
			"(fun need-n (Number) Number)\n(fun need-n (n) n)\n" +
			"(fun need-s (String) String)\n(fun need-s (s) s)\n" +
			"(let var (Ns x) = 5)\n(if (x.is? Number) then (need-s x) else (need-n x))", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := AnalyzeFile("t.pho", []byte(c.src))
			if got := hasDiag(d, "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q\n  diags: %#v", got, c.wantErr, c.src, d)
			}
		})
	}
}
