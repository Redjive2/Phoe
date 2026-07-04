package lint

import "testing"

// TestSemanticTokensGolden pins the full semantic-token output for a
// program exercising every special form (import, struct, method, fun,
// var, foreach, const) plus dot access, a dict literal, and a namespaced
// call. It's the behavior guard for refactors that unify the special-form
// dispatch between the diagnostic walker and the semantic-token walker:
// the classification and spans must not shift.
func TestSemanticTokensGolden(t *testing.T) {
	src := "(import 'std/io')\n" +
		"(struct Point x #y)\n" +
		"(let Point.shift (self n) = (+ self.x n))\n" +
		"(fun main (a) (identity do\n" +
		"\t(let var p = Point.{ x = 1 #y = 2 })\n" +
		"\t(foreach i in (range a) (io/print-line i))\n" +
		"))\n" +
		"(let pi = 3)\n"

	type tok struct {
		line, startCol, endCol int
		typ                    SemanticTokenType
	}
	want := []tok{
		{1, 2, 8, SemTokKeyword},     // import
		{2, 2, 8, SemTokKeyword},     // struct
		{2, 9, 14, SemTokType},       // Point
		{3, 2, 5, SemTokKeyword},     // let (clause head)
		{3, 6, 11, SemTokType},       // Point (owner)
		{3, 12, 17, SemTokMethod},    // shift
		{3, 19, 23, SemTokParameter}, // self (the receiver binder)
		{3, 24, 25, SemTokParameter}, // n
		{3, 27, 28, SemTokKeyword},   // = (the clause's body marker)
		{3, 30, 31, SemTokOperator},  // +
		{3, 32, 36, SemTokParameter}, // self (body)
		{3, 37, 38, SemTokProperty},  // x
		{3, 39, 40, SemTokParameter}, // n
		{4, 2, 5, SemTokKeyword},     // fun
		{4, 6, 10, SemTokFunction},   // main
		{4, 12, 13, SemTokParameter}, // a
		{4, 16, 24, SemTokFunction},  // identity
		{4, 25, 27, SemTokKeyword},   // do
		{5, 3, 6, SemTokKeyword},     // let
		{5, 7, 10, SemTokKeyword},    // var (mutability modifier)
		{5, 11, 12, SemTokVariable},  // p
		{5, 15, 20, SemTokType},      // Point (constructor call)
		{6, 3, 10, SemTokKeyword},    // foreach
		{6, 11, 12, SemTokVariable},  // i (loop var)
		{6, 13, 15, SemTokKeyword},   // in
		{6, 17, 22, SemTokFunction},  // range (builtin)
		{6, 23, 24, SemTokParameter}, // a
		{6, 27, 29, SemTokNamespace}, // io (package segment)
		{6, 30, 40, SemTokFunction},  // print-line (final export — a function, not a namespace)
		{6, 41, 42, SemTokVariable},  // i
		{8, 2, 5, SemTokKeyword},     // let
		{8, 6, 8, SemTokVariable},    // pi
	}

	got := SemanticTokens("golden.phl", []byte(src))
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d\ngot: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Span.StartLine != w.line || g.Span.StartCol != w.startCol || g.Span.EndCol != w.endCol || g.Type != w.typ {
			t.Errorf("token %d = {L%d C%d-%d %v}, want {L%d C%d-%d %v}",
				i, g.Span.StartLine, g.Span.StartCol, g.Span.EndCol, g.Type,
				w.line, w.startCol, w.endCol, w.typ)
		}
	}
}

// TestSemanticTokensSlashChain pins that a package path `a/b/c` classifies
// every INTERMEDIATE segment as a namespace but the FINAL export by its kind —
// a function for a kebab-case name, a type for a Title-Kebab-Case one. This
// guards against the whole path reading as @namespace (which italicizes the
// trailing function/type in most themes). Depth is exercised (3 segments) so
// the inner-chain segments are covered, not just the leftmost.
func TestSemanticTokensSlashChain(t *testing.T) {
	src := "(std/core/print-line 1)\n" +
		"(std/io/Reader)\n"

	got := SemanticTokens("slash.phl", []byte(src))

	// text -> classification, resolved by slicing the source at each span.
	byText := map[string]SemanticTokenType{}
	for _, g := range got {
		if g.Span.StartLine < 1 {
			continue
		}
		lines := []string{"", "(std/core/print-line 1)", "(std/io/Reader)"}
		line := lines[g.Span.StartLine]
		byText[line[g.Span.StartCol-1:g.Span.EndCol-1]] = g.Type
	}

	want := map[string]SemanticTokenType{
		"std":        SemTokNamespace, // leftmost package
		"core":       SemTokNamespace, // inner subpackage
		"print-line": SemTokFunction,  // final export (function — NOT a namespace)
		"io":         SemTokNamespace, // inner subpackage
		"Reader":     SemTokType,      // final export (Capitalized → type)
	}
	for text, wt := range want {
		if got := byText[text]; got != wt {
			t.Errorf("%q classified %v, want %v", text, got, wt)
		}
	}
}

// TestSemanticTokensInterpolation pins that identifiers embedded in a
// string's `%...` interpolation chunks are classified — `%name`,
// `%a.b.c`, and `%(call args)` — so the editor highlights them the same
// as ordinary code instead of leaving the whole string opaque. The
// literal text and the `%`/quotes carry no token (they stay string-
// colored via the editor's base highlighting).
func TestSemanticTokensInterpolation(t *testing.T) {
	// One line, so columns are easy to verify. `who` is a parameter;
	// `range` is a builtin; `p` a parameter, `X` a property via dot.
	src := "(let f (who p) = (io.print 'hi %who n=%(range who) d=%p.X'))\n"

	got := SemanticTokens("interp.phl", []byte(src))

	// Collect the tokens that fall inside the string literal.
	type tk struct {
		col, end int
		typ      SemanticTokenType
	}
	strStart := 1 + len("(fun f (who p) (io.Print ") // 1-based col of opening quote
	var inStr []tk
	for _, g := range got {
		if g.Span.StartCol > strStart {
			inStr = append(inStr, tk{g.Span.StartCol, g.Span.EndCol, g.Type})
		}
	}
	// Expected interpolation tokens, in source order: who, len, who, p, X.
	want := []struct {
		text string
		typ  SemanticTokenType
	}{
		{"who", SemTokParameter},
		{"range", SemTokFunction},
		{"who", SemTokParameter},
		{"p", SemTokParameter},
		{"X", SemTokProperty},
	}
	if len(inStr) != len(want) {
		t.Fatalf("got %d interpolation tokens, want %d: %+v", len(inStr), len(want), inStr)
	}
	for i, w := range want {
		seg := src[inStr[i].col-1 : inStr[i].end-1]
		if seg != w.text || inStr[i].typ != w.typ {
			t.Errorf("interp token %d = {%q %v}, want {%q %v}", i, seg, inStr[i].typ, w.text, w.typ)
		}
	}
}
