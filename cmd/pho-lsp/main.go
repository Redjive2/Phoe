// Command pho-lsp is the Pho language server. It speaks LSP over
// stdin/stdout: clients (editors) connect by spawning the binary as a
// subprocess. The server runs pkg/lint on each open buffer and
// publishes diagnostics on every change.
//
// Currently implements:
//
//	initialize / initialized
//	textDocument/didOpen, didChange, didClose
//	textDocument/publishDiagnostics
//	textDocument/semanticTokens/full
//	textDocument/completion          (scope names + dot members)
//	textDocument/onTypeFormatting    (closer auto-balancing)
//	textDocument/hover, definition, documentSymbol, references
//	shutdown / exit
//
// Everything else is rejected with MethodNotFound.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"unicode/utf8"

	"pho/pkg/annot"
	"pho/pkg/core"
	"pho/pkg/lint"
	"pho/pkg/syntax"
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

	// workspaceRoot is the directory cross-file reference search may
	// scan, learned from the initialize request (first workspace
	// folder, else rootUri/rootPath, else the process cwd).
	workspaceRoot string

	shutdown bool
}

func newServer(in io.Reader, out io.Writer) *server {
	s := &server{
		t:       newTransport(in, out),
		buffers: make(map[string]string),
	}
	// Cross-file analysis (package siblings, imported packages,
	// workspace reference search) must see unsaved edits: serve open
	// buffers from memory, everything else from disk.
	lint.SetSourceReader(func(path string) ([]byte, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for uri, text := range s.buffers {
			if uriToPath(uri) == path {
				return []byte(text), nil
			}
		}
		return os.ReadFile(path)
	})
	// The analysis entrypoints recover their own panics and return empty
	// results (a feature degrades to "no result" instead of erroring or
	// crashing); record the trace so the underlying bug stays diagnosable.
	lint.PanicHook = func(op string, r any, stack []byte) {
		logf("recovered panic in lint.%s: %v\n%s", op, r, stack)
	}
	syntax.OnPanic = func(op string, r any, stack []byte) {
		logf("recovered panic in syntax.%s: %v\n%s", op, r, stack)
	}
	return s
}

// maxConsecutiveLoopPanics bounds how many times in a row the read/
// dispatch loop may recover a panic without a single message getting
// through. A panic in readMessage can leave the byte stream desynced, so
// retrying may panic again on the same bytes; rather than hot-spin
// forever we give up after this many, exit nonzero, and let the editor
// restart us from a clean state.
const maxConsecutiveLoopPanics = 16

func (s *server) run() error {
	consecutivePanics := 0
	for {
		panicked, err := s.step()
		if err != nil {
			return err
		}
		if panicked {
			consecutivePanics++
			if consecutivePanics >= maxConsecutiveLoopPanics {
				return fmt.Errorf("read/dispatch loop wedged: %d consecutive panics", consecutivePanics)
			}
			continue
		}
		consecutivePanics = 0
	}
}

// step reads and dispatches exactly one message. It is the outermost
// safety net: a panic anywhere on the main goroutine that isn't already
// caught by safeDispatch — most importantly in readMessage itself, which
// runs outside it — is recovered here so one bad message or latent bug
// can't kill the long-running server. Returns panicked=true when it
// recovered (the caller guards against a wedged stream) and a non-nil
// err only for a genuine transport tear-down (EOF, closed pipe).
func (s *server) step() (panicked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			logf("panic in read/dispatch loop: %v\n%s", r, debug.Stack())
			panicked = true
		}
	}()

	msg, rerr := s.t.readMessage()
	if rerr != nil {
		// A garbled body inside intact framing isn't fatal — the stream is
		// aligned on the next message. Report and go on; only real
		// transport errors end the session.
		if errors.Is(rerr, errMalformedBody) {
			logf("skipping malformed message: %v", rerr)
			_ = s.t.replyError(json.RawMessage("null"), codeParseError, "parse error")
			return false, nil
		}
		return false, rerr
	}
	s.safeDispatch(msg)
	return false, nil
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
				_ = s.t.replyError(msg.ID, codeInternalError, fmt.Sprintf("internal error: %v", r))
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

	// After `shutdown`, the spec requires every further request (except
	// `exit`) be answered with InvalidRequest; lingering notifications are
	// dropped. Without this the server would keep servicing requests as if
	// still live.
	if s.shutdown && msg.Method != "exit" {
		if len(msg.ID) > 0 {
			_ = s.t.replyError(msg.ID, codeInvalidRequest, "server is shutting down")
		}
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
	case "textDocument/didSave":
		s.handleDidSave(msg)
	case "textDocument/semanticTokens/full":
		s.handleSemanticTokens(msg)
	case "textDocument/completion":
		s.handleCompletion(msg)
	case "textDocument/onTypeFormatting":
		s.handleOnTypeFormatting(msg)
	case "textDocument/hover":
		s.handleHover(msg)
	case "textDocument/definition":
		s.handleDefinition(msg)
	case "textDocument/documentSymbol":
		s.handleDocumentSymbol(msg)
	case "textDocument/references":
		s.handleReferences(msg)
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
		// Unknown request — JSON-RPC prescribes MethodNotFound, which
		// also tells the client not to expect the capability.
		// Notifications get ignored.
		if len(msg.ID) > 0 {
			_ = s.t.replyError(msg.ID, codeMethodNotFound, "method not found: "+msg.Method)
		}
	}
}

// ----------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------

func (s *server) handleInitialize(msg *rawMessage) {
	var p initializeParams
	if err := json.Unmarshal(msg.Params, &p); err == nil {
		switch {
		case len(p.WorkspaceFolders) > 0 && p.WorkspaceFolders[0].URI != "":
			s.workspaceRoot = uriToPath(p.WorkspaceFolders[0].URI)
		case p.RootURI != "":
			s.workspaceRoot = uriToPath(p.RootURI)
		case p.RootPath != "":
			s.workspaceRoot = p.RootPath
		}
	}
	if s.workspaceRoot == "" {
		s.workspaceRoot, _ = os.Getwd()
	}
	logf("workspace root: %q", s.workspaceRoot)

	// The parse-time annotation macros (so `--@ (sig! ...)` resolves rather
	// than reporting "macro not defined") are not loaded here. The linter
	// loads them lazily the first time it analyzes a file that carries
	// annotations, resolved relative to that file — which is more robust than
	// guessing a layout off the workspace root. didSave then reloads them if
	// the library's own source changes (see handleDidSave).

	result := initializeResult{
		Capabilities: serverCapabilities{
			TextDocumentSync: textDocumentSyncOptions{
				OpenClose: true,
				Change:    2, // incremental
				Save:      &saveOptions{IncludeText: false},
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
			// Closer auto-balancing (pkg/syntax BalanceClosers): newline
			// is the main trigger — it fires after Enter, when the
			// cursor's indent reveals which open forms are finished.
			// Closers are also natural "I'm wrapping up" signals.
			DocumentOnTypeFormattingProvider: &documentOnTypeFormattingOptions{
				FirstTriggerCharacter: "\n",
				MoreTriggerCharacter:  []string{")", "]", "}"},
			},
			HoverProvider:          true,
			DefinitionProvider:     true,
			DocumentSymbolProvider: true,
			ReferencesProvider:     true,
		},
		ServerInfo: &serverInfo{Name: "pho-lsp", Version: "0.2.0"},
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

	if !s.applyChanges(p.TextDocument.URI, p.ContentChanges) {
		return
	}

	s.publish(p.TextDocument.URI)
}

// applyChanges splices a didChange batch into the URI's buffer. Returns
// false for documents that aren't open: absence from s.buffers means "not
// open", and fabricating an entry from the zero value would resurrect
// closed documents and publish phantom diagnostics for them. The unlock is
// deferred so a panic in splicing can't leave the server-wide mutex held.
func (s *server) applyChanges(uri string, changes []textDocumentContentChange) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	text, ok := s.buffers[uri]
	if !ok {
		logf("didChange for unopened document %q — ignored", uri)
		return false
	}
	for _, change := range changes {
		if change.Range == nil {
			// Full-text replacement (some clients send this even when
			// we advertise incremental).
			text = change.Text
			continue
		}
		text = applyChange(text, *change.Range, change.Text)
	}
	s.buffers[uri] = text
	return true
}

// applyChange splices `replacement` into `text` over the LSP range.
func applyChange(text string, r lspRange, replacement string) string {
	startOff := offsetForLSP(text, r.Start)
	endOff := offsetForLSP(text, r.End)
	if endOff < startOff {
		return text // backwards range — drop rather than corrupt
	}
	return text[:startOff] + replacement + text[endOff:]
}

// offsetForLSP converts an LSP position (0-based line, 0-based character
// in UTF-16 code units) to a byte offset in `text`. The conversion is
// lenient: negative positions clamp to the start, positions past the end
// of a line clamp to that line's end, and positions past the end of the
// text clamp to len(text). Editors (Zed included) routinely reference
// "the position after the last char" when appending, especially when the
// file lacks a trailing newline; treating those as -1 silently dropped
// changes and made the buffer go stale.
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
	lineEnd := lineStart
	for lineEnd < len(text) && text[lineEnd] != '\n' {
		lineEnd++
	}
	return lineStart + utf16ColToByte(text[lineStart:lineEnd], p.Character)
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

// handleDidSave reloads the annotation-macro library when the file just
// saved belongs to it. That library is loaded lazily and cached — a
// remembered parse failure included — so without this a long-lived session
// would keep serving a stale (possibly broken) library after the user fixes
// and saves it, until the editor restarted the server. The loader reads from
// disk, which is why this hooks save (the file is now on disk) rather than
// didChange (which only updates the in-memory buffer).
func (s *server) handleDidSave(msg *rawMessage) {
	var p didSaveParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	path := uriToPath(p.TextDocument.URI)

	// Macro sources are .phl files sitting directly in the library directory
	// (the loader reads only .phl files, and only at the top of the package
	// dir). Resolve that directory the same way the linter does — relative to
	// the file, walking up to "std/annot" — and require the saved file to be
	// a member. The fallback raw "std/annot" never matches an absolute dir, so
	// an unresolved library simply skips the reload.
	if !strings.HasSuffix(path, ".phl") {
		return
	}
	macrosDir := lint.ResolveImport(path, "std/annot")
	if macrosDir == "" || filepath.Dir(path) != macrosDir {
		return
	}

	logf("annotation-macro library saved (%s) — reloading", path)
	annot.ReloadDefault(macrosDir)
	// Re-lint every open buffer: annotations that reported "macro not
	// defined" against the old library must re-evaluate against the new one.
	s.republishAll()
}

// republishAll re-runs diagnostics for every open buffer. Used after the
// annotation-macro library reloads, since that changes the result of
// analyzing any file carrying annotations. Buffer URIs are snapshotted under
// the lock, then published without it (publish takes the lock itself).
func (s *server) republishAll() {
	s.mu.Lock()
	uris := make([]string, 0, len(s.buffers))
	for uri := range s.buffers {
		uris = append(uris, uri)
	}
	s.mu.Unlock()
	for _, uri := range uris {
		s.publish(uri)
	}
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

	lines := strings.Split(text, "\n")

	out := make([]lspDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, lspDiagnostic{
			Range: lspRange{
				Start: toLSPPosition(lines, d.Span.StartLine, d.Span.StartCol),
				End:   toLSPPosition(lines, d.Span.EndLine, d.Span.EndCol),
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
// Position encoding
//
// The lexer (pkg/syntax) counts lines and columns 1-based, with columns
// in BYTES. LSP speaks 0-based lines and columns in UTF-16 code units.
// Both directions of the conversion need the document text, so they live
// here rather than in pkg/lint.
// ----------------------------------------------------------------------

// utf16ColToByte converts a 0-based UTF-16 column to a byte offset within
// a single line, clamping to the line's bounds (including mid-surrogate
// positions, which snap to the following rune).
func utf16ColToByte(line string, col int) int {
	if col <= 0 {
		return 0
	}
	units := 0
	for i, r := range line {
		if units >= col {
			return i
		}
		units++
		if r > 0xFFFF {
			units++ // surrogate pair
		}
	}
	return len(line)
}

// byteColToUTF16 converts a byte offset within a single line to a 0-based
// UTF-16 column, clamping to the line's bounds (a mid-rune offset snaps
// to the rune's start).
func byteColToUTF16(line string, byteCol int) int {
	if byteCol < 0 {
		byteCol = 0
	}
	if byteCol > len(line) {
		byteCol = len(line)
	}
	for byteCol > 0 && byteCol < len(line) && !utf8.RuneStart(line[byteCol]) {
		byteCol--
	}
	units := 0
	for _, r := range line[:byteCol] {
		units++
		if r > 0xFFFF {
			units++
		}
	}
	return units
}

// toLSPPosition converts the lexer's 1-based (line, byte column) into an
// LSP 0-based (line, UTF-16 column) against the split document text.
func toLSPPosition(lines []string, line1, byteCol1 int) lspPosition {
	line0 := line1 - 1
	if line0 < 0 {
		line0 = 0
	}
	if line0 >= len(lines) {
		line0 = len(lines) - 1
	}
	return lspPosition{Line: line0, Character: byteColToUTF16(lines[line0], byteCol1-1)}
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
