// Command pho-lsp is the Pho language server. It speaks LSP over
// stdin/stdout: clients (editors) connect by spawning the binary as a
// subprocess. The server runs pkg/lint on each open buffer and
// publishes diagnostics on every change.
//
// Currently implements:
//   initialize / initialized
//   textDocument/didOpen, didChange, didClose
//   textDocument/publishDiagnostics
//   shutdown / exit
//
// Everything else is acknowledged with an empty response or ignored.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"pho/pkg/core"
	"pho/pkg/lint"
)

func main() {
	logf("pho-lsp starting (log: %s)", logPath())
	srv := newServer(os.Stdin, os.Stdout)
	if err := srv.run(); err != nil && !errors.Is(err, io.EOF) {
		logf("pho-lsp exiting with error: %v", err)
		fmt.Fprintf(os.Stderr, "pho-lsp: %v\n", err)
		os.Exit(1)
	}
	logf("pho-lsp shutting down")
}

// server is the LSP server state. The buffers map holds every open
// document's text, keyed by URI. Linting runs on the in-memory text,
// not the on-disk file, so unsaved edits get diagnostics.
type server struct {
	t       *transport
	mu      sync.Mutex
	buffers map[string]string

	shutdown bool
}

func newServer(in io.Reader, out io.Writer) *server {
	return &server{
		t:       newTransport(in, out),
		buffers: make(map[string]string),
	}
}

func (s *server) run() error {
	for {
		msg, err := s.t.readMessage()
		if err != nil {
			return err
		}
		s.safeDispatch(msg)
	}
}

// safeDispatch wraps dispatch in a recover. A panic inside any single
// handler (most likely from lint, which walks user-supplied source)
// would otherwise crash the whole server and force the editor to be
// restarted to get diagnostics again. Recovering keeps the server
// alive; the panic is logged with a stack trace, and if the offending
// message had an ID we send back a JSON-RPC error so the client isn't
// left waiting on a request that will never reply.
func (s *server) safeDispatch(msg *rawMessage) {
	defer func() {
		if r := recover(); r != nil {
			logf("panic in dispatch (method=%q): %v\n%s", msg.Method, r, debug.Stack())
			if len(msg.ID) > 0 {
				_ = s.t.replyError(msg.ID, fmt.Sprintf("internal error: %v", r))
			}
		}
	}()
	s.dispatch(msg)
}

func (s *server) dispatch(msg *rawMessage) {
	// Responses to our outbound requests aren't dispatched (we don't
	// send any yet). Skip if there's no method.
	if msg.Method == "" {
		return
	}

	switch msg.Method {
	case "initialize":
		s.handleInitialize(msg)
	case "initialized":
		// Notification — no response needed.
	case "textDocument/didOpen":
		s.handleDidOpen(msg)
	case "textDocument/didChange":
		s.handleDidChange(msg)
	case "textDocument/didClose":
		s.handleDidClose(msg)
	case "textDocument/semanticTokens/full":
		s.handleSemanticTokens(msg)
	case "textDocument/completion":
		s.handleCompletion(msg)
	case "shutdown":
		s.shutdown = true
		_ = s.t.reply(msg.ID, nil)
	case "exit":
		// Spec says exit cleanly with 0 if shutdown was received,
		// nonzero otherwise.
		if s.shutdown {
			os.Exit(0)
		}
		os.Exit(1)
	default:
		// Unknown request — reply with a default-value response so the
		// client doesn't hang waiting. Notifications get ignored.
		if len(msg.ID) > 0 {
			_ = s.t.reply(msg.ID, nil)
		}
	}
}

// ----------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------

func (s *server) handleInitialize(msg *rawMessage) {
	result := initializeResult{
		Capabilities: serverCapabilities{
			TextDocumentSync: textDocumentSyncOptions{
				OpenClose: true,
				Change:    2, // incremental
			},
			SemanticTokensProvider: semanticTokensOptions{
				Legend: semanticTokensLegend{
					TokenTypes:     lint.SemanticTokenTypeNames,
					TokenModifiers: []string{},
				},
				Full:  true,
				Range: false,
			},
			CompletionProvider: completionOptions{
				TriggerCharacters: []string{"."},
			},
		},
		ServerInfo: &serverInfo{Name: "pho-lsp", Version: "0.1.0"},
	}
	_ = s.t.reply(msg.ID, result)
}

func (s *server) handleDidOpen(msg *rawMessage) {
	var p didOpenParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	s.mu.Lock()
	s.buffers[p.TextDocument.URI] = p.TextDocument.Text
	s.mu.Unlock()
	s.publish(p.TextDocument.URI)
}

func (s *server) handleDidChange(msg *rawMessage) {
	var p didChangeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	if len(p.ContentChanges) == 0 {
		return
	}

	s.mu.Lock()
	text := s.buffers[p.TextDocument.URI]
	for _, change := range p.ContentChanges {
		if change.Range == nil {
			// Full-text replacement (some clients send this even when
			// we advertise incremental).
			text = change.Text
			continue
		}
		text = applyChange(text, *change.Range, change.Text)
	}
	s.buffers[p.TextDocument.URI] = text
	s.mu.Unlock()

	s.publish(p.TextDocument.URI)
}

// applyChange splices `replacement` into `text` over the LSP range.
// LSP positions are 0-based UTF-16 code units; we treat them as byte
// offsets here since Pho source is overwhelmingly ASCII. If/when we
// need real Unicode support, this is the function that grows.
func applyChange(text string, r lspRange, replacement string) string {
	startOff := offsetForLSP(text, r.Start)
	endOff := offsetForLSP(text, r.End)
	if endOff < startOff {
		return text // backwards range — drop rather than corrupt
	}
	return text[:startOff] + replacement + text[endOff:]
}

// offsetForLSP converts an LSP position (0-based line, 0-based
// character) to a byte offset in `text`. The conversion is lenient:
// positions past the end of a line clamp to that line's end, and
// positions past the end of the text clamp to len(text). Editors
// (Zed included) routinely reference "the position after the last
// char" when appending, especially when the file lacks a trailing
// newline; treating those as -1 silently dropped changes and made
// the buffer go stale.
func offsetForLSP(text string, p lspPosition) int {
	if p.Line < 0 {
		return 0
	}
	// Find the byte index where line p.Line begins.
	line := 0
	lineStart := 0
	for i := 0; i < len(text) && line < p.Line; i++ {
		if text[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	if line < p.Line {
		// Line is past the end of the text — clamp to end.
		return len(text)
	}
	// Walk forward `p.Character` bytes from the line start, but stop
	// at end-of-line or end-of-text, whichever comes first.
	pos := lineStart + p.Character
	for j := lineStart; j < pos && j < len(text); j++ {
		if text[j] == '\n' {
			return j
		}
	}
	if pos > len(text) {
		return len(text)
	}
	return pos
}

func (s *server) handleDidClose(msg *rawMessage) {
	var p didCloseParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	s.mu.Lock()
	delete(s.buffers, p.TextDocument.URI)
	s.mu.Unlock()
	// Publish empty diagnostics so the editor clears any prior ones.
	_ = s.t.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         p.TextDocument.URI,
		Diagnostics: []lspDiagnostic{},
	})
}

// publish runs lint on the buffer for the given URI and pushes the
// resulting diagnostics to the client. A lint panic is converted into
// a visible "internal-error" diagnostic at the top of the file so the
// user sees that something went wrong instead of stale diagnostics
// quietly hanging around.
func (s *server) publish(uri string) {
	s.mu.Lock()
	text, ok := s.buffers[uri]
	s.mu.Unlock()
	if !ok {
		return
	}

	path := uriToPath(uri)
	diags := safeAnalyzeFile(path, []byte(text))

	out := make([]lspDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, lspDiagnostic{
			Range: lspRange{
				Start: lspPosition{Line: d.Span.StartLine - 1, Character: d.Span.StartCol - 1},
				End:   lspPosition{Line: d.Span.EndLine - 1, Character: d.Span.EndCol - 1},
			},
			Severity: severityToLSP(d.Severity),
			Code:     d.Code,
			Source:   "pho-lsp",
			Message:  d.Message,
		})
	}

	_ = s.t.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Diagnostics: out,
	})
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

// uriToPath turns a `file://` URI into a filesystem path. Used for
// pkg/lint, which keys diagnostics on path strings (so the .pho/.phl
// suffix can be detected).
func uriToPath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	u, err := url.Parse(uri)
	if err != nil {
		return strings.TrimPrefix(uri, "file://")
	}
	return u.Path
}

// safeAnalyzeFile runs lint.AnalyzeFile under a recover. On panic it
// returns a single diagnostic pointing at line 1 so the editor's
// problems pane visibly indicates the server hit an internal error
// (and the log file has the stack trace).
func safeAnalyzeFile(path string, src []byte) (diags []lint.Diagnostic) {
	defer func() {
		if r := recover(); r != nil {
			logf("panic in lint.AnalyzeFile %s: %v\n%s", path, r, debug.Stack())
			diags = []lint.Diagnostic{{
				File:     path,
				Span:     core.Span{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 2},
				Severity: lint.SeverityError,
				Code:     "internal-error",
				Message:  fmt.Sprintf("pho-lsp internal error: %v (see %s)", r, logPath()),
			}}
		}
	}()
	return lint.AnalyzeFile(path, src)
}

func severityToLSP(s lint.Severity) int {
	switch s {
	case lint.SeverityError:
		return 1
	case lint.SeverityWarning:
		return 2
	case lint.SeverityInfo:
		return 3
	case lint.SeverityHint:
		return 4
	}
	return 1
}
