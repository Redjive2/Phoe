package lint

import "testing"

// `static method`/`static property` declarations must lint cleanly: the
// receiver resolves to the owning struct, and a static method's body sees its
// explicit params plus `Self`. Genuine errors (unknown receiver, body typo)
// still fire, and `static` is permitted at a library's top level.

func TestStaticDeclLintsClean(t *testing.T) {
	srcs := []string{
		"(struct Point.{ x Number y Number })\n(static method Point.at (x y) self.{ x x y y })\n(let p = (Point.at 1 2))\n(let a = p.x)\n",
		"(struct Counter.{ n Number })\n(static property Counter.zero get (method Counter (self) self.{ n 0 }))\n(let z = Counter.zero)\n",
	}
	for i, src := range srcs {
		d := AnalyzeFile("t.pho", []byte(src))
		if len(d) != 0 {
			t.Errorf("case %d: valid static decl should lint clean; got %#v", i, d)
		}
	}
}

func TestStaticAllowedInLibrary(t *testing.T) {
	src := "(struct Point.{ x Number })\n(static method Point.origin () self.{ x 0 })\n"
	d := AnalyzeFile("t.phl", []byte(src))
	if hasDiag(d, "phl-side-effect") {
		t.Errorf("static decls are declarations, not side effects; got %#v", d)
	}
}

func TestStaticDiagnostics(t *testing.T) {
	if d := AnalyzeFile("t.pho", []byte("(static method ghost.at (x) self.{ x x })\n")); !hasDiag(d, "unresolved-identifier") {
		t.Errorf("an unknown static receiver should fire; got %#v", d)
	}
	src := "(struct Point.{ x Number })\n(static method Point.at (x) (bogus_fn x))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); !hasDiag(d, "unresolved-identifier") {
		t.Errorf("a typo in a static body should fire; got %#v", d)
	}
}
