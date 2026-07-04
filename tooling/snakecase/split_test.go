package main

import "testing"

// splitOnce runs SplitTransform and fails on error.
func splitOnce(t *testing.T, src string) (string, int) {
	t.Helper()
	out, n, err := SplitTransform(src)
	if err != nil {
		t.Fatalf("SplitTransform(%q) error: %v", src, err)
	}
	return out, n
}

func TestSplitFunImpl(t *testing.T) {
	out, n := splitOnce(t, "(fun add (a b) (+ a b))")
	if want := "(= add (a b) (+ a b))"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if n != 1 {
		t.Errorf("edits = %d, want 1", n)
	}
}

func TestSplitMethodImpl(t *testing.T) {
	out, _ := splitOnce(t, "(method Pair.sum (self) (+ self.a self.b))")
	if want := "(= Pair.sum (self) (+ self.a self.b))"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestSplitOperatorNamedFunImpl(t *testing.T) {
	// A fun impl named with a real operator (in looksLikeIdentifier's set) is
	// still an impl; a non-operator symbol like `++` is NOT and is left alone.
	out, _ := splitOnce(t, "(fun + (a b) (Prim-add a b))")
	if want := "(= + (a b) (Prim-add a b))"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if unchanged, _ := splitOnce(t, "(fun ++ (a b) (Concat a b))"); unchanged != "(fun ++ (a b) (Concat a b))" {
		t.Errorf("non-operator ++ should be left alone, got %q", unchanged)
	}
}

func TestSplitLeavesFunSignature(t *testing.T) {
	// All-type params + type return = signature; must NOT be rewritten.
	for _, src := range []string{
		"(fun add (Number Number) Number)",
		"(fun ident (T) T)",
		"(fun make () Widget)", // empty params + type return = 0-arg sig
		"(method Eq.eq? (Self) Boolean)",
		"(method R.m (Self Number) None)",
	} {
		out, n := splitOnce(t, src)
		if out != src || n != 0 {
			t.Errorf("signature %q was rewritten to %q (%d edits); want unchanged", src, out, n)
		}
	}
}

func TestSplitLeavesAnonymousFun(t *testing.T) {
	// Anonymous funs have no name — the codemod does not migrate them.
	src := "(fun (a b) (+ a b))"
	out, n := splitOnce(t, src)
	if out != src || n != 0 {
		t.Errorf("anon fun %q rewritten to %q (%d edits); want unchanged", src, out, n)
	}
}

func TestSplitLeavesReassignAndCalls(t *testing.T) {
	for _, src := range []string{
		"(= x 5)",                 // 2-arg reassignment
		"(fun-call add 1 2)",      // not a decl head
		"(let add = (fun (a) a))", // fun in value position (still anon)
	} {
		out, n := splitOnce(t, src)
		if out != src || n != 0 {
			t.Errorf("%q rewritten to %q (%d edits); want unchanged", src, out, n)
		}
	}
}

func TestSplitLeavesEmptyParamNilReturnImpl(t *testing.T) {
	// `(fun f () none)` is a nil-returning IMPL (empty params, non-type return),
	// so it MUST be rewritten — mirrors isFunSigForm's strict empty-param rule.
	out, n := splitOnce(t, "(fun noop () none)")
	if want := "(= noop () none)"; out != want || n != 1 {
		t.Errorf("got %q (%d edits), want %q (1 edit)", out, n, want)
	}
}

func TestSplitPropertyStructGetter(t *testing.T) {
	src := "(property Process.size\n    get (method Process (self) (deps.PrimSize self))\n)"
	want := "(property Process.size\n    (get (self) (deps.PrimSize self))\n)"
	out, _ := splitOnce(t, src)
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestSplitPropertyFreeGetterAndSetter(t *testing.T) {
	// Free-standing getter (fun delegate) + a setter, both unwrap.
	src := "(property (Number temp) get (fun () backing) set (fun (v) (= backing v)))"
	want := "(property (Number temp) (get () backing) (set (v) (= backing v)))"
	out, _ := splitOnce(t, src)
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestSplitTraitBodyImplsButKeepsSigs(t *testing.T) {
	// Trait bodies follow the split: method SIGS stay, named impls swap.
	src := "(trait Show\n  (method Self.show (Self) String)\n  (method Self.debug (self) (self.show))\n)"
	want := "(trait Show\n  (method Self.show (Self) String)\n  (= Self.debug (self) (self.show))\n)"
	out, _ := splitOnce(t, src)
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestSplitTraitRequirementVsDefault(t *testing.T) {
	// A trait sig-style REQUIREMENT (lowercase `self` receiver + a TYPE return)
	// is NOT a default impl — leave it as `method`. A default impl (non-type
	// body) migrates to `=`.
	req := "(trait Drawable (method self.area (self) Number))"
	if out, n := splitOnce(t, req); out != req || n != 0 {
		t.Errorf("trait requirement rewritten to %q (%d edits); want unchanged", out, n)
	}
	def := "(trait Greeter (method self.hi (self) 'hello'))"
	wantDef := "(trait Greeter (= self.hi (self) 'hello'))"
	if out, _ := splitOnce(t, def); out != wantDef {
		t.Errorf("trait default impl: got %q, want %q", out, wantDef)
	}
	// A capital-Self requirement (all-type params) was already left by isFunSigForm.
	sig := "(trait To (method Self.to (Self) T))"
	if out, n := splitOnce(t, sig); out != sig || n != 0 {
		t.Errorf("trait Self-sig rewritten to %q (%d edits); want unchanged", out, n)
	}
}

func TestSplitLeavesStatic(t *testing.T) {
	src := "(static method Self.from (T) Self)"
	out, n := splitOnce(t, src)
	if out != src || n != 0 {
		t.Errorf("static %q rewritten to %q (%d edits); want unchanged", src, out, n)
	}
}

func TestSplitMultipleForms(t *testing.T) {
	src := "(fun a (x) x)\n(fun sig (Number) Number)\n(method M.b (self) self)"
	want := "(= a (x) x)\n(fun sig (Number) Number)\n(= M.b (self) self)"
	out, n := splitOnce(t, src)
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
	if n != 2 {
		t.Errorf("edits = %d, want 2", n)
	}
}

func TestSplitGoEmbedded(t *testing.T) {
	src := "package p\nvar prog = `(fun add (a b) (+ a b))`\n"
	want := "package p\nvar prog = `(= add (a b) (+ a b))`\n"
	out, n, err := SplitGoFile(src)
	if err != nil {
		t.Fatalf("SplitGoFile error: %v", err)
	}
	if out != want || n != 1 {
		t.Errorf("got %q (%d), want %q (1)", out, n, want)
	}
}

func TestSplitRefusesParseError(t *testing.T) {
	if _, _, err := SplitTransform("(fun add (a b"); err == nil {
		t.Error("expected refusal on unbalanced source, got nil")
	}
}
