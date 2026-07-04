package main

import (
	"encoding/json"
	"strings"

	"pho/pkg/lint"
)

// bufferPos returns the open buffer text and 1-based byte line/col for an LSP
// position, or ok=false when the document isn't open. Shared by handlers whose
// params aren't the plain textDocumentPositionParams (rename, inlay hints).
func (s *server) bufferPos(uri string, pos lspPosition) (text string, line, col int, ok bool) {
	s.mu.Lock()
	text, open := s.buffers[uri]
	s.mu.Unlock()
	if !open {
		return "", 0, 0, false
	}
	lines := strings.Split(text, "\n")
	byteCol := pos.Character
	if pos.Line >= 0 && pos.Line < len(lines) {
		byteCol = utf16ColToByte(lines[pos.Line], pos.Character)
	}
	return text, pos.Line + 1, byteCol + 1, true
}

// textDocument/documentHighlight — every occurrence of the symbol under the
// cursor IN THE CURRENT FILE, reusing the reference resolver.
func (s *server) handleDocumentHighlight(msg *rawMessage) {
	text, p, line, col, ok := s.posParams(msg)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	path := uriToPath(p.TextDocument.URI)
	out := []documentHighlight{}
	for _, site := range lint.ReferencesAt(s.workspaceRoot, path, []byte(text), line, col) {
		if site.File != "" && site.File != path {
			continue // same-file highlights only
		}
		out = append(out, documentHighlight{Range: s.rangeInFile(path, p.TextDocument.URI, text, site.Span)})
	}
	if len(out) == 0 {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	_ = s.t.reply(msg.ID, out)
}

// textDocument/prepareRename — the symbol is renameable iff the reference
// resolver resolves it (so builtins/keywords/unresolved names are rejected);
// return the identifier range at the cursor.
func (s *server) handlePrepareRename(msg *rawMessage) {
	text, p, line, col, ok := s.posParams(msg)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	path := uriToPath(p.TextDocument.URI)
	for _, site := range lint.ReferencesAt(s.workspaceRoot, path, []byte(text), line, col) {
		if site.File != "" && site.File != path {
			continue
		}
		sp := site.Span
		startOK := line > sp.StartLine || (line == sp.StartLine && col >= sp.StartCol)
		endOK := line < sp.EndLine || (line == sp.EndLine && col <= sp.EndCol)
		if startOK && endOK {
			_ = s.t.reply(msg.ID, s.rangeInFile(path, p.TextDocument.URI, text, sp))
			return
		}
	}
	_ = s.t.reply(msg.ID, nil) // not renameable
}

// textDocument/rename — rename every occurrence (across files) via a workspace
// edit. As complete as find-references: the same resolver drives both.
func (s *server) handleRename(msg *rawMessage) {
	var p renameParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	text, line, col, ok := s.bufferPos(p.TextDocument.URI, p.Position)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	path := uriToPath(p.TextDocument.URI)
	sites := lint.ReferencesAt(s.workspaceRoot, path, []byte(text), line, col)
	if len(sites) == 0 {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	changes := map[string][]textEdit{}
	for _, site := range sites {
		f := site.File
		if f == "" {
			f = path
		}
		uri := pathToURI(f)
		changes[uri] = append(changes[uri], textEdit{
			Range:   s.rangeInFile(f, p.TextDocument.URI, text, site.Span),
			NewText: p.NewName,
		})
	}
	_ = s.t.reply(msg.ID, workspaceEdit{Changes: changes})
}

// textDocument/inlayHint — inferred-type hints for bindings within the range.
func (s *server) handleInlayHint(msg *rawMessage) {
	var p inlayHintParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	s.mu.Lock()
	text, open := s.buffers[p.TextDocument.URI]
	s.mu.Unlock()
	if !open {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	path := uriToPath(p.TextDocument.URI)
	lines := strings.Split(text, "\n")
	out := []inlayHint{}
	startLine, endLine := p.Range.Start.Line+1, p.Range.End.Line+1
	for _, h := range lint.InlayHintsAt(path, []byte(text)) {
		if h.Line < startLine || h.Line > endLine || h.Line-1 >= len(lines) {
			continue
		}
		out = append(out, inlayHint{
			Position:    lspPosition{Line: h.Line - 1, Character: byteColToUTF16(lines[h.Line-1], h.Col-1)},
			Label:       h.Label,
			Kind:        1, // type
			PaddingLeft: true,
		})
	}
	_ = s.t.reply(msg.ID, out)
}

// textDocument/signatureHelp — parameter hints for the call under the cursor.
func (s *server) handleSignatureHelp(msg *rawMessage) {
	text, p, line, col, ok := s.posParams(msg)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	path := uriToPath(p.TextDocument.URI)
	help, found := lint.SignatureHelpAt(path, []byte(text), line, col)
	if !found {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	params := make([]parameterInformation, 0, len(help.Params))
	for _, pl := range help.Params {
		params = append(params, parameterInformation{Label: pl})
	}
	_ = s.t.reply(msg.ID, signatureHelp{
		Signatures:      []signatureInformation{{Label: help.Label, Parameters: params}},
		ActiveSignature: 0,
		ActiveParameter: help.ActiveParam,
	})
}

// textDocument/implementation — the structs that satisfy the trait at the cursor.
func (s *server) handleImplementation(msg *rawMessage) {
	text, p, line, col, ok := s.posParams(msg)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	path := uriToPath(p.TextDocument.URI)
	sites := lint.ImplementationsAt(path, []byte(text), line, col)
	if len(sites) == 0 {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	out := make([]lspLocation, 0, len(sites))
	for _, site := range sites {
		f := site.File
		if f == "" {
			f = path
		}
		out = append(out, lspLocation{URI: pathToURI(f), Range: s.rangeInFile(f, p.TextDocument.URI, text, site.Span)})
	}
	_ = s.t.reply(msg.ID, out)
}
