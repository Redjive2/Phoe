package lint

import (
	"testing"

	"pho/pkg/annot"
)

// A named type alias `(type Name T)` lints clean, is defined for later
// references, and a genuinely-unknown type name still flags.
func TestTypeDeclLintsClean(t *testing.T) {
	clean := []string{
		"(type Status (Or 200 404 500))\n(const x (200.Is? Status))\n",
		"(type Status (Or 200 404))\n(type MaybeStatus (Or Status Nil))\n(const x (Nil.Is? MaybeStatus))\n",
		"(type Method (Or 'GET' 'POST'))\n(const x ('GET'.Is? Method))\n",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.phl", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean, got %#v\n  src: %q", d, src)
		}
	}
	// A genuinely-unknown type name in a guard still flags as unresolved.
	if d := AnalyzeFile("t.phl", []byte("(const x (5.Is? Nope))\n")); len(d) == 0 {
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
			"(fun handle (Method) Method)\n(fun handle (m) m)\n(handle 'GET')", false},
		{"sig arg outside named enum", "(type Method (Or 'GET' 'POST'))\n" +
			"(fun handle (Method) Method)\n(fun handle (m) m)\n(handle 'DELETE')", true},
		// A named alias in a (~type …) var annotation.
		{"type annot named alias clean", "(type Status (Or 200 404))\n(var (Status s) 200)", false},
		{"type annot named alias mismatch", "(type Status (Or 200 404))\n(var (Status s) 500)", true},
		// Occurrence typing narrows a named-union binding in each arm.
		{"narrow named union then/else ok", "(type NS (Or Number String))\n" +
			"(fun needN (Number) Number)\n(fun needN (n) n)\n" +
			"(fun needS (String) String)\n(fun needS (s) s)\n" +
			"(var (NS x) 5)\n(if (x.Is? Number) then (needN x) else (needS x))", false},
		{"narrow named union wrong arm", "(type NS (Or Number String))\n" +
			"(fun needN (Number) Number)\n(fun needN (n) n)\n" +
			"(fun needS (String) String)\n(fun needS (s) s)\n" +
			"(var (NS x) 5)\n(if (x.Is? Number) then (needS x) else (needN x))", true},
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
