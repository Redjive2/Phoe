package lint

import "testing"

// A nested function IMPLEMENTATION written `(let helper (params) = body)` inside a
// function body must open its own param scope for completion — the decl/impl
// split's `=` form, mirroring the nested `(fun …)`/`(method …)` cases in
// bodyScopeFor's recursion.
func TestNestedEqualsImplCompletion(t *testing.T) {
	src := "(let outer (x) = (do\n" +
		"  (let helper (special-arg) = special-arg)\n" +
		"  (helper x)))\n"
	// Cursor inside helper's body (the second `special-arg`), line 2.
	// Line 2: "  (let helper (special-arg) = special-arg)"; the body token starts at
	// col 27, so col 30 lands inside it.
	defs := CompletionsAt("main.pho", []byte(src), 2, 30)
	if !containsName(defs, "special-arg") {
		t.Fatalf("nested `=` impl param should complete in its body, got %v", defNames(defs))
	}
}
