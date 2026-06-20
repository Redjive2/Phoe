// Command ifmigrate rewrites the old positional `if` forms — (if C T) and
// (if C T E) — into the keyword form (if C then T) / (if C then T else E).
// It parses each file, finds if-branches whose third child isn't already the
// `then` marker, and splices `then `/`else ` in at the arm offsets, leaving
// all other formatting untouched. Already-migrated ifs (and ifs with elif)
// are skipped, so it's idempotent.
package main

import (
	"fmt"
	"os"
	"sort"

	"pho/pkg/ast"
	"pho/pkg/span"
	"pho/pkg/syntax"
)

type insert struct {
	off  int
	text string
}

// offsetOf converts a 1-based (line, col) span start to a byte offset. The
// lexer counts columns in bytes, so this is exact.
func offsetOf(src []byte, sp span.Span) int {
	off, line := 0, 1
	for line < sp.StartLine && off < len(src) {
		if src[off] == '\n' {
			line++
		}
		off++
	}
	return off + sp.StartCol - 1
}

func main() {
	for _, path := range os.Args[1:] {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Println("ERR read", path, err)
			continue
		}
		toks, _ := syntax.LexPos(string(src))
		tree, _ := syntax.ParsePos(toks)
		// Collapse do-notation so a `do`-block arm counts as one child (its
		// synthetic form's span starts at the `do` keyword, which is where the
		// `then`/`else` marker belongs).
		tree = syntax.NormalizeDo(tree)

		var inserts []insert
		var walk func(n ast.PNode)
		walk = func(n ast.PNode) {
			switch v := n.(type) {
			case *ast.PBranch:
				if v.Open == "(" && len(v.Children) >= 3 {
					if h, ok := v.Children[0].(*ast.PLeaf); ok && h.Value == "if" {
						c2, isLeaf := v.Children[2].(*ast.PLeaf)
						alreadyNew := isLeaf && c2.Value == "then"
						// Old positional if is exactly 3 children (C T) or 4 (C T E).
						if !alreadyNew && (len(v.Children) == 3 || len(v.Children) == 4) {
							inserts = append(inserts, insert{offsetOf(src, v.Children[2].GetSpan()), "then "})
							if len(v.Children) == 4 {
								inserts = append(inserts, insert{offsetOf(src, v.Children[3].GetSpan()), "else "})
							}
						}
					}
				}
				for _, c := range v.Children {
					walk(c)
				}
			case *ast.PSigil:
				walk(v.Inner)
			case *ast.PDot:
				walk(v.LHS)
				walk(v.RHS)
			case *ast.PMacroCall:
				walk(v.Head)
				for _, a := range v.Args {
					walk(a)
				}
			}
		}
		for _, form := range tree {
			walk(form)
		}

		// Apply high offsets first so earlier splices don't shift later ones.
		sort.Slice(inserts, func(i, j int) bool { return inserts[i].off > inserts[j].off })
		out := string(src)
		for _, in := range inserts {
			out = out[:in.off] + in.text + out[in.off:]
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Println("ERR write", path, err)
			continue
		}
		fmt.Printf("%s: inserted %d marker(s)\n", path, len(inserts))
	}
}
