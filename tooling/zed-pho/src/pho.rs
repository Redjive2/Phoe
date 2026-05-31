// zed-pho: the Rust shim that lets Zed launch the pho-lsp binary.
//
// The actual language server lives in `cmd/pho-lsp/` (Go). All this
// extension does is tell Zed where to find that binary on the user's
// PATH and how to spawn it. Zed compiles this Rust to WASM at install
// time and calls into it whenever it needs to start the language
// server for an open .pho / .phl file.

use zed_extension_api::{self as zed, Command, LanguageServerId, Result, Worktree};

struct PhoExtension;

impl zed::Extension for PhoExtension {
    fn new() -> Self {
        Self
    }

    fn language_server_command(
        &mut self,
        _server_id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        // We don't ship the LSP binary with the extension — users build
        // it from the Pho repo with `go install ./cmd/pho-lsp`.
        //
        // Two ways the binary can be found:
        //
        //   1. It's on Zed's PATH — `worktree.which` succeeds. (Note:
        //      macOS GUI apps don't always inherit shell PATH, so
        //      ~/go/bin is often missing here.)
        //   2. The user has set `lsp.pho-lsp.binary.path` in Zed's
        //      settings.json — that overrides whatever we return.
        //
        // We never error out: returning a literal "pho-lsp" lets path
        // 2 work even when path 1 fails. If neither path resolves,
        // Zed's own log will report a clear "binary not found" error
        // when it tries to spawn the server.
        let command = worktree
            .which("pho-lsp")
            .unwrap_or_else(|| "pho-lsp".to_string());

        Ok(Command {
            command,
            args: vec![],
            env: vec![],
        })
    }
}

zed::register_extension!(PhoExtension);
