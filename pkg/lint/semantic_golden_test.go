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
		"(struct Point X y)\n" +
		"(method Point.Shift (self n) (+ self.X n))\n" +
		"(fun main (a) (identity do\n" +
		"\t(var p Point.{ X 1 y 2 })\n" +
		"\t(foreach i in (range a) (io.PrintLine i))\n" +
		"))\n" +
		"(const PI 3)\n"

	type tok struct {
		line, startCol, endCol int
		typ                    SemanticTokenType
	}
	want := []tok{
		{1, 2, 8, SemTokKeyword},     // import
		{2, 2, 8, SemTokKeyword},     // struct
		{2, 9, 14, SemTokType},       // Point
		{3, 2, 8, SemTokKeyword},     // method
		{3, 9, 14, SemTokType},       // Point (owner)
		{3, 15, 20, SemTokMethod},    // Shift
		{3, 22, 26, SemTokFunction},  // self
		{3, 27, 28, SemTokParameter}, // n
		{3, 31, 32, SemTokOperator},  // +
		{3, 33, 37, SemTokFunction},  // self (body)
		{3, 38, 39, SemTokProperty},  // X
		{3, 40, 41, SemTokParameter}, // n
		{4, 2, 5, SemTokKeyword},     // fun
		{4, 6, 10, SemTokFunction},   // main
		{4, 12, 13, SemTokParameter}, // a
		{4, 16, 24, SemTokFunction},  // identity
		{4, 25, 27, SemTokKeyword},   // do
		{5, 3, 6, SemTokKeyword},     // var
		{5, 7, 8, SemTokVariable},    // p
		{5, 9, 14, SemTokType},       // Point (constructor call)
		{6, 3, 10, SemTokKeyword},    // foreach
		{6, 11, 12, SemTokVariable},  // i (loop var)
		{6, 13, 15, SemTokKeyword},   // in
		{6, 17, 22, SemTokFunction},  // range (builtin)
		{6, 23, 24, SemTokParameter}, // a
		{6, 27, 29, SemTokNamespace}, // io
		{6, 30, 39, SemTokProperty},  // PrintLine
		{6, 40, 41, SemTokVariable},  // i
		{8, 2, 7, SemTokKeyword},     // const
		{8, 8, 10, SemTokVariable},   // PI
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

// TestSemanticTokensInterpolation pins that identifiers embedded in a
// string's `%...` interpolation chunks are classified — `%name`,
// `%a.b.c`, and `%(call args)` — so the editor highlights them the same
// as ordinary code instead of leaving the whole string opaque. The
// literal text and the `%`/quotes carry no token (they stay string-
// colored via the editor's base highlighting).
func TestSemanticTokensInterpolation(t *testing.T) {
	// One line, so columns are easy to verify. `who` is a parameter;
	// `range` is a builtin; `p` a parameter, `X` a property via dot.
	src := "(fun f (who p) (io.Print 'hi %who n=%(range who) d=%p.X'))\n"

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
