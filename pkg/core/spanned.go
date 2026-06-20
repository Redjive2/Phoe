package core

// ttspanned is a transparent positioning wrapper around a runtime node.
// syntax.Lower inserts one at every form boundary (call forms, slice/map
// literals, dot chains, blocks, macro calls — never bare leaves, so
// head-leaf comparisons like br[0] == ttleaf("spread") keep working).
// SynthSpans inserts the same wrappers over macro-generated code, with
// spans into the generated source text.
//
// Evaluate stamps the span into the value-copied Context and delegates,
// which scopes the "current span" to this subtree for free: the caller's
// cursor is untouched when the subtree returns, and every error raised
// below reports the innermost enclosing form. Inside macro-generated code
// (ctx.Expand set) it moves the expansion cursor ExpandAt, leaving At
// frozen at the macro call site so the primary excerpt stays there.
//
// Code that pattern-matches node SHAPES must look through the wrapper —
// use AsBranch / AsLeaf / Strip rather than direct type assertions.
type ttspanned struct {
	node ttnode
	span Span
}

func (s *ttspanned) Evaluate(ctx Context) Tval {
	if ctx.Expand != nil {
		ctx.ExpandAt = &s.span
	} else {
		ctx.At = &s.span
	}
	return s.node.Evaluate(ctx)
}

// WithSpan wraps n with a source span. A nil node or zero span returns n
// unchanged, so callers don't need to special-case unknown positions.
func WithSpan(n Node, sp Span) Node {
	if n == nil || sp == (Span{}) {
		return n
	}
	return &ttspanned{node: n, span: sp}
}

// Strip removes any span wrappers, returning the underlying node.
func Strip(n Node) Node {
	for {
		s, ok := n.(*ttspanned)
		if !ok {
			return n
		}
		n = s.node
	}
}

// SpanOf returns the span n is wrapped with, if any.
func SpanOf(n Node) (Span, bool) {
	if s, ok := n.(*ttspanned); ok {
		return s.span, true
	}
	return Span{}, false
}

// AsBranch is the wrapper-aware form of n.(Branch).
func AsBranch(n Node) (Branch, bool) {
	br, ok := Strip(n).(ttbranch)
	return br, ok
}

// AsLeaf is the wrapper-aware form of n.(Leaf).
func AsLeaf(n Node) (Leaf, bool) {
	lf, ok := Strip(n).(ttleaf)
	return lf, ok
}
