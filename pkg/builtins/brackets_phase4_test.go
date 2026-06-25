package builtins

import (
	"testing"

	"pho/pkg/core"
)

// TestMapAndStructInitRuntime proves the Phase 4 surface works end-to-end: a
// `[k -> v]` literal builds a real map (index access by key returns the value),
// and the new `P.{ field = value }` struct-construction form builds the struct
// (Doc/PlanV1/Syntax.md, Phase 4).
func TestMapAndStructInitRuntime(t *testing.T) {
	// `[k -> v]` map; `.[key]` (arrow-free, an index) looks up the value.
	if got := core.Stringify(evalProgram(t, "[:a -> 1 :b -> 2].[:b]")); got != "2" {
		t.Fatalf("[k -> v] map indexed by :b = %q, want 2", got)
	}

	// Struct construction with the new `=` form.
	src := "(struct P X Y)\n(const p P.{ X = 10 Y = 20 })\np.X"
	if got := core.Stringify(evalProgram(t, src)); got != "10" {
		t.Fatalf("struct init with '=' : p.X = %q, want 10", got)
	}
}
