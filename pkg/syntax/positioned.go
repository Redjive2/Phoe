package syntax

import (
	"fmt"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/span"
)

// Position-tracking lexer + parser, used by the linter (pkg/lint) and
// eventually the LSP. Distinct from the runtime's Lex/Parse because the
// runtime doesn't need positions and uses a sentinel-based pipeline that
// would mangle them.
//
// The positioned parser deliberately does NOT run the four sugar passes
// (CompressBlock/Dot/Macro/Code). Linters reason about user-written
// syntax, not the desugared form — `&body` should look like `&body`, not
// `(block 'body)` — so the resulting tree mirrors the source.
//
// The PNode tree types live in pkg/ast; the runtime walks a separate
// desugared tree (pkg/core's ttnode), which pkg/syntax/lower.go produces
// from these PNodes. Token and ParseError stay here — they're
// parser-internal artifacts.

// Token is one lexer-produced unit with its source position.
//
// Annot marks the token as a parse-time annotation marker (`--@ ...`)
// rather than a real syntactic token: its Value is the verbatim body
// text after the `--@ ` prefix and its Span covers that body. The parser
// consumes annotation tokens out of band (buffering them onto the form
// that follows); they never reach expression parsing.
type Token struct {
	Value string
	Span  span.Span
	Annot bool
}

// mkSpan is a positional constructor for span.Span. The lexer below
// builds spans dozens of times in identical (sl, sc, el, ec) order;
// using this keeps the call sites tight without the unkeyed-literal
// vet warning that fires for cross-package composite literals.
func mkSpan(sl, sc, el, ec int) span.Span {
	return span.Span{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
}

// ParseError records a malformed-input issue picked up by either the
// lexer or the parser. The lexer flags unrecognized characters; the
// parser flags unclosed groupings, dangling sigils, and stray closers.
// Lint surfaces these as "parse-error" diagnostics.
//
// OpenSpan and Close are set only for missing-closer errors: OpenSpan
// points at the opener whose closer is missing, Span points at the
// inferred close site (where the closer should be inserted), and Close
// is the closer text itself. Stray marks an unexpected closer at the
// top level (Span covers the stray token). The balancer
// (BalanceClosers) and LSP quick fixes are driven by these fields;
// lint only reads Span and Message.
type ParseError struct {
	Span     span.Span
	OpenSpan span.Span
	Close    string
	Stray    bool
	Message  string
}

// ----------------------------------------------------------------------
// Lexer
// ----------------------------------------------------------------------

// LexPos tokenizes source into positioned tokens. Comments are skipped;
// strings, char literals, and backtick-escapes are emitted as single
// tokens. Brackets / braces / parens / sigils are each their own token.
//
// Returns the token stream plus any lex-level errors (currently just
// unrecognized characters — those are dropped from the token stream
// rather than emitted as garbage tokens).
func LexPos(src string) ([]Token, []ParseError) {
	var tokens []Token
	var errs []ParseError
	line, col := 1, 1
	i := 0

	// emitIdent appends an identifier token, enforcing the snake_case /
	// Title_Snake_Case naming rule when StrictNames is on. The token is still
	// emitted on a violation so the parser can keep making progress; the
	// ParseError is what fails the read.
	emitIdent := func(val string, sp span.Span) {
		if StrictNames && classifyIdent(val) == IdentInvalid {
			errs = append(errs, ParseError{
				Span:    sp,
				Message: fmt.Sprintf("non-conforming name %q — values use snake_case, types use Title_Snake_Case", val),
			})
		}
		tokens = append(tokens, Token{Value: val, Span: sp})
	}
	for i < len(src) {
		ch := src[i]

		// Whitespace.
		if ch == ' ' || ch == '\t' || ch == '\r' {
			i++
			col++
			continue
		}
		if ch == '\n' {
			i++
			line++
			col = 1
			continue
		}

		// Line comment `--` to end of line — normally skipped. The one
		// exception is the annotation marker `--@ `: its body (the rest of
		// the line) is captured as an Annot token for pkg/annot rather than
		// discarded. The trailing space is required, so ordinary comments
		// (`-- note`, `--------`, even `--@@@`) stay plain comments.
		if ch == '-' && i+1 < len(src) && src[i+1] == '-' {
			isAnnot := i+3 < len(src) && src[i+2] == '@' &&
				(src[i+3] == ' ' || src[i+3] == '\t')
			if isAnnot {
				i += 3 // past `--@`
				col += 3
				// Skip the whitespace run between `--@` and the body so the
				// captured span starts at the body's first real character.
				for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
					i++
					col++
				}
				bodyLine, bodyCol, bodyStart := line, col, i
				for i < len(src) && src[i] != '\n' {
					i++
					col++
				}
				tokens = append(tokens, Token{
					Value: src[bodyStart:i],
					Span:  mkSpan(bodyLine, bodyCol, line, col),
					Annot: true,
				})
				continue
			}
			for i < len(src) && src[i] != '\n' {
				i++
				col++
			}
			continue
		}

		startLine, startCol := line, col

		// String literal: `"..."` with three skip rules.
		//   1. `` `X `` (backtick pair) — legacy passthrough: the
		//      backtick stays in the resulting string, but the next
		//      byte is taken literally and doesn't terminate the
		//      string. Useful for embedding `(` `)` `"`.
		//   2. `\X` — conventional C-style escapes (`\n`, `\t`, `\"`,
		//      `\\`, ...) translated by the leaf evaluator.
		//   3. `%(...)` — interpolation expression. The OUTER lexer
		//      walks past the matching `)` so that inner code
		//      (including its own `"..."` strings) doesn't end the
		//      outer string. Inner strings can in turn contain
		//      `%(...)`, which makes scanString and scanInterpExpr
		//      mutually recursive (see helpers below).
		if ch == '\'' {
			j, cline, ccol, terminated := scanString(src, i, line, col, ch)
			if !terminated {
				errs = append(errs, ParseError{
					Span:    mkSpan(startLine, startCol, cline, ccol),
					Message: "unterminated string literal",
				})
			}
			tokens = append(tokens, Token{
				Value: src[i:j],
				Span:  mkSpan(startLine, startCol, cline, ccol),
			})
			line, col = cline, ccol
			i = j
			continue
		}

		// Char literal: `` `X` `` — exactly three characters.
		if ch == '`' && i+2 < len(src) && src[i+2] == '`' {
			tokens = append(tokens, Token{
				Value: src[i : i+3],
				Span:  mkSpan(startLine, startCol, line, col+3),
			})
			i += 3
			col += 3
			continue
		}

		// Lone backtick — recognized, but malformed. Char literals must be
		// `X`. Reporting this specifically beats the generic "unrecognized
		// character" message that would otherwise fire below.
		if ch == '`' {
			errs = append(errs, ParseError{
				Span:    mkSpan(startLine, startCol, line, col+1),
				Message: "stray '`' — char literals must be `X`",
			})
			i++
			col++
			continue
		}

		// Atom literal: ':' GLUED (no space) to an identifier or digit run,
		// e.g. `:fast` or `:01213`. A free-standing ':' (followed by space,
		// ']', ')', etc.) is left for the structural branch below, where it
		// stays the slice/range separator — so slices must space their colon
		// (`xs.[1 : 2]`). The atom body mirrors identifier lexing, including
		// an optional trailing '?'; the leaf evaluator validates the form.
		if ch == ':' && i+1 < len(src) && (isIdentStart(src[i+1]) || isDigit(src[i+1])) {
			j := scanIdentBody(src, i+1)
			if j < len(src) && src[j] == '?' {
				j++
			}
			tokens = append(tokens, Token{
				Value: src[i:j],
				Span:  mkSpan(startLine, startCol, line, col+(j-i)),
			})
			col += j - i
			i = j
			continue
		}

		// Single-character punctuation that always tokenizes alone.
		if isStructural(ch) {
			tokens = append(tokens, Token{
				Value: string(ch),
				Span:  mkSpan(startLine, startCol, line, col+1),
			})
			i++
			col++
			continue
		}

		// Negative number: `-` glued to digits, no whitespace.
		if ch == '-' && i+1 < len(src) && isDigit(src[i+1]) {
			j := i + 1
			for j < len(src) && isDigit(src[j]) {
				j++
			}
			tokens = append(tokens, Token{
				Value: src[i:j],
				Span:  mkSpan(startLine, startCol, line, col+(j-i)),
			})
			col += j - i
			i = j
			continue
		}

		// Number literal.
		if isDigit(ch) {
			j := i
			for j < len(src) && isDigit(src[j]) {
				j++
			}
			tokens = append(tokens, Token{
				Value: src[i:j],
				Span:  mkSpan(startLine, startCol, line, col+(j-i)),
			})
			col += j - i
			i = j
			continue
		}

		// Private identifier: '#' glued to an identifier body, e.g. `#secret`
		// or `#Secret_Type`. The '#' marks a private binding/member and is part
		// of the name token. A lone '#' (not followed by an identifier start)
		// is malformed.
		if ch == '#' {
			if i+1 < len(src) && isIdentStart(src[i+1]) {
				j := scanIdentBody(src, i+1)
				if j < len(src) && src[j] == '?' {
					j++
				}
				if j < len(src) && src[j] == '!' {
					j++
				}
				emitIdent(src[i:j], mkSpan(startLine, startCol, line, col+(j-i)))
				col += j - i
				i = j
				continue
			}
			errs = append(errs, ParseError{
				Span:    mkSpan(startLine, startCol, line, col+1),
				Message: "stray '#' — '#' must prefix a private name like #secret",
			})
			i++
			col++
			continue
		}

		// Identifier or word-like operator. Optional trailing effect suffixes are
		// part of the identifier, always in the order `name?!=`:
		//   '?' — predicate convention (`atom?`)
		//   '!' — ENVIRONMENTAL effect (io / randomness / module-global write)
		//   '=' — SELF/VALUE mutation (mutates a `(var self)` or `(var arg)`,
		//         e.g. `append=`) — the value counterpart of '!'.
		// The '=' is consumed only when it's a LONE '=' (not the start of '=='),
		// so the equality operator still lexes on its own.
		if isIdentStart(ch) {
			j := scanIdentBody(src, i+1)
			if j < len(src) && src[j] == '?' {
				j++
			}
			if j < len(src) && src[j] == '!' {
				j++
			}
			if j < len(src) && src[j] == '=' && (j+1 >= len(src) || src[j+1] != '=') {
				j++
			}
			emitIdent(src[i:j], mkSpan(startLine, startCol, line, col+(j-i)))
			col += j - i
			i = j
			continue
		}

		// Multi-char operators (==, ~=, <=, >=, ->). Try two-char match first.
		// `->` is the map-literal key/value separator: `[k -> v]`.
		if i+1 < len(src) {
			two := src[i : i+2]
			if two == "==" || two == "~=" || two == "<=" || two == ">=" || two == "->" {
				tokens = append(tokens, Token{
					Value: two,
					Span:  mkSpan(startLine, startCol, line, col+2),
				})
				i += 2
				col += 2
				continue
			}
		}

		// Single-char operators. `=` belongs here too — `=` alone is the
		// assignment builtin, distinct from the equality operator `==`.
		if strings.ContainsRune("+-*/<>~=", rune(ch)) {
			tokens = append(tokens, Token{
				Value: string(ch),
				Span:  mkSpan(startLine, startCol, line, col+1),
			})
			i++
			col++
			continue
		}

		// Unrecognized character: don't emit a token — the parser
		// shouldn't have to deal with garbage. Record as a lex error.
		errs = append(errs, ParseError{
			Span:    mkSpan(startLine, startCol, line, col+1),
			Message: fmt.Sprintf("unrecognized character %q", ch),
		})
		i++
		col++
	}
	return tokens, errs
}

// scanIdentBody advances from j over an identifier body: letters and digits,
// plus an INTERIOR `-` — a `-` immediately followed by another letter or digit,
// so kebab-case names like `print-line` and `foo-5` scan as ONE token. A `-`
// that is NOT followed by an ident char is left for its own branch, keeping
// the minus operator `(- a b)`, negative numbers `-5`, the `--` comment, the
// `->` map arrow, and a trailing `-` all intact. Returns the index past the
// body (before any `?`/`!`/`=` effect suffix).
func scanIdentBody(src string, j int) int {
	for j < len(src) {
		switch {
		case isIdentCont(src[j]):
			j++
		case src[j] == '-' && j+1 < len(src) && (isIdentStart(src[j+1]) || isDigit(src[j+1])):
			j++
		default:
			return j
		}
	}
	return j
}

// isStructural reports whether ch is a token-by-itself punctuation
// character that doesn't combine with adjacent characters.
func isStructural(ch byte) bool {
	switch ch {
	case '(', ')', '[', ']', '{', '}', '&', '.', ':':
		return true
	}
	return false
}

func isDigit(ch byte) bool      { return ch >= '0' && ch <= '9' }
func isIdentStart(ch byte) bool { return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') }
func isIdentCont(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch)
}

// isBareWord reports whether s is a plain identifier token — the shape a
// struct-construction field name takes in `T.{ field value }`. It decides
// which brace keys get quoted into field-name string literals during
// construction parsing; non-identifier keys (already a string, a quoted
// symbol, or a computed form) pass through untouched.
// structInitHasEq reports whether a `LHS.{ … }` brace body uses the new
// `field = value` triple form (an `=` marker in the second slot) rather than
// the old `field value` pair form.
func structInitHasEq(children []ast.PNode) bool {
	if len(children) < 2 {
		return false
	}
	lf, ok := children[1].(*ast.PLeaf)
	return ok && lf.Value == "="
}

// quoteFieldKey turns a bare field-name leaf (including a private `#field`)
// into a string literal so the struct constructor reads it as a field name;
// any non-bare-word node passes through unchanged.
func quoteFieldKey(n ast.PNode) ast.PNode {
	if lf, ok := n.(*ast.PLeaf); ok && isBareWord(lf.Value) {
		return &ast.PLeaf{Value: `'` + lf.Value + `'`, Span: lf.Span}
	}
	return n
}

// isFieldTypePos reports whether the first element of a `.{ … }` pair sits in
// TYPE position — i.e. the pair is a typed field `Type name` (a struct
// declaration `(struct P.{ Number x })` or a record type `Struct.{ Number x }`)
// rather than a `name value` construction `P.{ x 1 }`. A type is a compound
// form (`(Or …)`, `(List …)`, a qualified `pkg.T`, a nested `Struct.{ … }`) or a
// Title-cased identifier; a field name is a lower-case identifier (optionally
// `#`-private). This lets the brace sugar quote the NAME — not the type — in
// either shape, keeping the type expression live for the constructor/checker.
func isFieldTypePos(n ast.PNode) bool {
	lf, ok := n.(*ast.PLeaf)
	if !ok {
		return true // a form/dot is a type expression
	}
	s := lf.Value
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	// A field NAME is a lower-case identifier (the construction `name value`
	// case). Anything else in first position is a type: a Title-cased name, or a
	// numeric/string/atom singleton type (`Struct.{ 5 x }`, `Struct.{ :ok tag }`).
	return !(s[0] >= 'a' && s[0] <= 'z')
}

func isBareWord(s string) bool {
	if s == "" {
		return false
	}
	// A private member name `#field` is a bare word too: the leading '#' is
	// part of the name token (Doc/PlanV1/Syntax.md).
	if s[0] == '#' {
		s = s[1:]
	}
	// A field name may carry the predicate '?' and/or effect '!' suffix,
	// always in the order `name?!` (mirrors core.IsIdent). Peel them before
	// checking the identifier body so keys like `is-fish?` are quoted as
	// field names in construction rather than left as identifier references.
	if len(s) > 0 && s[len(s)-1] == '!' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '?' {
		s = s[:len(s)-1]
	}
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	// An interior '-' is part of a kebab-case name (`my-field`), matching the
	// identifier grammar `[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]+)*`. The lexer has
	// already produced this as a single token, so accepting '-' as a
	// continuation char here suffices to quote hyphenated construction keys.
	for i := 1; i < len(s); i++ {
		if !isIdentCont(s[i]) && s[i] != '-' {
			return false
		}
	}
	return true
}

// ----------------------------------------------------------------------
// Parser
// ----------------------------------------------------------------------

// ParsePos turns a token stream into a slice of top-level positioned
// nodes plus any parse errors recovered along the way. Unbalanced
// brackets, dangling sigils, and stray closers are tolerated — the
// result is the best tree we could build; the errors describe the
// rough patches.
//
// Recovery from a missing closer is indentation-guided: an unclosed
// form is cut off at the first line-leading token at or left of the
// opener's line indent (see parseGrouping). Balanced input is never
// affected — the rule only fires when the form's closer genuinely
// does not appear later in the stream.
//
// Sugar passes (CompressBlock/Dot/Macro/Code) are NOT run. The linter
// wants to see source-shaped trees.
func ParsePos(tokens []Token) ([]ast.PNode, []ParseError) {
	p := newPosParser(tokens)
	var out []ast.PNode
	var pending []ast.PAnnotation
	for !p.atEnd() {
		t := p.peek()

		// Annotation marker (`--@ (form)`): buffer it and attach to the
		// next top-level form. Stacked annotations accumulate in order.
		if t.Annot {
			p.advance()
			pending = append(pending, p.parseAnnotation(t))
			continue
		}

		// Defensive: a stray closer at top level is an error and would
		// otherwise spin in parseExpr → parsePrimary → consume nothing.
		if t.Value == ")" || t.Value == "]" || t.Value == "}" {
			p.errs = append(p.errs, ParseError{
				Span:    t.Span,
				Stray:   true,
				Message: fmt.Sprintf("unexpected %q at top level", t.Value),
			})
			p.advance()
			continue
		}

		form := p.parseExpr()
		if len(pending) > 0 {
			p.attachAnnotations(form, pending)
			pending = nil
		}
		out = append(out, form)
	}

	// Annotations with no following form (e.g. a trailing `--@` at EOF).
	for _, a := range pending {
		p.errs = append(p.errs, ParseError{
			Span:    a.Span,
			Message: "annotation '--@' has no form to annotate",
		})
	}
	return out, p.errs
}

// attachAnnotations records the buffered annotations on the form they
// precede. Annotations may only decorate a parenthesized form (the
// declaration shapes `(fun ...)`, `(const ...)`, `(struct ...)`, ...);
// a bare atom, an array/dict literal, or a macro call is reported and the
// annotations are dropped.
func (p *posParser) attachAnnotations(form ast.PNode, anns []ast.PAnnotation) {
	if br, ok := form.(*ast.PBranch); ok && br.Open == "(" {
		br.Annotations = append(br.Annotations, anns...)
		return
	}
	for _, a := range anns {
		p.errs = append(p.errs, ParseError{
			Span:    a.Span,
			Message: "annotation '--@' may only precede a '(...)' form",
		})
	}
}

// parseAnnotation turns an annotation marker into a PAnnotation by
// re-lexing and re-parsing its body as a standalone form. The sub-parse
// runs in the body's own coordinate space; every position it produces is
// shifted back onto the original source (offsetSpan) so diagnostics and
// the parsed Form point at the real columns. Lex/parse problems inside the
// body surface as errors against the enclosing file.
func (p *posParser) parseAnnotation(marker Token) ast.PAnnotation {
	ann := ast.PAnnotation{Raw: marker.Value, Span: marker.Span}

	bodyToks, lexErrs := LexPos(marker.Value)
	for k := range bodyToks {
		bodyToks[k].Span = offsetSpan(bodyToks[k].Span, marker.Span.StartLine, marker.Span.StartCol)
	}
	// Lex errors carry body-relative spans; relocate them. Parse errors
	// are derived from the already-shifted tokens, so they are absolute.
	for k := range lexErrs {
		lexErrs[k].Span = offsetSpan(lexErrs[k].Span, marker.Span.StartLine, marker.Span.StartCol)
	}
	forms, parseErrs := ParsePos(bodyToks)
	p.errs = append(p.errs, lexErrs...)
	p.errs = append(p.errs, parseErrs...)

	switch len(forms) {
	case 0:
		p.errs = append(p.errs, ParseError{
			Span:    marker.Span,
			Message: "empty annotation '--@'",
		})
	case 1:
		ann.Form = forms[0]
	default:
		ann.Form = forms[0]
		p.errs = append(p.errs, ParseError{
			Span:    forms[1].GetSpan(),
			Message: "annotation '--@' must contain exactly one form",
		})
	}
	return ann
}

// offsetSpan relocates a body-relative span onto the original source. An
// annotation body is always a single line (the lexer captures it up to the
// newline), so every body position sits on relative line 1: line 1 maps to
// the body's origin line with columns shifted by the origin column. A
// deeper relative line (defensive — should not occur) shifts by line only.
func offsetSpan(s span.Span, baseLine, baseCol int) span.Span {
	shift := func(l, c int) (int, int) {
		if l == 1 {
			return baseLine, baseCol + (c - 1)
		}
		return baseLine + (l - 1), c
	}
	out := s
	out.StartLine, out.StartCol = shift(s.StartLine, s.StartCol)
	out.EndLine, out.EndCol = shift(s.EndLine, s.EndCol)
	return out
}

// maxParseDepth bounds how deeply the recursive-descent parser will
// nest grouped forms and sigils. Real code nests a handful deep; this
// limit only ever trips on pathological input (a runaway paste, a
// generated blob, a mid-edit avalanche of `(`). Without it, deeply
// nested input recurses the parser — and then every downstream walker
// (lint, semantic tokens, navigation) — until the Go stack overflows,
// which is a FATAL crash that recover() cannot catch and that would
// take the whole language server down. Capping the tree depth here
// bounds the recursion everywhere downstream at once.
const maxParseDepth = 1000

type posParser struct {
	tokens []Token
	i      int
	errs   []ParseError

	// depth is the current grouped-form / sigil nesting depth, bounded
	// by maxParseDepth. depthCapped records that the cap was hit so the
	// "nested too deeply" error is reported once, not once per token.
	depth       int
	depthCapped bool

	// leading[k] is true when token k is the first token on its line —
	// no earlier token occupies any part of its start line. indent[k]
	// is the column of the line-leading token of token k's start line
	// (tokens after a multi-line string inherit the string's start-line
	// indent). Both drive the indentation-guided recovery in
	// parseGrouping. The lexer counts a tab as one column, so files
	// that mix tabs and spaces compare indents tab-as-one-column; a
	// consistently-indented file is always self-consistent.
	leading []bool
	indent  []int
}

func newPosParser(tokens []Token) *posParser {
	leading, indent := lineInfo(tokens)
	return &posParser{tokens: tokens, leading: leading, indent: indent}
}

// lineInfo computes, for every token, whether it is the first token on
// its line and the indent of that line (the line-leading token's start
// column). Tokens after a multi-line string on the string's end line
// inherit the string's start-line indent. Shared by the parser's
// recovery rule and the closer balancer.
func lineInfo(tokens []Token) (leading []bool, indent []int) {
	leading = make([]bool, len(tokens))
	indent = make([]int, len(tokens))
	cur := 1
	for k, t := range tokens {
		if k == 0 || tokens[k-1].Span.EndLine < t.Span.StartLine {
			leading[k] = true
			cur = t.Span.StartCol
		}
		indent[k] = cur
	}
	return leading, indent
}

func (p *posParser) atEnd() bool { return p.i >= len(p.tokens) }

func (p *posParser) peek() Token {
	if p.atEnd() {
		return Token{}
	}
	return p.tokens[p.i]
}

func (p *posParser) advance() Token {
	t := p.tokens[p.i]
	p.i++
	return t
}

func (p *posParser) parseExpr() ast.PNode {
	expr := p.parsePrimary()
	// Build a left-associative access chain: while the next token is `.`
	// (value/type member) or `/` (package/subpackage navigation), consume it
	// and pair with the next primary. `a.b.c` → PDot{PDot{a,b},c}; `a/b/c` →
	// PSlash{PSlash{a,b},c}; the two interleave (`pctl/stdout.write!`). A `/`
	// at list-head — `(/ a b)` — is read by parsePrimary as a leaf (division)
	// and never reaches this loop.
	for !p.atEnd() && (p.peek().Value == "." || p.peek().Value == "/") {
		sep := p.peek().Value
		sepTok := p.advance() // consume '.' or '/'
		if p.atEnd() {
			p.errs = append(p.errs, ParseError{
				Span:    sepTok.Span,
				Message: "missing expression after '" + sep + "'",
			})
			break
		}
		// The token after the separator must be able to start a primary.
		// Closers can't, and consuming them here would steal them from the
		// surrounding form (e.g. `args.)` would eat the `)` that belongs to
		// the enclosing parenthesized form).
		var rhs ast.PNode
		switch p.peek().Value {
		case ")", "]", "}", "!":
			p.errs = append(p.errs, ParseError{
				Span:    sepTok.Span,
				Message: "missing expression after '" + sep + "'",
			})
			return expr
		case ".":
			// Consecutive dots — `args...` and similar. Each extra `.`
			// becomes a literal leaf in the chain so the legacy variadic
			// syntax keeps producing the expected tree shape.
			tok := p.advance()
			rhs = &ast.PLeaf{Value: tok.Value, Span: tok.Span}
		default:
			rhs = p.parsePrimary()
		}
		span := expr.GetSpan()
		span.EndLine = rhs.GetSpan().EndLine
		span.EndCol = rhs.GetSpan().EndCol
		if sep == "/" {
			// `/` navigates a package/subpackage down to an export — no
			// struct-construction sugar (that is a `.`-only form).
			expr = &ast.PSlash{LHS: expr, RHS: rhs, Span: span}
			continue
		}
		// `LHS.{ field value … }` is struct-construction sugar. The brace's
		// keys are BARE field names, not values: rewrite each even-position
		// key from a bare identifier into a string literal, then splice the
		// pairs straight into a call of the LHS — `Point.{ X 10 }` becomes
		// `(Point "X" 10)`. The constructor reads alternating name/value
		// arguments (see builtins/decl.go). This is deliberately a different
		// shape from a call with a single brace argument, so the retired
		// `(LHS { … })` form is no longer a way to construct a struct.
		if br, ok := rhs.(*ast.PBranch); ok && br.Open == "{" {
			children := make([]ast.PNode, 0, len(br.Children)+1)
			children = append(children, expr)
			if structInitHasEq(br.Children) {
				// New `{ field = value … }`: name/value triples, drop the `=`.
				for i := 0; i+2 < len(br.Children); i += 3 {
					children = append(children, quoteFieldKey(br.Children[i]), br.Children[i+2])
				}
			} else {
				// `{ … }` pairs — a typed field list `{ Type name … }` (struct
				// decl / record type): the first element of each pair is a Type
				// (kept live), the second is the field name (quoted so the
				// decl/constructor reads it as a string) → `(LHS Type 'name')`.
				//
				// A bare CONSTRUCTION pair `{ field value … }` (first element a
				// lowercase field name, not a type) is RETIRED: construction must
				// use the explicit `{ field = value }` form (structInitHasEq
				// above) so it is unambiguous versus a typed field list. Flag it
				// once, but still desugar it the old way for error recovery.
				flaggedBare := false
				for i := 0; i < len(br.Children); i += 2 {
					a := br.Children[i]
					if i+1 >= len(br.Children) {
						children = append(children, quoteFieldKey(a)) // trailing odd element
						break
					}
					b := br.Children[i+1]
					if isFieldTypePos(a) {
						children = append(children, a, quoteFieldKey(b)) // Type name — typed field
						continue
					}
					if !flaggedBare {
						p.errs = append(p.errs, ParseError{
							Span:    a.GetSpan(),
							Message: "struct construction must use 'field = value'; the bare 'field value' form is no longer allowed",
						})
						flaggedBare = true
					}
					children = append(children, quoteFieldKey(a), b) // name value — retired; desugared for recovery
				}
			}
			expr = &ast.PBranch{
				Open:     "(",
				Close:    ")",
				Children: children,
				Span:     span,
			}
			continue
		}
		expr = &ast.PDot{LHS: expr, RHS: rhs, Span: span}
	}
	return expr
}

// parsePrimary handles a single non-dot expression: a grouped form, a
// sigil-prefixed expression, or an atom. Callers (top-level loop,
// parseGrouping, parseSigil, parseExpr's dot loop) all filter closers
// before recursing, so a closer cannot reach here in practice.
func (p *posParser) parsePrimary() ast.PNode {
	// Depth guard: every nesting level (grouped form or sigil) reaches
	// here, so capping depth here bounds the whole recursive descent —
	// and, because the tree it builds is no deeper than this, every
	// downstream walker too. Past the cap we stop descending and consume
	// the token as a flat leaf, which still makes forward progress (the
	// caller's loop keeps draining tokens) without growing the stack.
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		if !p.depthCapped {
			p.depthCapped = true
			p.errs = append(p.errs, ParseError{
				Span:    p.peek().Span,
				Message: fmt.Sprintf("expression nested too deeply (limit %d) — flattening the remainder", maxParseDepth),
			})
		}
		tok := p.advance()
		return &ast.PLeaf{Value: tok.Value, Span: tok.Span}
	}

	t := p.peek()
	switch t.Value {
	case "(":
		return foldMacroCall(p.parseGrouping("(", ")"))
	case "[":
		return p.parseGrouping("[", "]")
	case "{":
		return p.parseGrouping("{", "}")
	case "&":
		return p.parseSigil(t.Value)
	case ".":
		// A leading `.` can't start a primary — it's only meaningful as
		// the chain continuation handled by parseExpr.
		tok := p.advance()
		p.errs = append(p.errs, ParseError{
			Span:    tok.Span,
			Message: "unexpected '.' — '.' must follow an expression",
		})
		return &ast.PLeaf{Value: tok.Value, Span: tok.Span}
	}
	tok := p.advance()
	return &ast.PLeaf{Value: tok.Value, Span: tok.Span}
}

// parseSigil consumes the sigil and recursively parses the next
// expression. The Sigil's span runs from the sigil character through
// the inner expression's end. A sigil with no following expression is
// recovered as a bare leaf and reported as a parse error.
func (p *posParser) parseSigil(sigil string) ast.PNode {
	sigilTok := p.advance()
	// Recover cleanly when the sigil hits a boundary that can't start an
	// expression: EOF or any closer. Without this, `&` followed by `)`
	// would consume the closer as the sigil's body and then unbalance the
	// surrounding form.
	if p.atEnd() {
		p.errs = append(p.errs, ParseError{
			Span:    sigilTok.Span,
			Message: fmt.Sprintf("missing expression after %q sigil", sigil),
		})
		return &ast.PLeaf{Value: sigil, Span: sigilTok.Span}
	}
	switch p.peek().Value {
	case ")", "]", "}":
		p.errs = append(p.errs, ParseError{
			Span:    sigilTok.Span,
			Message: fmt.Sprintf("missing expression after %q sigil", sigil),
		})
		return &ast.PLeaf{Value: sigil, Span: sigilTok.Span}
	}
	inner := p.parseExpr()
	span := sigilTok.Span
	span.EndLine = inner.GetSpan().EndLine
	span.EndCol = inner.GetSpan().EndCol
	return &ast.PSigil{Sigil: sigil, Inner: inner, Span: span}
}

// foldMacroCall recognizes the `(~name arg1 arg2 ...)` shape coming
// out of parseGrouping and returns it as a PMacroCall instead. The
// leading `~` prefix sigil is dropped from the children — its position
// is kept on PMacroCall.SigilSpan. Only `(`-form branches led by `~`
// fold; anything else passes through unchanged.
func foldMacroCall(br *ast.PBranch) ast.PNode {
	if br.Open != "(" || len(br.Children) < 2 {
		return br
	}
	tilde, ok := br.Children[0].(*ast.PLeaf)
	if !ok || tilde.Value != "~" {
		return br
	}
	args := make([]ast.PNode, 0, len(br.Children)-2)
	args = append(args, br.Children[2:]...)
	return &ast.PMacroCall{
		Head:      br.Children[1],
		Args:      args,
		SigilSpan: tilde.Span,
		Span:      br.Span,
	}
}

// parseGrouping reads a `(`-style form ending in the given closer. The
// opener has not been consumed yet when this is called. Records a
// parse error if the closer is missing; recovery is indentation-guided
// so a missing closer cuts the form off at a sensible boundary instead
// of swallowing the rest of the file:
//
//	(fun 'f '(x)        ← unclosed
//	  '(do (print x)    ← unclosed
//	(var 'y 5)          ← line-leading at col ≤ the openers' line
//	                      indents: both forms are inferred to close
//	                      just after (print x), and (var 'y 5) parses
//	                      as its own top-level form.
//
// The rule only fires when the form's closer genuinely doesn't appear
// later in the stream (closerExists lookahead), so balanced code —
// however it's indented — parses exactly as it always did. Missing-
// closer errors carry Span = the inferred close site (where a closer
// should be inserted) and OpenSpan = the opener.
func (p *posParser) parseGrouping(open, close string) *ast.PBranch {
	openIdx := p.i
	openTok := p.advance() // consume opener
	openIndent := p.indent[openIdx]
	br := &ast.PBranch{
		Open:  open,
		Close: close,
		Span:  openTok.Span,
	}
	reported := false
	for !p.atEnd() {
		t := p.peek()
		if t.Value == close {
			closeTok := p.advance()
			br.Span.EndLine = closeTok.Span.EndLine
			br.Span.EndCol = closeTok.Span.EndCol
			return br
		}
		// Indentation recovery: a line-leading token at or left of the
		// opener's line indent means the user has moved on to a sibling
		// or outer form — close here, before the dedented token, unless
		// our closer really does appear downstream.
		if p.leading[p.i] && t.Span.StartLine > openTok.Span.StartLine &&
			t.Span.StartCol <= openIndent && !p.closerExists(open, close) {
			p.errs = append(p.errs, missingCloser(open, close, openTok.Span, p.tokens[p.i-1].Span))
			reported = true
			break
		}
		// If we see a different closer, leave it for the outer caller —
		// best-effort recovery on malformed input.
		if t.Value == ")" || t.Value == "]" || t.Value == "}" {
			p.errs = append(p.errs, missingCloser(open, close, openTok.Span, p.tokens[p.i-1].Span))
			reported = true
			break
		}
		br.Children = append(br.Children, p.parseExpr())
	}
	// EOF without matching closer is an error too.
	if p.atEnd() && !reported {
		last := openTok.Span
		if p.i > 0 {
			last = p.tokens[p.i-1].Span
		}
		p.errs = append(p.errs, missingCloser(open, close, openTok.Span, last))
	}
	// Span ends at the last child (or opener if none).
	if n := len(br.Children); n > 0 {
		s := br.Children[n-1].GetSpan()
		br.Span.EndLine = s.EndLine
		br.Span.EndCol = s.EndCol
	}
	return br
}

// closerExists reports whether the current form's closer appears later
// in the token stream, using same-type depth counting from the current
// position (depth starts at 1 for the form being parsed). Mixed-type
// bracket crossings can fool the count, but those inputs are already
// malformed and get best-effort treatment anyway. O(n) per call; only
// invoked on dedent candidates inside forms that may be unclosed, so
// well-formed files never pay for it more than the dedents they have.
func (p *posParser) closerExists(open, close string) bool {
	depth := 1
	for k := p.i; k < len(p.tokens); k++ {
		switch p.tokens[k].Value {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// missingCloser builds the ParseError for an unclosed form: a one-
// column span at the inferred close site (right after the last token
// of the form), with the opener recorded on OpenSpan.
func missingCloser(open, close string, openSpan, lastSpan span.Span) ParseError {
	return ParseError{
		Span:     mkSpan(lastSpan.EndLine, lastSpan.EndCol, lastSpan.EndLine, lastSpan.EndCol+1),
		OpenSpan: openSpan,
		Close:    close,
		Message: fmt.Sprintf("missing closing %q for %q opened at %d:%d",
			close, open, openSpan.StartLine, openSpan.StartCol),
	}
}

// scanString skips past a string body delimited by `quote` (either `'` or
// `"`) starting at i (which points at the opening quote), returning the byte
// position right after the closing quote, the updated line/col, and whether
// the closing quote was actually found. Honors the three skip rules
// documented at the string site in LexPos:
//   - “ `X “ backtick passthrough
//   - `\X` backslash escape (eval translates it later)
//   - `%(...)` interpolation expression (delegates to scanInterpExpr)
//
// scanString and scanInterpExpr are mutually recursive: an inner
// string inside `%(...)` is scanned by another call to scanString,
// and a `%(...)` inside that inner string is in turn scanned by
// scanInterpExpr. The recursion is bounded by source length.
func scanString(src string, i, line, col int, quote byte) (end, endLine, endCol int, terminated bool) {
	j := i + 1
	cline, ccol := line, col+1
	for j < len(src) && src[j] != quote {
		c := src[j]
		if (c == '`' || c == '\\') && j+1 < len(src) {
			if src[j+1] == '\n' {
				cline++
				ccol = 1
			} else {
				ccol += 2
			}
			j += 2
			continue
		}
		if c == '%' && j+1 < len(src) && src[j+1] == '(' {
			// `j` points at the %; advance to the ( before delegating.
			j, cline, ccol = scanInterpExpr(src, j+1, cline, ccol+1)
			continue
		}
		if c == '\n' {
			cline++
			ccol = 1
		} else {
			ccol++
		}
		j++
	}
	if j < len(src) {
		j++ // include closing "
		ccol++
		return j, cline, ccol, true
	}
	return j, cline, ccol, false
}

// scanInterpExpr skips past a `%(...)` interpolation expression.
// `start` points at the `(` (one byte after the `%`); the caller has
// already accounted for the `%` in its line/col. Returns the byte
// position right after the matching `)`.
//
// Inside the paren region, the function recognizes the same lexical
// shapes the outer lexer would, so structural `(` `)` counting isn't
// fooled by mismatched parens elsewhere:
//   - Inner strings `"..."` — delegated to scanString (which itself
//     handles nested `%(...)`).
//   - Char literals “ `X` “ (exactly 3 bytes).
//   - Line comments `-- ...` to end of line.
//
// If the source runs out before depth returns to zero, scanInterpExpr
// returns the position at end-of-source. The unterminated-string
// diagnostic at the outer call site is what surfaces this to the user.
func scanInterpExpr(src string, start, line, col int) (end, endLine, endCol int) {
	j := start + 1 // past the opening (
	cline, ccol := line, col+1
	depth := 1
	for j < len(src) && depth > 0 {
		c := src[j]
		switch {
		case c == '\'':
			var terminated bool
			j, cline, ccol, terminated = scanString(src, j, cline, ccol, c)
			if !terminated {
				return j, cline, ccol
			}
		case c == '`' && j+2 < len(src) && src[j+2] == '`':
			j += 3
			ccol += 3
		case c == '-' && j+1 < len(src) && src[j+1] == '-':
			for j < len(src) && src[j] != '\n' {
				j++
				ccol++
			}
		case c == '(':
			depth++
			j++
			ccol++
		case c == ')':
			depth--
			j++
			ccol++
		case c == '\n':
			cline++
			ccol = 1
			j++
		default:
			j++
			ccol++
		}
	}
	return j, cline, ccol
}
