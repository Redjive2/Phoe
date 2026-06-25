package syntax

import (
	"os"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
)

// noSpans disables span-wrapper insertion (PHO_NO_SPANS=1): the lowered
// tree is then byte-identical to the pre-diagnostics pipeline. Escape
// hatch for debugging the wrapper mechanism and the lever for the A/B
// equivalence test (program output must not depend on wrappers).
var noSpans = os.Getenv("PHO_NO_SPANS") != ""

// spanned wraps a lowered form with its source span (no-op under
// PHO_NO_SPANS). Only forms are wrapped — never bare leaves — so shape
// checks against head leaves keep working without unwrapping.
func spanned(n core.Node, sp span.Span) core.Node {
	if noSpans {
		return n
	}
	return core.WithSpan(n, sp)
}

// Lower converts the source-shaped PNode tree returned by ParsePos
// into the desugared core.Node tree the runtime walks. It applies the
// four transforms the legacy parser ran (block, dot, macro, quote)
// inline so the result is structurally identical to what the old
// syntax.Parse(syntax.Lex(...)) pipeline produced for the same input —
// except that every form is wrapped in a transparent span carrier
// (core.WithSpan) so runtime errors can point at source positions.
//
// The runtime entry (modload) iterates the returned Branch's children
// and evaluates each as a top-level form — same shape as before.
func Lower(top []ast.PNode) core.Node {
	out := make(core.Branch, 0, len(top))
	for _, p := range top {
		out = append(out, lowerNode(p))
	}
	return core.Node(out)
}

// isDoBoundary reports whether n is a bare keyword leaf that terminates a
// `do`-arm's capture. A `do` inside an if/unless branch must stop at the next
// branch separator — `elif` or `else` — instead of swallowing the sibling
// branches into its body (the "context-aware do" of Doc/Features.md). The
// loop forms put `do` last (`while cond then do …`), so they never reach one.
func isDoBoundary(n ast.PNode) bool {
	lf, ok := n.(*ast.PLeaf)
	if !ok {
		return false
	}
	return lf.Value == "elif" || lf.Value == "else"
}

// splitDoForm implements `do` notation. Within a call form, a bare `do`
// element captures the siblings AFTER it into a single (core.Do …) call, so
// `(head a do x y)` becomes `(head a (core.Do x y))`. The `do` keyword itself
// is replaced by the mangled core.Do head, hiding the sequencing primitive
// from direct use.
//
// At head position the form IS the sequence: `(do x y)` becomes `(core.Do x
// y)` directly — the `do` head is renamed in place rather than wrapping the
// tail, so a leading `do` evaluates its body and yields the last value
// instead of calling the body's result. (No `identity` wrapper needed.)
//
// The capture is context-aware: a non-head `do` stops at the first `elif`/
// `else` boundary keyword (see isDoBoundary), so each arm of
//
//	(if cond then do a b  elif cond then do c  else do d e f)
//
// becomes its own (Do …) block — `do a b` no longer swallows the `elif`
// branch. Everything from the boundary onward stays a sibling of the
// enclosing form and is rescanned for further `do`-arms, so all three arms
// split independently. Children with no bare `do` are returned unchanged.
func splitDoForm(children []ast.PNode, head string) []ast.PNode {
	for i, c := range children {
		// `&do …` block helper: the `&do` sigil turns the rest of the form into
		// a single do-block as the one-arg block's body, so `(list.Map &do a b)`
		// becomes `(list.Map &(do a b))` — a function `&(do a b)`. Like a bare
		// `do`, the capture stops at an elif/else boundary.
		if sig, ok := c.(*ast.PSigil); ok && sig.Sigil == "&" {
			if dlf, ok := sig.Inner.(*ast.PLeaf); ok && dlf.Value == "do" {
				endIdx := len(children)
				for j := i + 1; j < len(children); j++ {
					if isDoBoundary(children[j]) {
						endIdx = j
						break
					}
				}
				rest := children[i+1 : endIdx]
				tail := children[endIdx:]

				doChildren := make([]ast.PNode, 0, len(rest)+1)
				doChildren = append(doChildren, &ast.PLeaf{Value: "do", Span: dlf.Span})
				doChildren = append(doChildren, rest...)
				end := dlf.Span
				if len(rest) > 0 {
					end = rest[len(rest)-1].GetSpan()
				}
				bodySpan := span.Span{
					StartLine: dlf.Span.StartLine, StartCol: dlf.Span.StartCol,
					EndLine: end.EndLine, EndCol: end.EndCol,
				}
				newSig := &ast.PSigil{
					Sigil: "&",
					Inner: &ast.PBranch{Open: "(", Close: ")", Children: doChildren, Span: bodySpan},
					Span: span.Span{
						StartLine: sig.Span.StartLine, StartCol: sig.Span.StartCol,
						EndLine: end.EndLine, EndCol: end.EndCol,
					},
				}

				out := make([]ast.PNode, 0, i+1+len(tail))
				out = append(out, children[:i]...)
				out = append(out, newSig)
				out = append(out, splitDoForm(tail, head)...)
				return out
			}
		}

		lf, ok := c.(*ast.PLeaf)
		if !ok || lf.Value != "do" {
			continue
		}

		headLeaf := &ast.PLeaf{Value: head, Span: lf.Span}

		// Head `do`: rename in place — the enclosing form becomes the (Do …)
		// call itself, no extra nesting. A leading `do` is a standalone block,
		// never an if/unless arm, so it captures to the end (no boundary trim).
		if i == 0 {
			rest := children[i+1:]
			out := make([]ast.PNode, 0, len(rest)+1)
			out = append(out, headLeaf)
			out = append(out, rest...)
			return out
		}

		// Non-head `do`: capture siblings up to the first boundary keyword
		// (an if/unless arm separator) into a single (Do …) sub-call.
		endIdx := len(children)
		for j := i + 1; j < len(children); j++ {
			if isDoBoundary(children[j]) {
				endIdx = j
				break
			}
		}
		rest := children[i+1 : endIdx]
		tail := children[endIdx:]

		doChildren := make([]ast.PNode, 0, len(rest)+1)
		doChildren = append(doChildren, headLeaf)
		doChildren = append(doChildren, rest...)

		end := lf.Span
		if len(rest) > 0 {
			end = rest[len(rest)-1].GetSpan()
		}
		doForm := &ast.PBranch{
			Open:     "(",
			Close:    ")",
			Children: doChildren,
			Span: span.Span{
				StartLine: lf.Span.StartLine, StartCol: lf.Span.StartCol,
				EndLine: end.EndLine, EndCol: end.EndCol,
			},
		}

		// The boundary keyword and everything after it stay siblings; rescan
		// them so a later arm's `do` (e.g. the `elif`/`else` branch) splits too.
		out := make([]ast.PNode, 0, i+1+len(tail))
		out = append(out, children[:i]...)
		out = append(out, doForm)
		out = append(out, splitDoForm(tail, head)...)
		return out
	}
	return children
}

// NormalizeDo applies do-notation across a whole parsed tree, recursively, so
// consumers that walk the un-lowered PNode tree (the linter) see a bare `do`
// as the sequencing form it becomes — `(head a do x y)` → `(head a (do x y))`.
// Without it the linter counts a do-form's raw children and rejects e.g. a fun
// body as over-arity. It keeps the plain `do` head (not the runtime's mangled
// core.Do) so the linter's existing do-handling — var hoisting, arity — still
// matches. Mutates the tree in place and returns it.
func NormalizeDo(tree []ast.PNode) []ast.PNode {
	for i, n := range tree {
		tree[i] = normalizeDoNode(n)
	}
	return tree
}

func normalizeDoNode(n ast.PNode) ast.PNode {
	switch v := n.(type) {
	case *ast.PBranch:
		// Recurse FIRST, then split at this level. The split's `do` head is the
		// literal "do", so re-visiting the form it produces would re-match and
		// re-split forever; normalizing children before splitting means the new
		// do-form is never walked again (its tail was already normalized).
		for i, c := range v.Children {
			v.Children[i] = normalizeDoNode(c)
		}
		if v.Open == "(" {
			v.Children = splitDoForm(v.Children, "do")
		}
	case *ast.PSigil:
		v.Inner = normalizeDoNode(v.Inner)
	case *ast.PDot:
		v.LHS = normalizeDoNode(v.LHS)
		v.RHS = normalizeDoNode(v.RHS)
	case *ast.PMacroCall:
		v.Head = normalizeDoNode(v.Head)
		for i, a := range v.Args {
			v.Args[i] = normalizeDoNode(a)
		}
	}
	return n
}

// lowerNode converts a single PNode to its desugared Node form.
func lowerNode(p ast.PNode) core.Node {
	switch n := p.(type) {
	case *ast.PLeaf:
		// String literals with `%name` / `%(expr)` interpolation
		// markers get desugared to a (Strinterp ...) call. Plain
		// strings and non-string leaves fall through unchanged.
		if core.IsStrLit(n.Value) {
			body := core.StrLitBody(n.Value)
			if HasInterpolation(body) {
				return spanned(loweredInterp(body, n.Span), n.Span)
			}
		}
		return core.Leaf(n.Value)

	case *ast.PBranch:
		// Bracket / brace literals expanded to (slice ...) / (map ...)
		// by the legacy lexer; preserve that shape here.
		switch n.Open {
		case "[":
			// `[…]` is a list literal, EXCEPT when it carries `->` separators —
			// then it's a map literal `[k -> v  …]` (the new map syntax; the old
			// `{k v}` form still lowers to (map …) below during the migration).
			// The arrows are dropped, leaving alternating key/value args, so
			// `[k -> v]` → (map k v) and the empty map `[->]` → (map).
			if bracketIsMap(n.Children) {
				out := make(core.Branch, 0, len(n.Children)+1)
				out = append(out, core.Leaf(core.Map))
				for _, c := range n.Children {
					if lf, ok := c.(*ast.PLeaf); ok && lf.Value == "->" {
						continue
					}
					out = append(out, lowerNode(c))
				}
				return spanned(out, n.Span)
			}
			out := make(core.Branch, 0, len(n.Children)+1)
			out = append(out, core.Leaf(core.Slice))
			for _, c := range n.Children {
				out = append(out, lowerNode(c))
			}
			return spanned(out, n.Span)
		case "{":
			out := make(core.Branch, 0, len(n.Children)+1)
			out = append(out, core.Leaf(core.Map))
			for _, c := range n.Children {
				out = append(out, lowerNode(c))
			}
			return spanned(out, n.Span)
		default:
			// A call form. Apply `do` notation first: a bare `do` element
			// captures every following sibling into a (Do …) sub-call.
			children := splitDoForm(n.Children, core.Do)
			out := make(core.Branch, 0, len(children))
			for _, c := range children {
				out = append(out, lowerNode(c))
			}
			return spanned(out, n.Span)
		}

	case *ast.PSigil:
		// &expr → (block <quoted-expr>) — the `block` builtin makes it a
		// one-argument function whose implicit parameter is `it`. (`&do …` is
		// rewritten to `&(do …)` by splitDoForm before this point.) The `'`
		// quote sigil was retired; the only remaining sigil is `&`.
		switch n.Sigil {
		case "&":
			return spanned(core.Branch{core.Leaf("block"), listifyP(n.Inner)}, n.Span)
		}
		return core.Leaf(n.Sigil)

	case *ast.PDot:
		// a.b → (Dot a b). The Dot symbol is mangled at startup so user
		// code can't accidentally call it directly.
		return spanned(core.Branch{core.Leaf(core.Dot), lowerNode(n.LHS), lowerNode(n.RHS)}, n.Span)

	case *ast.PMacroCall:
		// (~head a b ...) → (Macrocall head 'a 'b ...): the macro's arguments
		// are quoted, and the Macrocall builtin resolves head to a macro,
		// invokes it with the quoted args, and resumes the result. head stays
		// a bare reference (not quoted) so Macrocall can evaluate it to the
		// macro value.
		out := make(core.Branch, 0, len(n.Args)+2)
		out = append(out, core.Leaf(core.Macrocall))
		out = append(out, lowerNode(n.Head))
		for _, a := range n.Args {
			out = append(out, listifyP(a))
		}
		return spanned(out, n.Span)
	}
	return nil
}

// bracketIsMap reports whether a `[…]` literal carries `->` separators, which
// distinguishes a map literal `[k -> v]` from a plain list `[a b c]`.
func bracketIsMap(children []ast.PNode) bool {
	for _, c := range children {
		if lf, ok := c.(*ast.PLeaf); ok && lf.Value == "->" {
			return true
		}
	}
	return false
}

// listifyP is the PNode-aware ListifyTree: produces the AST shape that
// the legacy quote system would have produced for the given subtree.
//
//	leaf x  → Leaf("'x'")               -- the leaf wrapped in literal '
//	(...)   → (slice listify(c1) ...)    -- children re-listified
//	[...]   → lower then re-listify      -- legacy collapses [ to (slice ..
//	                                         BEFORE listification, so we
//	                                         match by lowering first
//	{...}   → ditto for { → (map ..
//	sigil/dot/macro inside a quote: lower (so the syntactic transform
//	still applies) then re-listify the resulting Node.
//
// Quoted branch forms carry the ORIGINAL subform's span: the wrapper is
// invisible when the quoted tree is evaluated as data, and Derepr
// transfers it onto the reconstructed body form — which is how
// fun/method/block bodies keep positions through the quote round-trip.
func listifyP(p ast.PNode) core.Node {
	switch n := p.(type) {
	case *ast.PLeaf:
		// A string literal carrying interpolation must desugar even
		// inside a quote. Fun/method bodies are quoted, so without this
		// `'(... "%x" ...)` would re-quote the raw text and the
		// interpolation would never run — it'd render as literal `%x`.
		// Lower it to the (Strinterp ...) call, then listify THAT, so
		// once the quoted body is Derepr'd and evaluated the
		// interpolation fires. Mirrors the PLeaf case in lowerNode.
		if core.IsStrLit(n.Value) {
			body := core.StrLitBody(n.Value)
			if HasInterpolation(body) {
				return listifyNode(spanned(loweredInterp(body, n.Span), n.Span))
			}
		}
		return core.Leaf("'" + n.Value + "'")
	case *ast.PBranch:
		if n.Open != "(" {
			return listifyNode(lowerNode(n))
		}
		// `do` notation desugars inside quotes too, so a quoted body like
		// `'(identity do x y)` carries the (Do …) structure as data and
		// runs correctly once Derepr'd and evaluated.
		children := splitDoForm(n.Children, core.Do)
		out := make(core.Branch, 0, len(children)+1)
		out = append(out, core.Leaf(core.Slice))
		for _, c := range children {
			out = append(out, listifyP(c))
		}
		return spanned(out, n.Span)
	case *ast.PSigil, *ast.PDot, *ast.PMacroCall:
		return listifyNode(lowerNode(p))
	}
	return nil
}

// listifyNode is the legacy ListifyTree pass on Node. The adapter uses
// it to re-listify already-lowered subtrees (e.g. a sigil sitting
// inside an outer quote, where we have to apply the syntactic
// transform first and then quote the result). Span wrappers transfer
// onto the listified result so the position survives the re-quoting.
func listifyNode(t core.Node) core.Node {
	if sp, ok := core.SpanOf(t); ok {
		return core.WithSpan(listifyNode(core.Strip(t)), sp)
	}
	if lf, ok := t.(core.Leaf); ok {
		return core.Leaf("'" + lf + "'")
	}
	branch := t.(core.Branch)
	out := make(core.Branch, len(branch)+1)
	out[0] = core.Leaf(core.Slice)
	for i, c := range branch {
		out[i+1] = listifyNode(c)
	}
	return out
}
