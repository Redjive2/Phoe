package lint

import (
	"testing"

	"pho/pkg/builtins"
	"pho/pkg/core"
)

// TestBuiltinNamesMatchRuntime keeps lint.builtinNames in lockstep with the
// names builtins.NewEnv actually registers. The linter can't import
// builtins at runtime (that would drag the whole interpreter + goop into
// the LSP), so the list is maintained by hand — and this test fails the
// moment it drifts from the real builtin set in either direction.
func TestBuiltinNamesMatchRuntime(t *testing.T) {
	env := builtins.NewEnv()

	registered := map[string]bool{}
	for name := range *env.Globals {
		// The mangled internal builtins (the dot accessor, the sequencing
		// primitive behind `do` notation, the string interpolation helpers,
		// the macro-call sugar behind `name!`) are deliberately hidden from
		// user code, so they are intentionally absent from builtinNames.
		switch name {
		case core.Dot, core.Do, core.Strinterp, core.Strcoerce, core.Macrocall:
			continue
		}
		registered[name] = true
	}

	// Names the linter predeclares that are NOT runtime globals: the atoms
	// the leaf evaluator recognizes specially, the `self` soft keyword, and
	// `do` — now a syntactic keyword (the lower pass rewrites it to the
	// hidden core.Do primitive) rather than a directly registered builtin.
	softKeyword := map[string]bool{"True": true, "False": true, "Nil": true, "self": true, "do": true}

	listed := map[string]bool{}
	for _, name := range builtinNames {
		listed[name] = true
	}

	for name := range registered {
		if !listed[name] {
			t.Errorf("builtin %q is registered by builtins.NewEnv but missing from lint.builtinNames", name)
		}
	}
	for name := range listed {
		if !registered[name] && !softKeyword[name] {
			t.Errorf("lint.builtinNames lists %q, which is neither a registered builtin nor a known soft keyword", name)
		}
	}
}
