package lint

import "testing"

// Defaults live in the SIGNATURE — `(optional Type else DEFAULT)` — and the
// clause binds a plain name for the slot (Features.md §1). The sig is
// recognized as a signature (not an impl) and the clause binder resolves.
func TestSigDefaultParamResolvesInBody(t *testing.T) {
	diags := analyze(t, "(fun add (Number (optional Number else 0)) Number)\n(let add (a b) = (+ a b))\n")
	if hasDiag(diags, "unresolved-identifier") || hasDiag(diags, "capitalized-param") {
		t.Fatalf("a sig-defaulted slot's clause binder must resolve, got %#v", diags)
	}
}

// A sig whose defaulted slot carries a malformed `else` (a lowercase non-type
// in the type slot) is NOT read as a signature — it degrades to the impl
// reading and draws the combined-impl redirect rather than silently passing.
func TestSigDefaultMalformedNotASig(t *testing.T) {
	diags := analyze(t, "(fun f (Number (optional b else 0)) Number)\n")
	if len(diags) == 0 {
		t.Fatalf("(optional b else 0) with a non-type slot should not lint clean, got %#v", diags)
	}
}

// The retired impl-side `(or name default)` param gets a pointed redirect to
// the signature's `(optional Type else default)` form.
func TestOrDefaultImplSlotRetired(t *testing.T) {
	for _, src := range []string{
		"(let f (a (or b 0)) = (+ a b))\n",
		"(let f (a (or b)) = a)\n",
	} {
		diags := analyze(t, src)
		if !hasDiag(diags, "bad-default-param") {
			t.Errorf("%q should flag bad-default-param, got %#v", src, diags)
		}
	}
}

// Go-to-definition, hover, and references resolve a defaulted slot's CLAUSE
// binder — both the binding site and the body reference.
func TestSigDefaultNavResolves(t *testing.T) {
	// Line 2 col 12 is the binder `b` in the clause; line 3 col 10 is the body
	// reference `b` in `(+ a b)`.
	src := "(fun f (Number (optional Number else 0)) Number)\n(let f (a b) =\n    (+ a b))\n"

	site, found := DefinitionAt("t.pho", []byte(src), 3, 10)
	if !found {
		t.Fatal("go-to-def on a clause binder reference: not found")
	}
	if site.Span.StartLine != 2 || site.Span.StartCol != 11 {
		t.Errorf("def resolved to %+v, want the binder at line 2 col 11", site.Span)
	}
	if _, _, ok := HoverAt("t.pho", []byte(src), 3, 10); !ok {
		t.Error("hover on a clause binder reference returned nothing")
	}
	// hover on the binding site itself
	if _, _, ok := HoverAt("t.pho", []byte(src), 2, 11); !ok {
		t.Error("hover on the clause binder returned nothing")
	}
	// find-references (also backs document-highlight / rename) sees the binding
	// and the body use — at least two sites.
	if refs := ReferencesAt(".", "t.pho", []byte(src), 2, 11); len(refs) < 2 {
		t.Errorf("references on a clause binder = %d sites, want >= 2 (binding + body use)", len(refs))
	}
}

// In a signature, `optional`/`else` highlight as keywords, the slot type as a
// type, and the clause's binder as a parameter.
func TestSigDefaultSemanticTokens(t *testing.T) {
	src := "(fun f (Number (optional Number else 0)) Number)\n(let f (a b) = b)\n"
	got := SemanticTokens("or.phl", []byte(src))

	var sawOptional, sawElse, sawParam bool
	for _, tk := range got {
		seg := src[lineColToByte(src, tk.Span.StartLine, tk.Span.StartCol):lineColToByte(src, tk.Span.EndLine, tk.Span.EndCol)]
		if seg == "optional" && tk.Type == SemTokKeyword {
			sawOptional = true
		}
		if seg == "else" && tk.Type == SemTokKeyword {
			sawElse = true
		}
		if seg == "b" && tk.Type == SemTokParameter && tk.Span.StartLine == 2 {
			sawParam = true
		}
	}
	if !sawOptional {
		t.Errorf("expected `optional` classified as SemTokKeyword, tokens: %+v", got)
	}
	if !sawElse {
		t.Errorf("expected `else` classified as SemTokKeyword, tokens: %+v", got)
	}
	if !sawParam {
		t.Errorf("expected the clause binder `b` classified as SemTokParameter, tokens: %+v", got)
	}
}
