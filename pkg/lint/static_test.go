package lint

import "testing"

// `static method`/`static property` declarations must lint cleanly: the
// receiver resolves to the owning struct, and a static method's body sees its
// explicit params plus `Self`. Genuine errors (unknown receiver, body typo)
// still fire, and `static` is permitted at a library's top level.

func TestStaticDeclLintsClean(t *testing.T) {
	srcs := []string{
		"(struct Point.{ Number x Number y })\n(static method Point.at (x y) self.{ x = x y = y })\n(let p = (Point.at 1 2))\n(let a = p.x)\n",
		"(struct Counter.{ Number n })\n(static property Counter.zero (get (self) self.{ n = 0 }))\n(let z = Counter.zero)\n",
	}
	for i, src := range srcs {
		d := AnalyzeFile("t.pho", []byte(src))
		if len(d) != 0 {
			t.Errorf("case %d: valid static decl should lint clean; got %#v", i, d)
		}
	}
}

func TestStaticAllowedInLibrary(t *testing.T) {
	src := "(struct Point.{ Number x })\n(static method Point.origin () self.{ x = 0 })\n"
	d := AnalyzeFile("t.phl", []byte(src))
	if hasDiag(d, "phl-side-effect") {
		t.Errorf("static decls are declarations, not side effects; got %#v", d)
	}
}

// A static method SIGNATURE `(static method Recv.M (T…) R)` — all-type params
// and a type return, no body — is a type signature, not an implementation. Its
// slots are TYPES, so it must lint clean (no capitalized-param on `String`/etc.),
// mirroring std/os's `File.open!`. This is the static analogue of a plain-method
// signature `(method R.M (Self) Boolean)`.
func TestStaticMethodSignature(t *testing.T) {
	src := "(struct File.{ Number id })\n" +
		"(static method File.open! (String String (optional Atom else :append)) File)\n" +
		"(let File.open! (path perm mode) = (File.{ id = 0 }))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
		t.Errorf("a static method signature should lint clean; got %#v", d)
	}
	// The one-arg-plus-return shape must not be mistaken for the impl form: a
	// bare type param stays a type, no binding.
	src = "(struct Vec.{ Number x })\n(static method Vec.unit (Number) Vec)\n(let Vec.unit (n) = (Vec.{ x = n }))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
		t.Errorf("a single-type-param static signature should lint clean; got %#v", d)
	}
}

// The semantic-token walker understands a static declaration: the `static`
// keyword paints @keyword, the member name @method, and a signature's type slots
// @type — the same treatment a plain method gets. (Before, static forms fell to
// the generic-call default and were left unclassified.)
func TestStaticSemanticTokens(t *testing.T) {
	line := "(static method File.open! (String) File)"
	src := []byte("(struct File.{ Number id })\n" + line + "\n")
	want := map[string]SemanticTokenType{
		"static": SemTokKeyword, // was mis-painted as @function
		"open!":  SemTokMethod,
		"String": SemTokType, // sig param slot is a type, not a parameter
	}
	seen := map[string]SemanticTokenType{}
	for _, tk := range SemanticTokens("t.pho", src) {
		if tk.Span.StartLine != 2 {
			continue
		}
		a, b := tk.Span.StartCol-1, tk.Span.EndCol-1
		if a < 0 || b > len(line) {
			continue
		}
		if _, ok := want[line[a:b]]; ok {
			seen[line[a:b]] = tk.Type
		}
	}
	for text, wantTok := range want {
		if seen[text] != wantTok {
			t.Errorf("token %q = %v, want %v", text, seen[text], wantTok)
		}
	}
}

func TestStaticDiagnostics(t *testing.T) {
	if d := AnalyzeFile("t.pho", []byte("(static method ghost.at (x) self.{ x = x })\n")); !hasDiag(d, "unresolved-identifier") {
		t.Errorf("an unknown static receiver should fire; got %#v", d)
	}
	src := "(struct Point.{ Number x })\n(static method Point.at (x) (bogus-fn x))\n"
	if d := AnalyzeFile("t.pho", []byte(src)); !hasDiag(d, "unresolved-identifier") {
		t.Errorf("a typo in a static body should fire; got %#v", d)
	}
}
