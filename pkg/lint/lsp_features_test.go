package lint

import "testing"

// Inlay hints surface the inferred type of each let/const/var binding whose
// value has a known shape — top-level and inside function bodies alike — and
// stay silent when the shape is unknown.
func TestInlayHints(t *testing.T) {
	src := "(let a = 5)\n" + // Number
		"(const b 'hi')\n" + // String
		"(let c = [1 2 3])\n" + // List
		"(let f (x) = (do (let n = 99) n))\n" + // in-body: Number
		"(struct P { Number v })\n(let p = P.{ v = 1 })\n" + // struct instance: P
		"(let g (y) = y)\n(let u = (g 1))\n" // unknown result → no hint
	got := map[int]string{}
	for _, h := range InlayHintsAt("t.pho", []byte(src)) {
		got[h.Line] = h.Label
	}
	want := map[int]string{1: ": Number", 2: ": String", 3: ": List", 4: ": Number", 6: ": P"}
	for line, label := range want {
		if got[line] != label {
			t.Errorf("line %d hint = %q, want %q", line, got[line], label)
		}
	}
	if lbl, ok := got[8]; ok {
		t.Errorf("unknown-shape binding should get no hint, got %q", lbl)
	}
}

// Signature help resolves the innermost call to a same-file inline signature,
// rendering it and marking the active parameter (clamped past the last arg).
func TestSignatureHelp(t *testing.T) {
	src := "(fun add (Number Number) Number)\n(let add (a b) = (+ a b))\n(let r = (add 1 2))\n"
	// Cursor on the first argument.
	if h, ok := SignatureHelpAt("t.pho", []byte(src), 3, 14); !ok {
		t.Fatal("expected signature help inside the add call")
	} else {
		if h.Label != "add(Number Number) → Number" {
			t.Errorf("label = %q", h.Label)
		}
		if len(h.Params) != 2 || h.Params[0] != "Number" {
			t.Errorf("params = %v", h.Params)
		}
	}
	// Cursor at/after the last argument stays on the last parameter (clamped).
	if h, ok := SignatureHelpAt("t.pho", []byte(src), 3, 18); !ok || h.ActiveParam != 1 {
		t.Errorf("active param past last arg = %d, want 1 (ok=%v)", h.ActiveParam, ok)
	}
	// Not inside a call with a known signature.
	if _, ok := SignatureHelpAt("t.pho", []byte(src), 1, 3); ok {
		t.Error("no signature help expected outside a known call")
	}
}

// Go-to-implementation on a trait name returns the structs that satisfy it (by
// required-member name) and nothing when a struct is missing a member.
func TestImplementations(t *testing.T) {
	src := "(trait Drawable (method self.draw (self)))\n" +
		"(struct Circle.{ Number r })\n(let Circle.draw (self) = self.r)\n" + // satisfies
		"(struct Box.{ Number s })\n" // no draw → does not satisfy
	sites := ImplementationsAt("t.pho", []byte(src), 1, 9) // cursor on "Drawable"
	if len(sites) != 1 || sites[0].Span.StartLine != 2 {
		t.Fatalf("expected only Circle (line 2), got %#v", sites)
	}
	// Cursor not on a trait → nothing.
	if got := ImplementationsAt("t.pho", []byte(src), 2, 9); len(got) != 0 {
		t.Errorf("a struct name is not a trait; got %#v", got)
	}
}
