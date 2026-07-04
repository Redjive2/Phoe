package syntax

import (
	"testing"

	"pho/pkg/ast"
)

// The lexer emits a `--@ ` line as an annotation token carrying the body
// text verbatim, distinct from an ordinary skipped comment.
func TestLexAnnotationToken(t *testing.T) {
	tokens, errs := LexPos("--@ (~type str)\n(let m = 'hi')")
	if len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %#v", errs)
	}
	var got *Token
	for i := range tokens {
		if tokens[i].Annot {
			got = &tokens[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("no annotation token produced; tokens=%#v", tokens)
	}
	if got.Value != "(~type str)" {
		t.Fatalf("annotation body = %q, want %q", got.Value, "(~type str)")
	}
}

// A `--@ (form)` line preceding a top-level form is captured and attached
// to that form, with its body re-parsed into a real PNode (here a macro
// call, since the body uses the `name!` shape).
func TestAnnotationCaptured(t *testing.T) {
	tokens, lexErrs := LexPos("--@ (~sig num num)\n(= add (x y) (+ x y))")
	if len(lexErrs) != 0 {
		t.Fatalf("unexpected lex errors: %#v", lexErrs)
	}
	tree, errs := ParsePos(tokens)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %#v", errs)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 top-level form, got %d", len(tree))
	}
	br, ok := tree[0].(*ast.PBranch)
	if !ok {
		t.Fatalf("expected *ast.PBranch, got %T", tree[0])
	}
	if len(br.Annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(br.Annotations))
	}
	ann := br.Annotations[0]
	if ann.Raw != "(~sig num num)" {
		t.Fatalf("annotation Raw = %q, want %q", ann.Raw, "(~sig num num)")
	}
	mc, ok := ann.Form.(*ast.PMacroCall)
	if !ok {
		t.Fatalf("expected annotation Form *ast.PMacroCall, got %T", ann.Form)
	}
	if head, ok := mc.Head.(*ast.PLeaf); !ok || head.Value != "sig" {
		t.Fatalf("expected macro head leaf 'sig', got %#v", mc.Head)
	}
}

// Multiple stacked `--@` lines all attach to the next form, in source order.
func TestAnnotationStacked(t *testing.T) {
	src := "--@ (~sig num)\n--@ (~doc 'adds')\n(= add (x) (+ x 1))"
	tree, errs := ParsePos(mustLex(t, src))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %#v", errs)
	}
	br := tree[0].(*ast.PBranch)
	if len(br.Annotations) != 2 {
		t.Fatalf("expected 2 annotations, got %d", len(br.Annotations))
	}
	if br.Annotations[0].Raw != "(~sig num)" || br.Annotations[1].Raw != "(~doc 'adds')" {
		t.Fatalf("annotation order wrong: %q then %q",
			br.Annotations[0].Raw, br.Annotations[1].Raw)
	}
}

// The lowered runtime tree must be byte-identical with and without the
// annotation present — annotations are pure metadata the runtime never
// sees. dumpTree strips spans, so this compares pure shape.
func TestAnnotationLeavesRuntimeTreeUnchanged(t *testing.T) {
	const decl = "(= add (x y) (+ x y))"
	plain := dumpTree(lower(decl))
	annotated := dumpTree(lower("--@ (~sig Num Num -> Num)\n" + decl))
	if plain != annotated {
		t.Fatalf("annotation changed the lowered tree:\n plain     = %s\n annotated = %s", plain, annotated)
	}
}

// Body positions are mapped back onto the original source: the annotation
// span and the parsed form's leaves point at the real columns.
func TestAnnotationSpanOffset(t *testing.T) {
	// Columns (1-based): 1:- 2:- 3:@ 4:space 5:( 6:~ 7:s 8:i 9:g
	tree, errs := ParsePos(mustLex(t, "--@ (~sig Num Num)\n(= f () ())"))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %#v", errs)
	}
	ann := tree[0].(*ast.PBranch).Annotations[0]
	if ann.Span.StartLine != 1 || ann.Span.StartCol != 5 {
		t.Fatalf("annotation span start = %d:%d, want 1:5", ann.Span.StartLine, ann.Span.StartCol)
	}
	head := ann.Form.(*ast.PMacroCall).Head.(*ast.PLeaf)
	if head.Span.StartLine != 1 || head.Span.StartCol != 7 {
		t.Fatalf("macro head span start = %d:%d, want 1:7", head.Span.StartLine, head.Span.StartCol)
	}
}

// `--@` only triggers when followed by a space/tab. Ordinary comments —
// including a `@` that isn't the marker — stay plain comments and produce
// no annotation token or attachment.
func TestPlainCommentsNotAnnotations(t *testing.T) {
	for _, src := range []string{
		"-- @notmarker (x)\n(= f () ())", // space before @, so `--@` never matches
		"--@nospace (x)\n(= f () ())",    // no space after @
		"-------- divider\n(= f () ())",  // a run of dashes
	} {
		tokens := mustLex(t, src)
		for _, tk := range tokens {
			if tk.Annot {
				t.Fatalf("src %q: unexpected annotation token %q", src, tk.Value)
			}
		}
		tree, _ := ParsePos(tokens)
		if len(tree) != 1 {
			t.Fatalf("src %q: expected 1 form, got %d", src, len(tree))
		}
		if anns := tree[0].(*ast.PBranch).Annotations; len(anns) != 0 {
			t.Fatalf("src %q: expected no annotations, got %d", src, len(anns))
		}
	}
}

// A `--@` with no following form (trailing at EOF) is an error.
func TestAnnotationTrailingNoForm(t *testing.T) {
	if errs := parseAll("(fun f () ())\n--@ (~sig Num)"); !hasMessageContaining(errs, "has no form to annotate") {
		t.Fatalf("expected trailing-annotation error, got %#v", errs)
	}
}

// An annotation before something that isn't a parenthesized form is an error.
func TestAnnotationOnNonForm(t *testing.T) {
	if errs := parseAll("--@ (~sig Num)\n42"); !hasMessageContaining(errs, "may only precede") {
		t.Fatalf("expected non-form error, got %#v", errs)
	}
}

// An empty `--@ ` body is reported rather than silently accepted.
func TestAnnotationEmptyBody(t *testing.T) {
	if errs := parseAll("--@ \n(fun f () ())"); !hasMessageContaining(errs, "empty annotation") {
		t.Fatalf("expected empty-annotation error, got %#v", errs)
	}
}

func mustLex(t *testing.T, src string) []Token {
	t.Helper()
	tokens, errs := LexPos(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected lex errors for %q: %#v", src, errs)
	}
	return tokens
}
