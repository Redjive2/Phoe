package lint

import (
	"strings"
	"testing"
)

// A slice `xs.[i : j]` preserves the receiver's kind: slicing a string yields a
// string (a substring — the runtime returns core.TvStr), slicing a list yields a
// list. Previously the linter inferred EVERY slice as a list, so a string slice
// was wrongly typed List.
func TestSliceShapePreservesReceiver(t *testing.T) {
	// string slice → String (the bug)
	s, _, ok := HoverAt("t.pho", []byte("(let s = 'hello'.[1 : 3])\n"), 1, 6)
	if !ok {
		t.Fatal("expected a hover on s")
	}
	if !strings.Contains(s, "String") || strings.Contains(s, "List") {
		t.Fatalf("a string slice must infer as String, not List, got:\n%s", s)
	}

	// list slice → List (unchanged)
	a, _, ok := HoverAt("t.pho", []byte("(let a = [1 2 3])\n(let b = a.[0 : 2])\n"), 2, 6)
	if !ok {
		t.Fatal("expected a hover on b")
	}
	if !strings.Contains(a, "List") {
		t.Fatalf("a list slice must infer as List, got:\n%s", a)
	}
}
