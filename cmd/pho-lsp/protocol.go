package main

// Minimal LSP protocol types — only what pho-lsp uses. The spec is
// huge; we replicate just enough.
//
// Field tags use omitempty so we don't emit JSON keys for unset
// fields; the LSP client tolerates absent optional fields.

// initializeParams: the first request the client sends. We read just
// enough to learn the workspace root (for cross-file reference
// search); capabilities are ignored.
type initializeParams struct {
	RootURI          string            `json:"rootUri"`
	RootPath         string            `json:"rootPath"`
	WorkspaceFolders []workspaceFolder `json:"workspaceFolders"`
}

type workspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   *serverInfo        `json:"serverInfo,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type serverCapabilities struct {
	TextDocumentSync                 textDocumentSyncOptions          `json:"textDocumentSync"`
	SemanticTokensProvider           semanticTokensOptions            `json:"semanticTokensProvider"`
	CompletionProvider               completionOptions                `json:"completionProvider"`
	DocumentOnTypeFormattingProvider *documentOnTypeFormattingOptions `json:"documentOnTypeFormattingProvider,omitempty"`
	HoverProvider                    bool                             `json:"hoverProvider,omitempty"`
	DefinitionProvider               bool                             `json:"definitionProvider,omitempty"`
	DocumentSymbolProvider           bool                             `json:"documentSymbolProvider,omitempty"`
	ReferencesProvider               bool                             `json:"referencesProvider,omitempty"`
}

type documentOnTypeFormattingOptions struct {
	FirstTriggerCharacter string   `json:"firstTriggerCharacter"`
	MoreTriggerCharacter  []string `json:"moreTriggerCharacter,omitempty"`
}

type semanticTokensOptions struct {
	Legend semanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full"`
	Range  bool                 `json:"range"`
}

type semanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type completionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

// textDocumentSyncOptions: incremental sync (Change = 2) plus
// open/close notifications. Save is advertised so the client sends
// didSave — the server uses it to notice edits to the annotation-macro
// library on disk and reload it (the loader reads from disk, so an
// in-memory didChange wouldn't reflect the new macros).
type textDocumentSyncOptions struct {
	OpenClose bool         `json:"openClose"`
	Change    int          `json:"change"` // 0 None, 1 Full, 2 Incremental
	Save      *saveOptions `json:"save,omitempty"`
}

// saveOptions: we don't need the saved text (the loader re-reads the
// file from disk), so includeText stays false.
type saveOptions struct {
	IncludeText bool `json:"includeText"`
}

// ----- textDocument/did{Open,Change,Close} -----

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type didChangeParams struct {
	TextDocument   versionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []textDocumentContentChange     `json:"contentChanges"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// textDocumentContentChange: full sync entries omit Range (the Text is
// the whole new buffer); incremental entries include a Range and the
// Text replaces just that range.
type textDocumentContentChange struct {
	Range *lspRange `json:"range,omitempty"`
	Text  string    `json:"text"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

// didSaveParams: Text is present only if the server asked for it via
// saveOptions.includeText (we don't), so it's normally empty.
type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         string                 `json:"text,omitempty"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

// ----- textDocument/publishDiagnostics -----

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity,omitempty"` // 1=Error 2=Warning 3=Info 4=Hint
	Code     string   `json:"code,omitempty"`
	Source   string   `json:"source,omitempty"`
	Message  string   `json:"message"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

// LSP positions are 0-based; ours are 1-based. Conversion is in
// server.go.
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// ----- textDocument/semanticTokens/full -----

type semanticTokensParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type semanticTokensResult struct {
	Data []uint32 `json:"data"`
}

// ----- textDocument/onTypeFormatting -----

type documentOnTypeFormattingParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
	Ch           string                 `json:"ch"`
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// ----- textDocument/{hover,definition,references,documentSymbol} -----

// textDocumentPositionParams is the shared request shape for hover,
// definition, and references.
type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
	Range    *lspRange     `json:"range,omitempty"`
}

type markupContent struct {
	Kind  string `json:"kind"` // "markdown" | "plaintext"
	Value string `json:"value"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type documentSymbolParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          lspRange         `json:"range"`
	SelectionRange lspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

// ----- textDocument/completion -----

type completionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
}

type completionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []completionItem `json:"items"`
}

type completionItem struct {
	Label      string `json:"label"`
	Kind       int    `json:"kind,omitempty"`
	Detail     string `json:"detail,omitempty"`
	FilterText string `json:"filterText,omitempty"` // text the client prefix-matches against
	InsertText string `json:"insertText,omitempty"` // text inserted on accept (defaults to Label)
}
