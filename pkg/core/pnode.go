package core

// Positioned AST node types. These live in core (rather than in
// pkg/syntax) so that Evaluate methods can later be hung off them —
// the runtime currently uses ttbranch/ttleaf, but the long-term goal
// is for the runtime to walk PNodes directly so runtime errors can
// carry source positions.
//
// PNode shapes mirror the source closely (no sugar-pass rewriting):
// `&body` parses to a PSigil, not `(block 'body)`; `a.b` parses to
// PDot, not `(. a 'b)`. The exception is `(name! ...)`, which the
// parser folds into a dedicated PMacroCall — the bang isn't a child,
// it's the form's identity.

// Span is a half-open source range. Lines and columns are 1-based.
type Span struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}

// PNode is a positioned AST node. Implementations are *PBranch,
// *PLeaf, *PSigil, *PDot, and *PMacroCall.
type PNode interface {
	GetSpan() Span
}

// PBranch is a parenthesized form, an array literal, or a dict literal.
// Open / Close hold the literal opener and closer ("(", "[", "{").
type PBranch struct {
	Open     string
	Close    string
	Children []PNode
	Span     Span
}

func (b *PBranch) GetSpan() Span { return b.Span }

// PLeaf is a single token in the AST.
type PLeaf struct {
	Value string
	Span  Span
}

func (l *PLeaf) GetSpan() Span { return l.Span }

// PSigil wraps a sigil-prefixed expression — `'expr` (quote) or `&expr`
// (block). Sigil holds the literal sigil character; Inner is the
// wrapped expression.
type PSigil struct {
	Sigil string
	Inner PNode
	Span  Span
}

func (s *PSigil) GetSpan() Span { return s.Span }

// PDot is a left-associative chain of `.` accesses: `a.b.c` parses as
// PDot{LHS: PDot{LHS: a, RHS: b}, RHS: c}. Mirrors the runtime's
// CompressDotLiterals output shape.
type PDot struct {
	LHS  PNode
	RHS  PNode
	Span Span
}

func (d *PDot) GetSpan() Span { return d.Span }

// PMacroCall is `(name! arg1 arg2 ...)` — at runtime it expands to
// `(resume (name 'arg1 'arg2 ...))`. Detected at parse time so the
// `!` isn't a child of a generic PBranch but the shape's identity.
// Head is the callable (always a leaf in well-formed source, but
// kept as PNode for tolerance); Args are the unquoted argument
// nodes; BangSpan is the position of the `!` token.
type PMacroCall struct {
	Head     PNode
	Args     []PNode
	BangSpan Span
	Span     Span
}

func (m *PMacroCall) GetSpan() Span { return m.Span }
