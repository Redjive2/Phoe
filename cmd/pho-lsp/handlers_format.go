package main

import (
	"encoding/json"
	"strings"

	"pho/pkg/syntax"
)

// ----------------------------------------------------------------------
// textDocument/onTypeFormatting — closer auto-balancing
// ----------------------------------------------------------------------

// handleOnTypeFormatting restores bracket balance around the cursor
// after a trigger character ("\n", ")", "]", "}"). The actual policy —
// when a missing closer is inserted, where it goes, when a stray one
// is deleted — lives in syntax.BalanceClosers; this handler only
// converts positions. Balanced buffers come back with no edits, so
// the common case is a cheap no-op.
func (s *server) handleOnTypeFormatting(msg *rawMessage) {
	var p documentOnTypeFormattingParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		_ = s.t.reply(msg.ID, nil)
		return
	}

	s.mu.Lock()
	text, ok := s.buffers[p.TextDocument.URI]
	s.mu.Unlock()
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}

	// LSP positions are 0-based UTF-16; the balancer speaks 1-based
	// byte columns.
	lines := strings.Split(text, "\n")
	byteCol := p.Position.Character
	if p.Position.Line >= 0 && p.Position.Line < len(lines) {
		byteCol = utf16ColToByte(lines[p.Position.Line], p.Position.Character)
	}

	edits := syntax.BalanceClosers(text, p.Position.Line+1, byteCol+1)
	if len(edits) == 0 {
		_ = s.t.reply(msg.ID, nil)
		return
	}

	out := make([]textEdit, 0, len(edits))
	for _, e := range edits {
		out = append(out, textEdit{
			Range: lspRange{
				Start: toLSPPosition(lines, e.Span.StartLine, e.Span.StartCol),
				End:   toLSPPosition(lines, e.Span.EndLine, e.Span.EndCol),
			},
			NewText: e.NewText,
		})
	}
	logf("onTypeFormatting %q at %d:%d → %d edit(s)",
		p.Ch, p.Position.Line+1, p.Position.Character+1, len(out))
	_ = s.t.reply(msg.ID, out)
}
