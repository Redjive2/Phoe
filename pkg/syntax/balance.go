package syntax

import (
	"runtime/debug"

	"pho/pkg/ast"
	"pho/pkg/span"
)

// OnPanic, if non-nil, is called when BalanceClosers recovers a panic,
// so the host (the LSP) can log the stack trace while the call still
// returns a safe empty result. Default nil: recover silently.
var OnPanic func(op string, recovered any, stack []byte)

// Closer balancing for as-you-type editing (LSP onTypeFormatting).
//
// Design: balance RESTORATION, not style enforcement. If the code
// around the cursor is bracket-balanced, BalanceClosers returns nil —
// it never rewrites working code to a canonical layout, so it can't
// fight the editor's auto-close or the user's formatting. Only when
// balance is broken (a deleted closer, a paste, an abandoned form)
// does it compute fixes, placing closers where the parser's
// indentation-guided recovery says the form was meant to end.
//
// The one judgment call is an unclosed form that runs to the end of
// the buffer while the user is still typing inside it: inserting the
// closer immediately would drop it above the code they're about to
// write. The cursor's column is the intent signal — a form whose
// opener's line indent is at or right of the cursor is finished (the
// user has dedented past it); a form the cursor is still indented
// inside stays open, and closes retroactively at the predicted site
// once a dedented sibling appears below it.

// Edit is a single text replacement. Span is 1-based and half-open; a
// zero-width Span is an insertion, an empty NewText is a deletion.
type Edit struct {
	Span    span.Span
	NewText string
}

// BalanceClosers computes the edits that restore bracket balance in
// the neighborhood of the cursor at (line, col), both 1-based. Returns
// nil when the neighborhood is already balanced or when it can't make
// a confident call (e.g. an unterminated string nearby).
//
// Scope: edits are only produced for problems inside the top-level
// form containing the cursor or the one immediately before it (the
// retroactive-close case) — broken code elsewhere in the file is left
// alone until the user edits near it.
func BalanceClosers(src string, line, col int) (edits []Edit) {
	// Panic-safe by contract: this runs on every on-type-format trigger
	// (newline / closer), so a latent index bug on odd mid-edit input
	// must degrade to "no edits", never destabilize the caller. OnPanic
	// (if set) records the trace.
	defer func() {
		if r := recover(); r != nil {
			if OnPanic != nil {
				OnPanic("BalanceClosers", r, debug.Stack())
			}
			edits = nil
		}
	}()
	tokens, lexErrs := LexPos(src)
	tree, parseErrs := ParsePos(tokens)
	if len(tree) == 0 {
		return nil
	}

	scopeStart, scopeEnd := cursorScope(tree, line, col)

	// An unterminated string (or any lex-level damage) inside the scope
	// makes token positions untrustworthy — don't touch anything.
	for _, e := range lexErrs {
		if posInScope(e.Span.StartLine, e.Span.StartCol, scopeStart, scopeEnd) {
			return nil
		}
	}

	_, indent := lineInfo(tokens)

	for _, e := range parseErrs {
		switch {
		case e.Close != "":
			// Missing closer. Only act on forms opened within scope.
			if !posInScope(e.OpenSpan.StartLine, e.OpenSpan.StartCol, scopeStart, scopeEnd) {
				continue
			}
			siteLine, siteCol := e.Span.StartLine, e.Span.StartCol
			if !tokenAtOrAfter(tokens, siteLine, siteCol) {
				// No token follows the close site: the form simply ran
				// off the end of the buffer rather than being cut off
				// by a dedented sibling — the user is plausibly still
				// typing inside it. Close it only if the cursor has
				// dedented to or past the opener's line indent (the
				// "I'm done with this form" signal).
				oi, ok := openerIndent(tokens, indent, e.OpenSpan)
				if !ok || oi < col {
					continue
				}
			}
			edits = appendInsertion(edits, siteLine, siteCol, e.Close)

		case e.Stray:
			// Stray closer: delete it.
			if !posInScope(e.Span.StartLine, e.Span.StartCol, scopeStart, scopeEnd) {
				continue
			}
			edits = append(edits, Edit{Span: e.Span, NewText: ""})
		}
	}
	return edits
}

// cursorScope returns the half-open position range [start, end) that
// balancing is allowed to edit: from the start of the top-level form
// immediately BEFORE the one containing the cursor (so a form
// abandoned just above the cursor can close retroactively) to the
// start of the form after the cursor's.
func cursorScope(tree []ast.PNode, line, col int) (start, end [2]int) {
	idx := 0
	for i, n := range tree {
		s := n.GetSpan()
		if s.StartLine < line || (s.StartLine == line && s.StartCol <= col) {
			idx = i
		} else {
			break
		}
	}
	startIdx := idx - 1
	if startIdx < 0 {
		startIdx = 0
	}
	ss := tree[startIdx].GetSpan()
	start = [2]int{ss.StartLine, ss.StartCol}
	end = [2]int{1 << 30, 1 << 30}
	if idx+1 < len(tree) {
		es := tree[idx+1].GetSpan()
		end = [2]int{es.StartLine, es.StartCol}
	}
	return start, end
}

func posInScope(line, col int, start, end [2]int) bool {
	afterStart := line > start[0] || (line == start[0] && col >= start[1])
	beforeEnd := line < end[0] || (line == end[0] && col < end[1])
	return afterStart && beforeEnd
}

// tokenAtOrAfter reports whether any token starts at or after the
// given position — true exactly when the parser's recovery cut a form
// off before a real dedented token (as opposed to running out of
// input).
func tokenAtOrAfter(tokens []Token, line, col int) bool {
	for _, t := range tokens {
		if t.Span.StartLine > line || (t.Span.StartLine == line && t.Span.StartCol >= col) {
			return true
		}
	}
	return false
}

// openerIndent finds the token matching the opener's span and returns
// its line indent (the column of the first token on the opener's
// line).
func openerIndent(tokens []Token, indent []int, open span.Span) (int, bool) {
	for k, t := range tokens {
		if t.Span.StartLine == open.StartLine && t.Span.StartCol == open.StartCol {
			return indent[k], true
		}
	}
	return 0, false
}

// appendInsertion adds a zero-width insertion edit, merging with an
// existing insertion at the same position. Parse errors arrive inner-
// form-first, so concatenation yields closers in the right order
// (innermost closer first).
func appendInsertion(edits []Edit, line, col int, text string) []Edit {
	for i := range edits {
		e := &edits[i]
		if e.NewText != "" && e.Span.StartLine == line && e.Span.StartCol == col &&
			e.Span.EndLine == line && e.Span.EndCol == col {
			e.NewText += text
			return edits
		}
	}
	return append(edits, Edit{
		Span:    span.Span{StartLine: line, StartCol: col, EndLine: line, EndCol: col},
		NewText: text,
	})
}
