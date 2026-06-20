package lint

import (
	"pho/pkg/ast"
	"pho/pkg/span"
)

// if-form normalization.
//
// The `if` special form reads
//
//	(if cond then expr  [elif cond then expr]*  [else expr])
//
// where `then`, `elif`, and `else` are bare keyword markers. Several linter
// passes need to know how those keywords lay out — the reference walker, the
// declaration-hoisting collector, shape inference, and the semantic-token
// classifier — so parseIfForm encodes the layout once and every consumer reads
// the normalized result (mirroring decls.go's role for declarations).

// ifBranch is one `cond then expr` clause. Expr is nil when the clause is
// truncated mid-edit.
type ifBranch struct {
	Cond ast.PNode
	Expr ast.PNode
}

// ifForm is the parsed shape of an if branch list. Bad records the first
// structural problem (empty when well-formed) so the form-shape checker can
// surface it, while Branches/Else/Keywords are populated as far as parsing got
// — so reference checking and hoisting still see every sub-expression even on
// malformed input.
type ifForm struct {
	Branches []ifBranch
	Else     ast.PNode    // nil when there is no else clause
	Keywords []*ast.PLeaf // the then/elif/else marker leaves, for token coloring
	Bad      string       // first structural problem, "" when well-formed
	BadSpan  span.Span    // where Bad applies
}

func (f *ifForm) setBad(msg string, sp span.Span) {
	if f.Bad == "" {
		f.Bad, f.BadSpan = msg, sp
	}
}

// ifKeyword reports whether n is the bare keyword leaf `kw`.
func ifKeyword(n ast.PNode, kw string) (*ast.PLeaf, bool) {
	if lf, ok := n.(*ast.PLeaf); ok && lf.Value == kw {
		return lf, true
	}
	return nil, false
}

// parseIfForm interprets the children of an `if`/`unless` list (br.Children[0]
// is the head). keyword is the head name, used in diagnostics; allowElif is
// true for `if` and false for `unless` (which permits a single `cond then expr`
// clause plus an optional `else`, no `elif`). It extracts as many conditions,
// arms, and the else as it can, and records the first structural surprise in
// Bad.
func parseIfForm(br *ast.PBranch, keyword string, allowElif bool) ifForm {
	var f ifForm
	q := "'" + keyword + "': "
	args := br.Children
	if len(args) > 0 {
		args = args[1:] // drop the head
	}

	for i, n := 0, len(args); i < n; {
		if lf, ok := ifKeyword(args[i], "else"); ok {
			f.Keywords = append(f.Keywords, lf)
			if i+1 < n {
				f.Else = args[i+1]
			}
			if i+2 != n {
				f.setBad(q+"'else' takes exactly one expression and must come last", lf.Span)
			}
			return f
		}

		// A `cond then expr` clause.
		thenLf, ok := ifKeyword(safeAt(args, i+1), "then")
		if !ok {
			f.Branches = append(f.Branches, ifBranch{Cond: args[i]})
			f.setBad(q+"expected 'then' after the condition", args[i].GetSpan())
			return f
		}
		f.Keywords = append(f.Keywords, thenLf)
		b := ifBranch{Cond: args[i]}
		if i+2 < n {
			b.Expr = args[i+2]
		} else {
			f.setBad(q+"'then' must be followed by an expression", thenLf.Span)
		}
		f.Branches = append(f.Branches, b)
		i += 3

		if i >= n {
			return f
		}
		if lf, ok := ifKeyword(args[i], "elif"); ok {
			if !allowElif {
				f.setBad(q+"'elif' is not supported", lf.Span)
				return f
			}
			f.Keywords = append(f.Keywords, lf)
			i++
			if i >= n {
				f.setBad(q+"'elif' must be followed by a condition", lf.Span)
				return f
			}
			continue
		}
		if _, ok := ifKeyword(args[i], "else"); ok {
			continue // handled at the top of the next iteration
		}
		if allowElif {
			f.setBad(q+"expected 'elif' or 'else' between branches", args[i].GetSpan())
		} else {
			f.setBad(q+"expected 'else' after the branch", args[i].GetSpan())
		}
		return f
	}
	return f
}

// safeAt returns nodes[i] or nil when i is out of range.
func safeAt(nodes []ast.PNode, i int) ast.PNode {
	if i < 0 || i >= len(nodes) {
		return nil
	}
	return nodes[i]
}
