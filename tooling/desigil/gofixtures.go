package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// MigrateGoFile rewrites the embedded Pho source in a Go file (test
// fixtures live in string literals). It parses the Go with go/parser,
// and for every string literal whose unquoted value parses cleanly as Pho
// AND is changed by Transform, it replaces that literal — re-quoting in the
// original style where possible. Literals that aren't Pho (expected-output
// text, diagnostic codes, English) fail to parse or produce no change, so
// they're left untouched.
//
// Only the literal byte ranges are edited; all other bytes — comments,
// layout, surrounding Go — are preserved. A Go parse error returns the
// source unchanged.
func MigrateGoFile(src string) (string, int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return src, 0, err
	}

	type repl struct {
		start, end int
		text       string
	}
	var repls []repl

	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		migrated, edits, err := Transform(val)
		if err != nil || edits == 0 || migrated == val {
			return true
		}

		// Re-quote: keep the raw backtick form when the original used it
		// and the content has no backtick; otherwise use a Go-escaped
		// interpreted string.
		var quoted string
		if strings.HasPrefix(lit.Value, "`") && !strings.Contains(migrated, "`") {
			quoted = "`" + migrated + "`"
		} else {
			quoted = strconv.Quote(migrated)
		}

		repls = append(repls, repl{
			start: fset.Position(lit.Pos()).Offset,
			end:   fset.Position(lit.End()).Offset,
			text:  quoted,
		})
		return true
	})

	if len(repls) == 0 {
		return src, 0, nil
	}

	sort.Slice(repls, func(i, j int) bool { return repls[i].start > repls[j].start })
	b := []byte(src)
	for _, r := range repls {
		nb := make([]byte, 0, r.start+len(r.text)+(len(b)-r.end))
		nb = append(nb, b[:r.start]...)
		nb = append(nb, r.text...)
		nb = append(nb, b[r.end:]...)
		b = nb
	}
	return string(b), len(repls), nil
}
