package main

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"

	"pho/pkg/core"
	"pho/pkg/lint"
)

// ----------------------------------------------------------------------
// textDocument/hover, definition, references, documentSymbol
// ----------------------------------------------------------------------

// posParams unmarshals the shared request shape and converts the LSP
// position to lint's 1-based byte coordinates against the open buffer.
// Returns ok=false when the document isn't open.
func (s *server) posParams(msg *rawMessage) (text string, p textDocumentPositionParams, line, col int, ok bool) {
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return "", p, 0, 0, false
	}
	s.mu.Lock()
	text, open := s.buffers[p.TextDocument.URI]
	s.mu.Unlock()
	if !open {
		return "", p, 0, 0, false
	}
	lines := strings.Split(text, "\n")
	byteCol := p.Position.Character
	if p.Position.Line >= 0 && p.Position.Line < len(lines) {
		byteCol = utf16ColToByte(lines[p.Position.Line], p.Position.Character)
	}
	return text, p, p.Position.Line + 1, byteCol + 1, true
}

func (s *server) handleHover(msg *rawMessage) {
	text, p, line, col, ok := s.posParams(msg)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	md, span, found := lint.HoverAt(uriToPath(p.TextDocument.URI), []byte(text), line, col)
	if !found {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	lines := strings.Split(text, "\n")
	r := spanToRange(lines, span)
	_ = s.t.reply(msg.ID, hoverResult{
		Contents: markupContent{Kind: "markdown", Value: md},
		Range:    &r,
	})
}

func (s *server) handleDefinition(msg *rawMessage) {
	text, p, line, col, ok := s.posParams(msg)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	site, found := lint.DefinitionAt(uriToPath(p.TextDocument.URI), []byte(text), line, col)
	if !found {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	_ = s.t.reply(msg.ID, lspLocation{
		URI:   pathToURI(site.File),
		Range: s.rangeInFile(site.File, p.TextDocument.URI, text, site.Span),
	})
}

func (s *server) handleReferences(msg *rawMessage) {
	text, p, line, col, ok := s.posParams(msg)
	if !ok {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	sites := lint.ReferencesAt(s.workspaceRoot, uriToPath(p.TextDocument.URI), []byte(text), line, col)
	if len(sites) == 0 {
		_ = s.t.reply(msg.ID, nil)
		return
	}
	out := make([]lspLocation, 0, len(sites))
	for _, site := range sites {
		out = append(out, lspLocation{
			URI:   pathToURI(site.File),
			Range: s.rangeInFile(site.File, p.TextDocument.URI, text, site.Span),
		})
	}
	_ = s.t.reply(msg.ID, out)
}

func (s *server) handleDocumentSymbol(msg *rawMessage) {
	var p documentSymbolParams
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
	symbols := lint.DocumentSymbols(uriToPath(p.TextDocument.URI), []byte(text))
	lines := strings.Split(text, "\n")
	_ = s.t.reply(msg.ID, symbolsToLSP(lines, symbols))
}

func symbolsToLSP(lines []string, symbols []lint.Symbol) []documentSymbol {
	out := make([]documentSymbol, 0, len(symbols))
	for _, sym := range symbols {
		out = append(out, documentSymbol{
			Name:           sym.Name,
			Kind:           defKindToSymbolKind(sym.Kind),
			Range:          spanToRange(lines, sym.Span),
			SelectionRange: spanToRange(lines, sym.SelectionSpan),
			Children:       symbolsToLSP(lines, sym.Children),
		})
	}
	return out
}

// defKindToSymbolKind maps lint.DefKind onto LSP SymbolKind integers.
func defKindToSymbolKind(k lint.DefKind) int {
	switch k {
	case lint.DefFun:
		return 12 // Function
	case lint.DefMacro:
		return 12 // Function (LSP SymbolKind has no Macro kind; a macro is callable)
	case lint.DefMethod:
		return 6 // Method
	case lint.DefStruct:
		return 23 // Struct
	case lint.DefConst:
		return 14 // Constant
	case lint.DefVar:
		return 13 // Variable
	case lint.DefField:
		return 8 // Field
	case lint.DefImport:
		return 3 // Namespace
	}
	return 13
}

// spanToRange converts a 1-based byte span to an LSP range against the
// given document lines.
func spanToRange(lines []string, span core.Span) lspRange {
	return lspRange{
		Start: toLSPPosition(lines, span.StartLine, span.StartCol),
		End:   toLSPPosition(lines, span.EndLine, span.EndCol),
	}
}

// rangeInFile converts a span in `file` to an LSP range. The
// requesting document's buffer is used directly; other open documents
// are served from their buffers (their unsaved edits shift line
// content); everything else is read from disk so byte→UTF-16
// conversion sees real line content. On read failure, falls back to a
// naive 1:1 column mapping.
func (s *server) rangeInFile(file, openURI, openText string, span core.Span) lspRange {
	if file == uriToPath(openURI) {
		return spanToRange(strings.Split(openText, "\n"), span)
	}
	s.mu.Lock()
	for uri, text := range s.buffers {
		if uriToPath(uri) == file {
			s.mu.Unlock()
			return spanToRange(strings.Split(text, "\n"), span)
		}
	}
	s.mu.Unlock()
	if data, err := os.ReadFile(file); err == nil {
		return spanToRange(strings.Split(string(data), "\n"), span)
	}
	return lspRange{
		Start: lspPosition{Line: span.StartLine - 1, Character: span.StartCol - 1},
		End:   lspPosition{Line: span.EndLine - 1, Character: span.EndCol - 1},
	}
}

// pathToURI is the inverse of uriToPath.
func pathToURI(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String()
}
