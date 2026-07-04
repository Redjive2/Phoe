package lint

import "testing"

// The grouped typed-let form `(let [var] (Type name) = value)` is recognized,
// erased, and type-checked — the declared type flows into the value check, and
// the compound/multi variants resolve. The old ungrouped `(let Type name = …)`
// form is rejected.
func TestTypedLetGrouped(t *testing.T) {
	old := EffectCheck
	EffectCheck = false
	defer func() { EffectCheck = old }()

	clean := []string{
		"(let (Number x) = 5)\n(let a = x)",
		"(let var (String s) = 'hi')",
		"(let ((Or Number String) id) = 5)",      // compound type, grouped
		"(let (Number a) = 1  (String b) = 'z')", // multi typed bindings
	}
	for _, src := range clean {
		if d := AnalyzeFile("t.pho", []byte(src)); len(d) != 0 {
			t.Errorf("expected clean, got %v\n  for %q", d, src)
		}
	}
	// The leading type is checked against the value.
	if !hasDiag(AnalyzeFile("t.pho", []byte("(let (Number x) = 'hi')")), "type-mismatch") {
		t.Error("expected type-mismatch for (let (Number x) = 'hi')")
	}
	if !hasDiag(AnalyzeFile("t.pho", []byte("(let var (Boolean b) = 3)")), "type-mismatch") {
		t.Error("expected type-mismatch for (let var (Boolean b) = 3)")
	}
	// The retired ungrouped typed form is rejected.
	if !hasDiag(AnalyzeFile("t.pho", []byte("(let Number g = 7)")), "bad-form-shape") {
		t.Error("the ungrouped form (let Number g = 7) should be rejected")
	}
}
