package syntax

import "pho/pkg/core"

// Lower converts the source-shaped PNode tree returned by ParsePos
// into the desugared core.Node tree the runtime walks. It applies the
// four transforms the legacy parser ran (block, dot, macro, quote)
// inline so the result is structurally identical to what the old
// syntax.Parse(syntax.Lex(...)) pipeline produced for the same input.
//
// The runtime entry (modload) iterates the returned Branch's children
// and evaluates each as a top-level form — same shape as before.
func Lower(top []core.PNode) core.Node {
	out := make(core.Branch, 0, len(top))
	for _, p := range top {
		out = append(out, lowerNode(p))
	}
	return core.Node(out)
}

// lowerNode converts a single PNode to its desugared Node form.
func lowerNode(p core.PNode) core.Node {
	switch n := p.(type) {
	case *core.PLeaf:
		// String literals with `%name` / `%(expr)` interpolation
		// markers get desugared to a (Strinterp ...) call. Plain
		// strings and non-string leaves fall through unchanged.
		if len(n.Value) >= 2 && n.Value[0] == '"' && n.Value[len(n.Value)-1] == '"' {
			body := n.Value[1 : len(n.Value)-1]
			if HasInterpolation(body) {
				return loweredInterp(body)
			}
		}
		return core.Leaf(n.Value)

	case *core.PBranch:
		// Bracket / brace literals expanded to (slice ...) / (map ...)
		// by the legacy lexer; preserve that shape here.
		switch n.Open {
		case "[":
			out := make(core.Branch, 0, len(n.Children)+1)
			out = append(out, core.Leaf("slice"))
			for _, c := range n.Children {
				out = append(out, lowerNode(c))
			}
			return out
		case "{":
			out := make(core.Branch, 0, len(n.Children)+1)
			out = append(out, core.Leaf("map"))
			for _, c := range n.Children {
				out = append(out, lowerNode(c))
			}
			return out
		default:
			out := make(core.Branch, 0, len(n.Children))
			for _, c := range n.Children {
				out = append(out, lowerNode(c))
			}
			return out
		}

	case *core.PSigil:
		// 'expr → quoted form (leaf wraps in literal "x"; branch wraps
		// children in (slice ...)).
		// &expr → (block <quoted-expr>) — the runtime's `block` builtin
		// expects exactly this shape.
		switch n.Sigil {
		case "'":
			return listifyP(n.Inner)
		case "&":
			return core.Branch{core.Leaf("block"), listifyP(n.Inner)}
		}
		return core.Leaf(n.Sigil)

	case *core.PDot:
		// a.b → (Dot a b). The Dot symbol is mangled at startup so user
		// code can't accidentally call it directly.
		return core.Branch{core.Leaf(core.Dot), lowerNode(n.LHS), lowerNode(n.RHS)}

	case *core.PMacroCall:
		// (head! a b ...) → (resume (head 'a 'b ...)) — i.e. the macro's
		// arguments are quoted and the call result is fed back to the
		// evaluator via `resume`.
		inner := make(core.Branch, 0, len(n.Args)+1)
		inner = append(inner, lowerNode(n.Head))
		for _, a := range n.Args {
			inner = append(inner, listifyP(a))
		}
		return core.Branch{core.Leaf("resume"), inner}
	}
	return nil
}

// listifyP is the PNode-aware ListifyTree: produces the AST shape that
// the legacy quote system would have produced for the given subtree.
//
//	leaf x  → Leaf("\"x\"")              -- the leaf wrapped in literal "
//	(...)   → (slice listify(c1) ...)    -- children re-listified
//	[...]   → lower then re-listify      -- legacy collapses [ to (slice ..
//	                                         BEFORE listification, so we
//	                                         match by lowering first
//	{...}   → ditto for { → (map ..
//	sigil/dot/macro inside a quote: lower (so the syntactic transform
//	still applies) then re-listify the resulting Node.
func listifyP(p core.PNode) core.Node {
	switch n := p.(type) {
	case *core.PLeaf:
		return core.Leaf("\"" + n.Value + "\"")
	case *core.PBranch:
		if n.Open != "(" {
			return listifyNode(lowerNode(n))
		}
		out := make(core.Branch, 0, len(n.Children)+1)
		out = append(out, core.Leaf("slice"))
		for _, c := range n.Children {
			out = append(out, listifyP(c))
		}
		return out
	case *core.PSigil, *core.PDot, *core.PMacroCall:
		return listifyNode(lowerNode(p))
	}
	return nil
}

// listifyNode is the legacy ListifyTree pass on Node. The adapter uses
// it to re-listify already-lowered subtrees (e.g. a sigil sitting
// inside an outer quote, where we have to apply the syntactic
// transform first and then quote the result).
func listifyNode(t core.Node) core.Node {
	if lf, ok := t.(core.Leaf); ok {
		return core.Leaf("\"" + lf + "\"")
	}
	branch := t.(core.Branch)
	out := make(core.Branch, len(branch)+1)
	out[0] = core.Leaf("slice")
	for i, c := range branch {
		out[i+1] = listifyNode(c)
	}
	return out
}
