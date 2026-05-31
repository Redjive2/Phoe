package main

import (
	"encoding/json"

	"pho/pkg/lint"
)

// ----------------------------------------------------------------------
// textDocument/semanticTokens/full
// ----------------------------------------------------------------------

// handleSemanticTokens classifies every identifier in the buffer and
// encodes the result in LSP's delta-compressed integer format.
//
// Each token contributes 5 ints:
//   [deltaLine, deltaStart, length, tokenType, tokenModifiers]
// Where deltas are relative to the previous token (or absolute when
// the line changes). Modifiers is always 0 — we don't tag any.
func (s *server) handleSemanticTokens(msg *rawMessage) {
	var p semanticTokensParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		_ = s.t.reply(msg.ID, semanticTokensResult{Data: []uint32{}})
		return
	}

	s.mu.Lock()
	text, ok := s.buffers[p.TextDocument.URI]
	s.mu.Unlock()
	if !ok {
		_ = s.t.reply(msg.ID, semanticTokensResult{Data: []uint32{}})
		return
	}

	tokens := lint.SemanticTokens(uriToPath(p.TextDocument.URI), []byte(text))

	// Encode. Tokens come back sorted by source position from
	// lint.SemanticTokens.
	data := make([]uint32, 0, len(tokens)*5)
	prevLine, prevCol := 0, 0
	for _, tok := range tokens {
		// 0-based positions for LSP.
		line := tok.Span.StartLine - 1
		col := tok.Span.StartCol - 1
		length := tok.Span.EndCol - tok.Span.StartCol
		if length <= 0 {
			length = 1
		}

		var deltaLine, deltaStart int
		if line == prevLine {
			deltaLine = 0
			deltaStart = col - prevCol
		} else {
			deltaLine = line - prevLine
			deltaStart = col
		}
		data = append(data,
			uint32(deltaLine),
			uint32(deltaStart),
			uint32(length),
			uint32(tok.Type),
			0, // modifiers
		)
		prevLine = line
		prevCol = col
	}
	_ = s.t.reply(msg.ID, semanticTokensResult{Data: data})
}

// ----------------------------------------------------------------------
// textDocument/completion
// ----------------------------------------------------------------------

// handleCompletion returns the names visible at the cursor.
func (s *server) handleCompletion(msg *rawMessage) {
	var p completionParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		_ = s.t.reply(msg.ID, completionList{Items: []completionItem{}})
		return
	}

	s.mu.Lock()
	text, ok := s.buffers[p.TextDocument.URI]
	s.mu.Unlock()
	if !ok {
		_ = s.t.reply(msg.ID, completionList{Items: []completionItem{}})
		return
	}

	// LSP positions are 0-based; lint speaks 1-based.
	defs := lint.CompletionsAt(uriToPath(p.TextDocument.URI), []byte(text),
		p.Position.Line+1, p.Position.Character+1)

	items := make([]completionItem, 0, len(defs))
	for _, d := range defs {
		items = append(items, completionItem{
			Label:  d.Name,
			Kind:   defKindToCompletionKind(d.Kind),
			Detail: d.Kind.String(),
		})
	}
	_ = s.t.reply(msg.ID, completionList{Items: items})
}

// defKindToCompletionKind maps lint.DefKind onto LSP CompletionItemKind
// integer codes.
func defKindToCompletionKind(k lint.DefKind) int {
	switch k {
	case lint.DefBuiltin:
		// Heuristic: keyword-y names get Keyword; others get Function.
		// We'd need the name to disambiguate, but for the completion
		// list this level is fine — Function is the default for things
		// you'd call.
		return 14 // Keyword (broad bucket; most builtins are in this category)
	case lint.DefImport:
		return 9 // Module
	case lint.DefConst:
		return 21 // Constant
	case lint.DefVar:
		return 6 // Variable
	case lint.DefFun:
		return 3 // Function
	case lint.DefMethod:
		return 2 // Method
	case lint.DefStruct:
		return 22 // Struct
	case lint.DefParam:
		return 6 // Variable (LSP has no Parameter kind)
	}
	return 6
}
