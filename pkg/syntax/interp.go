package syntax

import (
	"fmt"
	"os"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
)

// String interpolation lowering. The user writes:
//
//	"hi %name, you have %(len items) at %obj.field"
//
// and the lower pass turns the leaf into a call to the mangled
// Strinterp builtin, with each interpolated value wrapped in
// Strcoerce so a non-string `len items` still ends up concatenated
// correctly:
//
//	(Strinterp "hi " (Strcoerce name) ", you have "
//	           (Strcoerce (len items)) " at "
//	           (Strcoerce obj.field))
//
// Recognized after a literal `%`:
//   - `%name`         — a bare identifier
//   - `%a.b.c`        — a chain of atomic expressions joined by `.`
//                       with no whitespace; each atom can be an
//                       identifier, number, string literal, paren
//                       expression, or array/dict literal
//   - `%(expr)`       — any parenthesized expression (a degenerate
//                       case of the chain rule above — one atom)
//
// Whitespace before or after a `.` ends the chain. Use `\%` to
// embed a literal `%` (handled by the existing backslash escape
// system at eval time).

// InterpChunk is one piece of an interpolated string. Literal chunks
// are body substrings; expression chunks are the raw source of an
// interpolated value, to be lexed+parsed by the outer caller.
//
// BodyOffset is the byte offset where this chunk starts inside the
// original string body (without the surrounding quotes). The linter
// uses it to map error spans inside expression chunks back to the
// right column of the source file.
type InterpChunk struct {
	IsExpr     bool
	Text       string
	BodyOffset int
}

// HasInterpolation reports whether a string-literal body contains an
// unescaped `%`. Any unescaped `%` is a candidate — even one followed
// by an invalid character — because we want SplitInterp to surface
// a `bad-interpolation` error rather than silently letting `"100%"`
// pass through. Users with a literal `%` write `\%`.
func HasInterpolation(body string) bool {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\', '`':
			if i+1 < len(body) {
				i++ // skip the escaped byte
			}
		case '%':
			return true
		}
	}
	return false
}

// SplitInterp walks the body and breaks it into literal and expression
// chunks. Errors (trailing `%`, empty `()`, `%X` where X isn't a valid
// start, etc.) are returned as a slice; the caller decides whether to
// abort or fall back. On any error, the chunks returned so far are
// still valid up to that point.
func SplitInterp(body string) ([]InterpChunk, []error) {
	var (
		chunks   []InterpChunk
		errs     []error
		litStart = 0
	)
	flushLit := func(end int) {
		if end > litStart {
			chunks = append(chunks, InterpChunk{IsExpr: false, Text: body[litStart:end], BodyOffset: litStart})
		}
	}
	for i := 0; i < len(body); {
		c := body[i]
		// Skip escaped pairs so a `\%` or `` `% `` doesn't start interp.
		if (c == '\\' || c == '`') && i+1 < len(body) {
			i += 2
			continue
		}
		if c != '%' {
			i++
			continue
		}
		// Past this point we're at a `%`. Decide what kind.
		if i+1 >= len(body) {
			errs = append(errs, fmt.Errorf("trailing '%%' at end of string — use '\\%%' to embed a literal '%%'"))
			break
		}
		next := body[i+1]
		if !isInterpStart(next) {
			errs = append(errs, fmt.Errorf("invalid interpolation: '%%%c' — expected identifier or '(' after '%%', use '\\%%' for a literal", next))
			// Advance past the `%` and keep going so the rest of the
			// string still gets a chance to interpolate.
			i++
			continue
		}
		// Flush the literal chunk we've accumulated so far.
		flushLit(i)

		end, err := readInterpChain(body, i+1)
		if err != nil {
			errs = append(errs, err)
			// Don't try to recover the chain shape — advance past `%`
			// and resume scanning from the next byte.
			litStart = i + 1
			i++
			continue
		}
		chunks = append(chunks, InterpChunk{IsExpr: true, Text: body[i+1 : end], BodyOffset: i + 1})
		litStart = end
		i = end
	}
	flushLit(len(body))
	return chunks, errs
}

// readInterpChain returns the byte position one past the end of a
// chain expression starting at `pos`. The chain is at least one
// atomic expression, optionally followed by `.atomic` repetitions
// with no whitespace anywhere between them.
func readInterpChain(body string, pos int) (int, error) {
	end, err := readAtom(body, pos)
	if err != nil {
		return pos, err
	}
	for end < len(body) && body[end] == '.' {
		// Whitespace before the `.` is impossible to reach here
		// (readAtom would have stopped at the whitespace and `end`
		// wouldn't point at `.`), so the only thing to check is what
		// comes AFTER the `.`. A non-atom-start terminates the chain
		// and the `.` stays in the literal tail.
		if end+1 >= len(body) || !isAtomStart(body[end+1]) {
			return end, nil
		}
		next, err := readAtom(body, end+1)
		if err != nil {
			return end, nil
		}
		end = next
	}
	return end, nil
}

// readAtom reads one atomic expression starting at `pos`. Atomics
// are the kinds of things a Pho dot chain can be made of: identifiers,
// numbers, string literals, paren expressions, array/dict literals.
//
// Returns the byte position one past the atom's end. If the byte at
// `pos` doesn't begin an atom, returns an error and leaves pos.
func readAtom(body string, pos int) (int, error) {
	if pos >= len(body) {
		return pos, fmt.Errorf("interpolation expression ended unexpectedly")
	}
	c := body[pos]
	switch {
	case isIdentStart(c):
		end := pos + 1
		for end < len(body) && isIdentCont(body[end]) {
			end++
		}
		// An optional trailing '?' (predicate convention) and/or '!' (effect
		// convention), always ordered `name?!` — so `%done?` interpolates the
		// `done?` variable and `%flush!` the `flush!` one. The self-mutation '='
		// suffix is NOT consumed here: a bare `%name` interpolates a value, and a
		// trailing '=' in a string is almost always a literal (`%a=%b` shows a
		// key/value). A `=`-method reference can still be interpolated via the
		// explicit `%(expr)` form, which lexes through the full lexer.
		if end < len(body) && body[end] == '?' {
			end++
		}
		if end < len(body) && body[end] == '!' {
			end++
		}
		return end, nil
	case isDigit(c):
		end := pos + 1
		for end < len(body) && isDigit(body[end]) {
			end++
		}
		return end, nil
	case c == '-' && pos+1 < len(body) && isDigit(body[pos+1]):
		end := pos + 2
		for end < len(body) && isDigit(body[end]) {
			end++
		}
		return end, nil
	case c == '\'':
		end, _, _, ok := scanString(body, pos, 1, 1, c)
		if !ok {
			return pos, fmt.Errorf("unterminated string inside interpolation")
		}
		return end, nil
	case c == '(' || c == '[' || c == '{':
		return readBalanced(body, pos)
	}
	return pos, fmt.Errorf("interpolation expression must start with an identifier, number, string, '(', '[', or '{'; got %q", string(c))
}

// readBalanced finds the matching close of a `(...)`, `[...]`, or
// `{...}` form. Inner string literals (and their `%(...)` content)
// are walked through scanString so a stray `"` or `)` inside them
// doesn't fool the counter.
func readBalanced(body string, pos int) (int, error) {
	open := body[pos]
	var close byte
	switch open {
	case '(':
		close = ')'
	case '[':
		close = ']'
	case '{':
		close = '}'
	default:
		return pos, fmt.Errorf("readBalanced called on '%c'", open)
	}
	depth := 1
	j := pos + 1
	for j < len(body) && depth > 0 {
		c := body[j]
		switch {
		case c == '\'':
			next, _, _, ok := scanString(body, j, 1, 1, c)
			if !ok {
				return pos, fmt.Errorf("unterminated string inside interpolation")
			}
			j = next
		case c == '`' && j+2 < len(body) && body[j+2] == '`':
			j += 3
		case c == '-' && j+1 < len(body) && body[j+1] == '-':
			for j < len(body) && body[j] != '\n' {
				j++
			}
		case c == open:
			depth++
			j++
		case c == close:
			depth--
			j++
		default:
			j++
		}
	}
	if depth != 0 {
		return pos, fmt.Errorf("unbalanced '%c' inside interpolation", open)
	}
	// Reject empty parens: `%()` is the canonical "user wrote nothing".
	if open == '(' && j == pos+2 {
		return pos, fmt.Errorf("empty interpolation '%%()' — write '\\%%()' for a literal")
	}
	return j, nil
}

// loweredInterp builds the (Strinterp ...) Branch from a string body
// known to contain interpolation. Each expression chunk is lexed,
// parsed, and lowered through the regular pipeline, then wrapped in
// (Strcoerce ...) so non-string values get stringified. Literal
// chunks are emitted as plain string leaves the runtime will pass
// through unwrapped.
//
// strSpan is the source span of the enclosing string literal. Each
// chunk's inner tree parses in chunk-local coordinates; OffsetSpans
// remaps them into file coordinates (via BodyByteToPos, the same
// machinery the linter uses), so a runtime error inside `%(...)` carets
// the right columns inside the string literal.
//
// If a chunk's inner parse fails (which shouldn't happen for chunks
// that splitInterp accepted, but the inner LexPos/ParsePos can still
// emit recoverable trees with errors), the chunk is emitted as a
// literal `"%..."` placeholder so the build at least produces a
// well-formed AST.
func loweredInterp(body string, strSpan span.Span) core.Node {
	chunks, errs := SplitInterp(body)
	for _, err := range errs {
		// Lowering runs before evaluation and has no diagnostic session
		// to thread through; the linter reports bad interpolation with a
		// real span (and lint-on-run blocks the program before this).
		// This stderr line is a defensive backstop for unlinted paths.
		fmt.Fprintf(os.Stderr, "pho: %v\n", err)
	}
	out := make(core.Branch, 0, len(chunks)+1)
	out = append(out, core.Leaf(core.Strinterp))
	for _, ch := range chunks {
		if !ch.IsExpr {
			out = append(out, core.Leaf("'"+ch.Text+"'"))
			continue
		}
		tokens, lexErrs := LexPos(ch.Text)
		tree, parseErrs := ParsePos(tokens)
		if len(lexErrs)+len(parseErrs) > 0 || len(tree) != 1 {
			fmt.Fprintf(os.Stderr, "pho: failed to parse interpolation expression '%s'\n", ch.Text)
			out = append(out, core.Leaf("'%"+ch.Text+"'"))
			continue
		}
		if strSpan != (span.Span{}) {
			line, col := BodyByteToPos(body, ch.BodyOffset, strSpan.StartLine, strSpan.StartCol)
			OffsetSpans(tree[0], line-1, col-1)
		}
		expr := lowerNode(tree[0])
		out = append(out, spanned(core.Branch{core.Leaf(core.Strcoerce), expr}, tree[0].GetSpan()))
	}
	return out
}

// BodyByteToPos maps a byte offset inside a string-literal body to a
// (line, col) in the source file. `sLine` and `sCol` are the source
// position of the OPENING `"` of the string. Used by the linter to
// translate spans returned by re-lexing an interpolation expression
// back into the right column of the user's file.
//
// Line/col are 1-based, consistent with the rest of the syntax pkg.
func BodyByteToPos(body string, byteOffset, sLine, sCol int) (line, col int) {
	line = sLine
	col = sCol + 1
	if byteOffset > len(body) {
		byteOffset = len(body)
	}
	for i := 0; i < byteOffset; i++ {
		if body[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// OffsetSpans walks a PNode tree and shifts every span by the given
// deltas. The deltas come from BodyByteToPos: `lineDelta` is added to
// every line; `firstColDelta` is added to columns ONLY on the first
// line of the inner tree, since columns reset to 1 on subsequent
// lines. Mutates the tree in place.
func OffsetSpans(n ast.PNode, lineDelta, firstColDelta int) {
	if n == nil {
		return
	}
	shift := func(s *span.Span) {
		if s.StartLine == 1 {
			s.StartCol += firstColDelta
		}
		if s.EndLine == 1 {
			s.EndCol += firstColDelta
		}
		s.StartLine += lineDelta
		s.EndLine += lineDelta
	}
	switch t := n.(type) {
	case *ast.PLeaf:
		shift(&t.Span)
	case *ast.PBranch:
		shift(&t.Span)
		for _, c := range t.Children {
			OffsetSpans(c, lineDelta, firstColDelta)
		}
	case *ast.PSigil:
		shift(&t.Span)
		OffsetSpans(t.Inner, lineDelta, firstColDelta)
	case *ast.PDot:
		shift(&t.Span)
		OffsetSpans(t.LHS, lineDelta, firstColDelta)
		OffsetSpans(t.RHS, lineDelta, firstColDelta)
	case *ast.PSlash:
		shift(&t.Span)
		OffsetSpans(t.LHS, lineDelta, firstColDelta)
		OffsetSpans(t.RHS, lineDelta, firstColDelta)
	case *ast.PMacroCall:
		shift(&t.Span)
		shift(&t.SigilSpan)
		OffsetSpans(t.Head, lineDelta, firstColDelta)
		for _, a := range t.Args {
			OffsetSpans(a, lineDelta, firstColDelta)
		}
	}
}

func isInterpStart(c byte) bool { return isIdentStart(c) || c == '(' }
func isAtomStart(c byte) bool {
	return isIdentStart(c) || isDigit(c) || c == '-' || c == '\'' || c == '(' || c == '[' || c == '{'
}
