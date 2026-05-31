package syntax

import (
	"fmt"
	"strings"

	"pho/pkg/core"
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
// The PNode tree types live in pkg/core so the runtime can later hang
// Evaluate methods off them and walk this tree directly. Token and
// ParseError stay here — they're parser-internal artifacts.

// Token is one lexer-produced unit with its source position.
type Token struct {
	Value string
	Span  core.Span
}

// mkSpan is a positional constructor for core.Span. The lexer below
// builds spans dozens of times in identical (sl, sc, el, ec) order;
// using this keeps the call sites tight without the unkeyed-literal
// vet warning that fires for cross-package composite literals.
func mkSpan(sl, sc, el, ec int) core.Span {
	return core.Span{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
}

// ParseError records a malformed-input issue picked up by either the
// lexer or the parser. The lexer flags unrecognized characters; the
// parser flags unclosed groupings, dangling sigils, and stray closers.
// Lint surfaces these as "parse-error" diagnostics.
type ParseError struct {
	Span    core.Span
	Message string
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

		// Line comment: `--` to end of line.
		if ch == '-' && i+1 < len(src) && src[i+1] == '-' {
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
		if ch == '"' {
			j, cline, ccol, terminated := scanString(src, i, line, col)
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
				Span:  mkSpan(startLine, startCol, line, col + 3),
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
				Span:    mkSpan(startLine, startCol, line, col + 1),
				Message: "stray '`' — char literals must be `X`",
			})
			i++
			col++
			continue
		}

		// Single-character punctuation that always tokenizes alone.
		if isStructural(ch) {
			tokens = append(tokens, Token{
				Value: string(ch),
				Span:  mkSpan(startLine, startCol, line, col + 1),
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
				Span:  mkSpan(startLine, startCol, line, col + (j - i)),
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
				Span:  mkSpan(startLine, startCol, line, col + (j - i)),
			})
			col += j - i
			i = j
			continue
		}

		// Identifier or word-like operator.
		if isIdentStart(ch) {
			j := i + 1
			for j < len(src) && isIdentCont(src[j]) {
				j++
			}
			tokens = append(tokens, Token{
				Value: src[i:j],
				Span:  mkSpan(startLine, startCol, line, col + (j - i)),
			})
			col += j - i
			i = j
			continue
		}

		// Multi-char operators (==, ~=, <=, >=). Try two-char match first.
		if i+1 < len(src) {
			two := src[i : i+2]
			if two == "==" || two == "~=" || two == "<=" || two == ">=" {
				tokens = append(tokens, Token{
					Value: two,
					Span:  mkSpan(startLine, startCol, line, col + 2),
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
				Span:  mkSpan(startLine, startCol, line, col + 1),
			})
			i++
			col++
			continue
		}

		// Unrecognized character: don't emit a token — the parser
		// shouldn't have to deal with garbage. Record as a lex error.
		errs = append(errs, ParseError{
			Span:    mkSpan(startLine, startCol, line, col + 1),
			Message: fmt.Sprintf("unrecognized character %q", ch),
		})
		i++
		col++
	}
	return tokens, errs
}

// isStructural reports whether ch is a token-by-itself punctuation
// character that doesn't combine with adjacent characters.
func isStructural(ch byte) bool {
	switch ch {
	case '(', ')', '[', ']', '{', '}', '\'', '&', '!', '.', ':':
		return true
	}
	return false
}

func isDigit(ch byte) bool      { return ch >= '0' && ch <= '9' }
func isIdentStart(ch byte) bool { return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') }
func isIdentCont(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch) || ch == '_'
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
// Sugar passes (CompressBlock/Dot/Macro/Code) are NOT run. The linter
// wants to see source-shaped trees.
func ParsePos(tokens []Token) ([]core.PNode, []ParseError) {
	p := &posParser{tokens: tokens}
	var out []core.PNode
	for !p.atEnd() {
		// Defensive: a stray closer at top level is an error and would
		// otherwise spin in parseExpr → parsePrimary → consume nothing.
		t := p.peek()
		if t.Value == ")" || t.Value == "]" || t.Value == "}" {
			p.errs = append(p.errs, ParseError{
				Span:    t.Span,
				Message: fmt.Sprintf("unexpected %q at top level", t.Value),
			})
			p.advance()
			continue
		}
		out = append(out, p.parseExpr())
	}
	return out, p.errs
}

type posParser struct {
	tokens []Token
	i      int
	errs   []ParseError
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

func (p *posParser) parseExpr() core.PNode {
	expr := p.parsePrimary()
	// Build a left-associative dot chain: while the next token is `.`,
	// consume it and pair with the next primary. `a.b.c` becomes
	// PDot{PDot{a, b}, c}.
	for !p.atEnd() && p.peek().Value == "." {
		dotTok := p.advance() // consume '.'
		if p.atEnd() {
			p.errs = append(p.errs, ParseError{
				Span:    dotTok.Span,
				Message: "missing expression after '.'",
			})
			break
		}
		// The token after `.` must be able to start a primary. Closers
		// and `!` can't, and consuming them here would steal them from
		// the surrounding form (e.g. `args.)` would eat the `)` that
		// belongs to the enclosing parenthesized form).
		var rhs core.PNode
		switch p.peek().Value {
		case ")", "]", "}", "!":
			p.errs = append(p.errs, ParseError{
				Span:    dotTok.Span,
				Message: "missing expression after '.'",
			})
			return expr
		case ".":
			// Consecutive dots — `args...` and similar. Each extra `.`
			// becomes a literal leaf in the chain so the legacy variadic
			// syntax keeps producing the expected tree shape.
			tok := p.advance()
			rhs = &core.PLeaf{Value: tok.Value, Span: tok.Span}
		default:
			rhs = p.parsePrimary()
		}
		span := expr.GetSpan()
		span.EndLine = rhs.GetSpan().EndLine
		span.EndCol = rhs.GetSpan().EndCol
		expr = &core.PDot{LHS: expr, RHS: rhs, Span: span}
	}
	return expr
}

// parsePrimary handles a single non-dot expression: a grouped form, a
// sigil-prefixed expression, or an atom. Callers (top-level loop,
// parseGrouping, parseSigil, parseExpr's dot loop) all filter closers
// before recursing, so a closer cannot reach here in practice.
func (p *posParser) parsePrimary() core.PNode {
	t := p.peek()
	switch t.Value {
	case "(":
		return foldMacroCall(p.parseGrouping("(", ")"))
	case "[":
		return p.parseGrouping("[", "]")
	case "{":
		return p.parseGrouping("{", "}")
	case "'", "&":
		return p.parseSigil(t.Value)
	case ".":
		// A leading `.` can't start a primary — it's only meaningful as
		// the chain continuation handled by parseExpr.
		tok := p.advance()
		p.errs = append(p.errs, ParseError{
			Span:    tok.Span,
			Message: "unexpected '.' — '.' must follow an expression",
		})
		return &core.PLeaf{Value: tok.Value, Span: tok.Span}
	}
	tok := p.advance()
	return &core.PLeaf{Value: tok.Value, Span: tok.Span}
}

// parseSigil consumes the sigil and recursively parses the next
// expression. The Sigil's span runs from the sigil character through
// the inner expression's end. A sigil with no following expression is
// recovered as a bare leaf and reported as a parse error.
func (p *posParser) parseSigil(sigil string) core.PNode {
	sigilTok := p.advance()
	// Recover cleanly when the sigil hits a boundary that can't start an
	// expression: EOF, any closer, or the macro-stop `!`. Without this,
	// `'` followed by `)` would consume the closer as the sigil's body
	// and then unbalance the surrounding form.
	if p.atEnd() {
		p.errs = append(p.errs, ParseError{
			Span:    sigilTok.Span,
			Message: fmt.Sprintf("missing expression after %q sigil", sigil),
		})
		return &core.PLeaf{Value: sigil, Span: sigilTok.Span}
	}
	switch p.peek().Value {
	case ")", "]", "}", "!":
		p.errs = append(p.errs, ParseError{
			Span:    sigilTok.Span,
			Message: fmt.Sprintf("missing expression after %q sigil", sigil),
		})
		return &core.PLeaf{Value: sigil, Span: sigilTok.Span}
	}
	inner := p.parseExpr()
	span := sigilTok.Span
	span.EndLine = inner.GetSpan().EndLine
	span.EndCol = inner.GetSpan().EndCol
	return &core.PSigil{Sigil: sigil, Inner: inner, Span: span}
}

// foldMacroCall recognizes the `(name! arg1 arg2 ...)` shape coming
// out of parseGrouping and returns it as a PMacroCall instead. The
// `!` token is dropped from the args list — its position is kept on
// PMacroCall.BangSpan. Only `(`-form branches with `!` in second
// position fold; anything else passes through unchanged.
func foldMacroCall(br *core.PBranch) core.PNode {
	if br.Open != "(" || len(br.Children) < 2 {
		return br
	}
	bang, ok := br.Children[1].(*core.PLeaf)
	if !ok || bang.Value != "!" {
		return br
	}
	args := make([]core.PNode, 0, len(br.Children)-2)
	args = append(args, br.Children[2:]...)
	return &core.PMacroCall{
		Head:     br.Children[0],
		Args:     args,
		BangSpan: bang.Span,
		Span:     br.Span,
	}
}

// parseGrouping reads a `(`-style form ending in the given closer. The
// opener has not been consumed yet when this is called. Records a
// parse error if EOF is hit before the closer or if a wrong closer is
// found at the same depth.
func (p *posParser) parseGrouping(open, close string) *core.PBranch {
	openTok := p.advance() // consume opener
	br := &core.PBranch{
		Open:  open,
		Close: close,
		Span:  openTok.Span,
	}
	for !p.atEnd() {
		t := p.peek()
		if t.Value == close {
			closeTok := p.advance()
			br.Span.EndLine = closeTok.Span.EndLine
			br.Span.EndCol = closeTok.Span.EndCol
			return br
		}
		// If we see a different closer, leave it for the outer caller —
		// best-effort recovery on malformed input.
		if t.Value == ")" || t.Value == "]" || t.Value == "}" {
			p.errs = append(p.errs, ParseError{
				Span:    openTok.Span,
				Message: fmt.Sprintf("missing closing %q for %q opened here", close, open),
			})
			break
		}
		br.Children = append(br.Children, p.parseExpr())
	}
	// EOF without matching closer is an error too.
	if p.atEnd() {
		p.errs = append(p.errs, ParseError{
			Span:    openTok.Span,
			Message: fmt.Sprintf("missing closing %q for %q opened here", close, open),
		})
	}
	// Span ends at the last child (or opener if none).
	if n := len(br.Children); n > 0 {
		s := br.Children[n-1].GetSpan()
		br.Span.EndLine = s.EndLine
		br.Span.EndCol = s.EndCol
	}
	return br
}

// scanString skips past a `"..."` string body starting at i (which
// points at the opening quote), returning the byte position right
// after the closing quote, the updated line/col, and whether the
// closing quote was actually found. Honors the three skip rules
// documented at the `if ch == '"'` site in LexPos:
//   - `` `X `` backtick passthrough
//   - `\X` backslash escape (eval translates it later)
//   - `%(...)` interpolation expression (delegates to scanInterpExpr)
//
// scanString and scanInterpExpr are mutually recursive: an inner
// string inside `%(...)` is scanned by another call to scanString,
// and a `%(...)` inside that inner string is in turn scanned by
// scanInterpExpr. The recursion is bounded by source length.
func scanString(src string, i, line, col int) (end, endLine, endCol int, terminated bool) {
	j := i + 1
	cline, ccol := line, col+1
	for j < len(src) && src[j] != '"' {
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
//   - Char literals `` `X` `` (exactly 3 bytes).
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
		case c == '"':
			var terminated bool
			j, cline, ccol, terminated = scanString(src, j, cline, ccol)
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
