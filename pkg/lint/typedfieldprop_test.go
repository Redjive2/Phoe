package lint

import "testing"

// Typed struct fields use Type-name order, and properties may carry a declared
// type `(property (Type name) …)`; both lint clean and resolve, and field-type
// checking still fires through the new order.
func TestTypeNameFieldsAndTypedProperties(t *testing.T) {
	clean := []string{
		"(struct Point.{ Number x Number y })\n(let p = Point.{ x = 1 y = 2 })\n(let a = p.x)",
		"(struct Box.{ Number n })\n(property (Number Box.area) (get (self) self.n))\n(let b = Box.{ n = 1 })\n(let a = b.area)",
		"(let n = 5)\n(property (Number twice) (get () (* n 2)))\n(let t = twice)",
		"(struct Node.{ Number value (Or Node None) next })\n(let n = Node.{ value = 1 next = none })\n(let v = n.next.value)",
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean, got %v\n  for %q", d, src)
		}
	}
	// record-type argument check still fires through the Type-name order.
	if !hasDiag(AnalyzeFile("t.pho", []byte("(struct P x)\n(fun f (Struct.{ Number x }) None)\n(let f (p) = none)\n(f 5)")), "type-mismatch") {
		t.Error("expected type-mismatch for a non-struct arg against a record type")
	}
}

// Struct field types and a typed property's type paint as @type.
func TestSemanticTokensFieldAndPropertyTypes(t *testing.T) {
	countType := func(toks []SemanticToken, line int) (n int) {
		for _, tk := range toks {
			if tk.Span.StartLine == line && tk.Type == SemTokType {
				n++
			}
		}
		return
	}
	if n := countType(SemanticTokens("t.phl", []byte("(struct Point.{ Number x Number y })")), 1); n != 3 {
		t.Errorf("struct decl: @type=%d, want 3 (Point, Number, Number)", n)
	}
	if n := countType(SemanticTokens("t.phl", []byte("(property (Number twice) (get () 2))")), 1); n != 1 {
		t.Errorf("typed property: @type=%d, want 1 (Number)", n)
	}
}

// A typed property's declared type flows into member access `inst.prop`, so an
// access whose type is incompatible with its use fires a mismatch — the
// property analogue of typed-field access checking.
func TestPropertyAccessTyping(t *testing.T) {
	base := "(struct Box.{ Number w Number h })\n" +
		"(property (Number Box.area) (get (self) (* self.w self.h)))\n" +
		"(fun need-str (String) None)\n(fun need-str (s) none)\n" +
		"(fun need-num (Number) None)\n(fun need-num (n) none)\n" +
		"(let b = Box.{ w = 1 h = 2 })\n"
	if !hasDiag(AnalyzeFile("t.pho", []byte(base+"(need-str b.area)")), "type-mismatch") {
		t.Error("expected type-mismatch passing Number property b.area to a String parameter")
	}
	if hasDiag(AnalyzeFile("t.pho", []byte(base+"(need-num b.area)")), "type-mismatch") {
		t.Error("unexpected type-mismatch passing Number property b.area to a Number parameter")
	}
}
