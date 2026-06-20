package syntax

import (
	"strings"
	"testing"

	"pho/pkg/ast"
)

// pdump renders a PNode back to a compact source-like string for shape
// assertions (positions and spans are not part of the comparison).
func pdump(n ast.PNode) string {
	switch v := n.(type) {
	case *ast.PLeaf:
		return v.Value
	case *ast.PBranch:
		parts := make([]string, len(v.Children))
		for i, c := range v.Children {
			parts[i] = pdump(c)
		}
		return v.Open + strings.Join(parts, " ") + v.Close
	case *ast.PDot:
		return pdump(v.LHS) + "." + pdump(v.RHS)
	case *ast.PSigil:
		return v.Sigil + pdump(v.Inner)
	default:
		return "<?>"
	}
}

func normalizeToString(t *testing.T, src string) string {
	t.Helper()
	tokens, _ := LexPos(src)
	tree, _ := ParsePos(tokens)
	tree = NormalizeDo(tree)
	parts := make([]string, len(tree))
	for i, n := range tree {
		parts[i] = pdump(n)
	}
	return strings.Join(parts, "\n")
}

// Context-aware do: each `do` arm of an if/unless is bounded by the next
// elif/else, so the three arms split into three independent (do …) blocks
// rather than the first one swallowing the rest.
func TestNormalizeDoStopsAtBranchBoundaries(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"if/elif/else arms split independently",
			`(if c1 then do a b elif c2 then do c else do d e f)`,
			`(if c1 then (do a b) elif c2 then (do c) else (do d e f))`,
		},
		{
			"single then-arm captures to the end",
			`(if c then do a b c)`,
			`(if c then (do a b c))`,
		},
		{
			"unless then-arm stops at else",
			`(unless c then do a b else do x y)`,
			`(unless c then (do a b) else (do x y))`,
		},
		{
			"head do still renames in place (captures to end)",
			`(do a b c)`,
			`(do a b c)`,
		},
		{
			"loop body do captures to the end (no boundary)",
			`(while c then do a b)`,
			`(while c then (do a b))`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeToString(t, tc.src); got != tc.want {
				t.Errorf("NormalizeDo(%s)\n  got  %s\n  want %s", tc.src, got, tc.want)
			}
		})
	}
}
