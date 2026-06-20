package core

import "testing"

// TestSynthSpansText: the rendered text matches Inspect exactly (so the
// expansion excerpt reads the way the user expects), across the surface
// sugar Inspect handles.
func TestSynthSpansText(t *testing.T) {
	cases := []ttnode{
		ttbranch{ttleaf("fakeFn"), ttleaf("arg")},
		ttbranch{ttleaf("do"), ttbranch{ttleaf("undefinedThing")}},
		ttbranch{ttleaf("slice"), ttleaf("1"), ttleaf("2")},
		ttbranch{ttleaf(Dot), ttleaf("a"), ttleaf("b")},
		ttbranch{ttleaf(Do), ttleaf("1"), ttleaf("2")},
		ttbranch{ttleaf(Macrocall), ttleaf("m"), ttleaf("a"), ttleaf("b")},
		ttleaf("loneLeaf"),
	}
	for _, c := range cases {
		_, text := SynthSpans(c)
		if want := Inspect(c); text != want {
			t.Errorf("SynthSpans text = %q, want Inspect = %q", text, want)
		}
	}
}

// TestInspectUnmanglesDo: the runtime tree carries the mangled core.Do head,
// but rendering un-mangles it back to the readable `do` keyword — the same
// way dot/slice/map render their surface sugar — so the internal name never
// leaks into error messages, (inspect ...), or macro-expansion excerpts.
func TestInspectUnmanglesDo(t *testing.T) {
	tree := ttbranch{ttleaf(Do), ttleaf("1"), ttleaf("2")}
	if got := Inspect(tree); got != "(do 1 2)" {
		t.Errorf("Inspect(core.Do …) = %q, want %q", got, "(do 1 2)")
	}
	if _, text := SynthSpans(tree); text != "(do 1 2)" {
		t.Errorf("SynthSpans text = %q, want %q", text, "(do 1 2)")
	}
	// The rendered tree keeps the mangled head leaf so evaluation/dispatch
	// still recognizes it — only the text un-mangles.
	wrapped, _ := SynthSpans(tree)
	if head := Strip(wrapped).(ttbranch)[0]; head != ttleaf(Do) {
		t.Errorf("head leaf = %v, want the mangled core.Do for dispatch", head)
	}
}

// TestInspectUnmanglesMacrocall: the macro-call sugar renders back with the
// `!` (and the mangled Macrocall head never leaks), like the other sugar.
func TestInspectUnmanglesMacrocall(t *testing.T) {
	tree := ttbranch{ttleaf(Macrocall), ttleaf("mymacro"), ttleaf("a"), ttleaf("b")}
	if got := Inspect(tree); got != "(mymacro! a b)" {
		t.Errorf("Inspect(Macrocall …) = %q, want %q", got, "(mymacro! a b)")
	}
	if _, text := SynthSpans(tree); text != "(mymacro! a b)" {
		t.Errorf("SynthSpans text = %q, want %q", text, "(mymacro! a b)")
	}
}

// TestSynthSpansNestedSpan: a nested form is wrapped with the span it
// occupies in the rendered text, so the expansion caret can narrow to it.
func TestSynthSpansNestedSpan(t *testing.T) {
	// (do (undefinedThing)) — the inner form starts at column 5.
	tree := ttbranch{ttleaf("do"), ttbranch{ttleaf("undefinedThing")}}
	wrapped, text := SynthSpans(tree)
	if text != "(do (undefinedThing))" {
		t.Fatalf("text = %q", text)
	}

	outer, ok := wrapped.(*ttspanned)
	if !ok {
		t.Fatal("top node not wrapped")
	}
	if outer.span != (Span{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: len(text) + 1}) {
		t.Errorf("outer span = %+v", outer.span)
	}

	// The inner (undefinedThing) form: child index 1 of the wrapped branch.
	inner, ok := outer.node.(ttbranch)[1].(*ttspanned)
	if !ok {
		t.Fatal("inner form not wrapped")
	}
	// "(do " is 4 bytes, so the inner form spans columns 5..20 (half-open 21).
	if want := (Span{StartLine: 1, StartCol: 5, EndLine: 1, EndCol: 21}); inner.span != want {
		t.Errorf("inner span = %+v, want %+v", inner.span, want)
	}

	// Heads stay unwrapped leaves so dispatch still works.
	if _, leaf := outer.node.(ttbranch)[0].(ttleaf); !leaf {
		t.Errorf("head should be an unwrapped leaf, got %T", outer.node.(ttbranch)[0])
	}
}
