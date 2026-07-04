// Package ast defines Pho's positioned parse tree — the source-shaped
// syntax tree produced by pkg/syntax and consumed by pkg/lint and the
// language server. It is deliberately separate from the runtime's
// evaluation tree (core.Node): pkg/syntax.Lower converts an ast.PNode
// into a core.Node, applying the sugar passes along the way.
//
// PNode shapes mirror the source closely (no sugar-pass rewriting):
// `&body` parses to a PSigil, not `(block 'body)`; `a.b` parses to PDot,
// not `(. a 'b)`. The exception is `(name! ...)`, which the parser folds
// into a dedicated PMacroCall — the bang isn't a child, it's the form's
// identity.
//
// ast depends only on pkg/span, so it stays free of any runtime coupling.
package ast

import "pho/pkg/span"

// PNode is a positioned AST node. Implementations are *PBranch, *PLeaf,
// *PSigil, *PDot, and *PMacroCall.
type PNode interface {
	GetSpan() span.Span
}

// PBranch is a parenthesized form, an array literal, or a dict literal.
// Open / Close hold the literal opener and closer ("(", "[", "{").
//
// Annotations holds any parse-time annotations (`--@ (form)` comments)
// that immediately preceded this form at the top level. It is nil for the
// overwhelming majority of forms; it never affects lowering (the runtime
// tree is identical with or without annotations) — pkg/annot reads it to
// evaluate the annotations in isolation.
type PBranch struct {
	Open        string
	Close       string
	Children    []PNode
	Span        span.Span
	Annotations []PAnnotation
}

func (b *PBranch) GetSpan() span.Span { return b.Span }

// PAnnotation is a single parse-time annotation captured from a `--@ (form)`
// comment that precedes a top-level form. Form is the annotation expression
// re-parsed from the comment body, with its spans mapped back onto the
// original source; Raw is the verbatim body text; Span covers the body in
// the source. The annotation is evaluated by pkg/annot in a fresh isolated
// environment at parse time — it never becomes part of the runtime tree.
type PAnnotation struct {
	Form PNode
	Raw  string
	Span span.Span
}

// PLeaf is a single token in the AST.
type PLeaf struct {
	Value string
	Span  span.Span
}

func (l *PLeaf) GetSpan() span.Span { return l.Span }

// PSigil wraps a sigil-prefixed expression — `'expr` (quote) or `&expr`
// (block). Sigil holds the literal sigil character; Inner is the wrapped
// expression.
type PSigil struct {
	Sigil string
	Inner PNode
	Span  span.Span
}

func (s *PSigil) GetSpan() span.Span { return s.Span }

// PDot is a left-associative chain of `.` accesses: `a.b.c` parses as
// PDot{LHS: PDot{LHS: a, RHS: b}, RHS: c}. Mirrors the runtime's
// CompressDotLiterals output shape.
type PDot struct {
	LHS  PNode
	RHS  PNode
	Span span.Span
}

func (d *PDot) GetSpan() span.Span { return d.Span }

// PSlash is a left-associative chain of `/` package navigations: `a/b/c`
// parses as PSlash{LHS: PSlash{LHS: a, RHS: b}, RHS: c}. It is the package /
// subpackage / export accessor — distinct from PDot (value/type member
// access). Lowers to the mangled core.Slash accessor. A `/` at list-head
// position (`(/ a b)`) stays the division operator and never folds here.
type PSlash struct {
	LHS  PNode
	RHS  PNode
	Span span.Span
}

func (s *PSlash) GetSpan() span.Span { return s.Span }

// PMacroCall is `(~name arg1 arg2 ...)` — at runtime it lowers to
// `(core.Macrocall name 'arg1 'arg2 ...)`. Detected at parse time so the
// `~` prefix sigil isn't a child of a generic PBranch but the shape's
// identity. Head is the callable (always a leaf in well-formed source, but
// kept as PNode for tolerance); Args are the unquoted argument nodes;
// SigilSpan is the position of the `~` token.
type PMacroCall struct {
	Head      PNode
	Args      []PNode
	SigilSpan span.Span
	Span      span.Span
}

func (m *PMacroCall) GetSpan() span.Span { return m.Span }
