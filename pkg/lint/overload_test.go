package lint

import "testing"

const overloadSrc = "(fun add (Number Number) Number)\n" +
	"(let add (a b) = (+ a b))\n" +
	"(fun add (String String) String)\n" +
	"(let add (a b) = b)\n"

// §9 overloads: several signatures of one name coexist; a call whose argument
// types match SOME overload is clean.
func TestOverloadedSigsCoexist(t *testing.T) {
	srcs := []string{
		overloadSrc + "(add 1 2)\n",
		overloadSrc + "(add 'a' 'b')\n",
	}
	for _, src := range srcs {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean for %q, got %v", src, d)
		}
	}
}

// A call whose KNOWN argument types match NO overload can never dispatch.
func TestNoMatchingOverload(t *testing.T) {
	d := AnalyzeFile("t.pho", []byte(overloadSrc+"(add 1 'b')\n"))
	if !hasDiag(d, "no-matching-overload") {
		t.Fatalf("mixed Number/String matches no overload, got %v", d)
	}
	// An unknown argument type stays gradual — silent.
	clean := AnalyzeFile("t.pho", []byte(overloadSrc+"(let g (x) = (add x x))\n"))
	if hasDiag(clean, "no-matching-overload") {
		t.Fatalf("gradual args must not flag, got %v", clean)
	}
}

// A call whose argument COUNT fits no overload's arity window is flagged.
func TestOverloadArityMismatch(t *testing.T) {
	d := AnalyzeFile("t.pho", []byte(overloadSrc+"(add 1 2 3)\n"))
	if !hasDiag(d, "arity-mismatch") {
		t.Fatalf("3 args fit no overload, got %v", d)
	}
	if d := AnalyzeFile("t.pho", []byte(overloadSrc+"(add 1)\n")); !hasDiag(d, "arity-mismatch") {
		t.Fatalf("1 arg fits no overload, got %v", d)
	}
}

// A sig-declared default widens the arity window: the defaulted slot may be
// omitted at the call site.
func TestOptionalWidensArity(t *testing.T) {
	src := "(fun pad (String (optional Number else 1)) String)\n" +
		"(let pad (s n) = s)\n"
	for _, call := range []string{"(pad 'x')\n", "(pad 'x' 3)\n"} {
		if d := AnalyzeFile("t.pho", []byte(src+call)); len(d) != 0 {
			t.Errorf("expected clean for %q, got %v", call, d)
		}
	}
	if d := AnalyzeFile("t.pho", []byte(src+"(pad)\n")); !hasDiag(d, "arity-mismatch") {
		t.Errorf("0 args below the window, got no arity-mismatch")
	}
	if d := AnalyzeFile("t.pho", []byte(src+"(pad 'x' 1 2)\n")); !hasDiag(d, "arity-mismatch") {
		t.Errorf("3 args above the window, got no arity-mismatch")
	}
}

// A `(spread T)` sig accepts any surplus, each checked against T.
func TestSpreadSigArity(t *testing.T) {
	src := "(fun sum (Number (spread Number)) Number)\n" +
		"(let sum (a rest) = a)\n"
	for _, call := range []string{"(sum 1)\n", "(sum 1 2 3 4)\n"} {
		if d := AnalyzeFile("t.pho", []byte(src+call)); hasDiag(d, "arity-mismatch") {
			t.Errorf("spread sig should admit %q, got %v", call, d)
		}
	}
	if d := AnalyzeFile("t.pho", []byte(src+"(sum)\n")); !hasDiag(d, "arity-mismatch") {
		t.Errorf("required first arg missing — expected arity-mismatch")
	}
}

// Overloads with the SAME result type still type a call's result even when the
// matching overload is ambiguous; differing results stay gradual.
func TestOverloadResultTyping(t *testing.T) {
	sameResult := "(fun size (String) Number)\n(let size (s) = 1)\n" +
		"(fun size (Number) Number)\n(let size (n) = n)\n" +
		"(fun want-s (String) None)\n(let want-s (s) = none)\n" +
		"(let g (x) = (want-s (size x)))\n" // x unknown, but every overload returns Number
	if d := AnalyzeFile("t.pho", []byte(sameResult)); !hasDiag(d, "type-mismatch") {
		t.Errorf("size always returns Number — passing it to want-s must flag, got %v", d)
	}
}
