package main

// Minimal LSP protocol types — only what pho-lsp uses. The spec is
// huge; we replicate just enough.
//
// Field tags use omitempty so we don't emit JSON keys for unset
// fields; the LSP client tolerates absent optional fields.

// initializeParams: the first request the client sends. We ignore
// most of it (capabilities, workspace folders, etc.) — we only care
// about the ID for responding.

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   *serverInfo        `json:"serverInfo,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type serverCapabilities struct {
	TextDocumentSync       textDocumentSyncOptions `json:"textDocumentSync"`
	SemanticTokensProvider semanticTokensOptions   `json:"semanticTokensProvider"`
	CompletionProvider     completionOptions       `json:"completionProvider"`
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
// open/close notifications.
type textDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"` // 0 None, 1 Full, 2 Incremental
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
	Label  string `json:"label"`
	Kind   int    `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}
