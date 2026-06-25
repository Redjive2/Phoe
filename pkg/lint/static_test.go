package lint

import "testing"

// `static method`/`static property` declarations must lint cleanly: the
// receiver resolves to the owning struct, and a static method's body sees its
// explicit params plus `Self`. Genuine errors (unknown receiver, body typo)
// still fire, and `static` is permitted at a library's top level.

func TestStaticDeclLintsClean(t *testing.T) {
	srcs := []string{
		"(struct Point.{ X Number Y Number })\n(static method Point.At (x y) Self.{ X x Y y })\n(const p (Point.At 1 2))\n(const a p.X)\n",
		"(struct Counter.{ N Number })\n(static property Counter.Zero get (method Counter (Self) Self.{ N 0 }))\n(const z Counter.Zero)\n",
	}
	for i, src := range srcs {
		d := AnalyzeFile("t.pho", []byte(src))
		if len(d) != 0 {
			t.Errorf("case %d: valid static decl should lint clean; got %#v", i, d)
		}
	}
}

func TestStaticAllowedInLibrary(t *testing.T) {
	src := "(struct Point.{ X Number })\n(static method Point.Origin () Self.{ X 0 })\n"
	d := AnalyzeFile("t.phl", []byte(src))
	if hasDiag(d, "phl-side-effect") {
		t.Errorf("static decls are declarations, not side effects; got %#v", d)
	}
}

func TestStaticDiagnostics(t *testing.T) {
	if d := AnalyzeFile("t.pho", []byte("(static method Ghost.At (x) Self.{ X x })\n")); !hasDiag(d, "unresolved-identifier") {
		t.Errorf("an unknown static receiver should fire; got %#v", d)
	}
	src := "(struct Point.{ X Number })\n(static method Point.At (x) (bogusFn x))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); !hasDiag(d, "unresolved-identifier") {
		t.Errorf("a typo in a static body should fire; got %#v", d)
	}
}
