# zed-pho

Zed editor extension for the [Pho](../../) language. Provides syntax
highlighting, bracket matching, comment toggling, an outline view, and
auto-indent — all driven by the
[tree-sitter-pho](../tree-sitter-pho) grammar.

## What you get

- **Syntax highlighting** — every form: lists, arrays, `[k -> v]` maps,
  `.{}` construction, blocks, macro calls, dot chains, slice forms, comments, etc.
- **Bracket matching / auto-pairing** for `(` `)` `[` `]` `{` `}` and `'`.
- **Comment toggle** — Cmd-/ toggles `--` line comments.
- **Outline view** — `fun`, `struct`, `method`, `property`, and static-member
  definitions.
- **Auto-indent** inside lists / arrays / dicts.
- **Diagnostics** (via the Pho LSP) — top-level `var`, `.phl` side
  effects, redeclarations, set-on-constant, unresolved identifiers.

## Enabling the LSP

The extension's Rust component (`src/pho.rs`) tells Zed how to spawn
the language server. It looks for a binary named `pho-lsp` on your
PATH — so you build the LSP from the Pho repo once and the extension
takes care of the rest.

### 1. Build the LSP binary

```sh
cd /path/to/pho
go install ./cmd/pho-lsp
```

That writes to `$GOBIN` (defaults to `$GOPATH/bin`, typically
`~/go/bin`). Make sure that directory is on your PATH:

```sh
echo $PATH | tr ':' '\n' | grep -q "$(go env GOPATH)/bin" && echo "ok" || echo "add $(go env GOPATH)/bin to PATH"
```

### 2. Reload the extension

After installing or updating `pho-lsp`, reload the extension so Zed
picks up the new binary:

- Command palette → **`zed: rebuild dev extension`** (if you've already
  installed it as a dev extension).
- Or restart Zed.

Open a `.pho` / `.phl` file. Errors should underline live as you type.

### Updating just the LSP

The Rust shim resolves the binary path on each LSP launch — no
extension reload needed when you update only the Go code:

```sh
cd /path/to/pho
go install ./cmd/pho-lsp
```

Then close and reopen the file (or restart the LSP via the command
palette: `zed: restart language server`).

## Status

Phase 2 — extension provides syntax highlighting (via
[tree-sitter-pho](../tree-sitter-pho)) **and** an LSP server (the
`pho-lsp` binary built from `cmd/pho-lsp/`). Set up for **local dev
installation** until both the grammar and the binary have proper
distribution paths.

## Local dev install

Three things to set up: the **tree-sitter parser** (compiled to a C
parser by tree-sitter-cli), the **LSP binary** (built from Go), and a
working **Rust toolchain** (Zed compiles the extension's Rust shim to
WASM at install time).

Prerequisites:

- Go ≥ 1.21
- Node.js (just for `tree-sitter-cli`; one-time)
- Rust toolchain (`rustup` is fine)

### 1. Generate the tree-sitter parser and make it a git repo

The grammar's C source is gitignored; you generate it once. Then we make
the directory a git repo so Zed can clone it.

```sh
cd tooling/tree-sitter-pho

# Build the parser
npm install                  # one-time: install the tree-sitter CLI
npx tree-sitter generate     # writes src/parser.c, src/grammar.json, ...
npx tree-sitter test         # optional: run the corpus tests

# Initialize as a standalone git repo so Zed can clone from it
git init
git add -A
git commit -m "init"

# Note the commit SHA for the next step
git rev-parse HEAD
```

### 2. Point the extension at your local repo

Edit `tooling/zed-pho/extension.toml`. Replace the placeholder
`[grammars.pho]` section with:

```toml
[grammars.pho]
repository = "file:///absolute/path/to/pho/tooling/tree-sitter-pho"
commit = "<sha from step 1>"
```

(Use the absolute path, including the `file://` prefix. `pwd` from inside
`tooling/tree-sitter-pho` will give you the absolute path.)

### 3. Build the LSP binary

```sh
cd /path/to/pho
go install ./cmd/pho-lsp
```

This must end up on your PATH — the Rust shim resolves it via PATH
lookup. If `go install` writes somewhere not on PATH, either add that
directory to PATH or `go build -o /usr/local/bin/pho-lsp ./cmd/pho-lsp`.

### 4. Install as a Zed dev extension

In Zed:

1. Open the command palette (⌘-Shift-P).
2. Run **`zed: install dev extension`**.
3. Pick the `tooling/zed-pho/` directory.

Zed clones the grammar from the `file://` URL at the pinned commit,
compiles the tree-sitter parser, compiles the Rust shim to WASM,
registers the language, and starts the LSP. Opening any `*.pho` /
`*.phl` file should highlight and show diagnostics live.

### Re-syncing after grammar changes

When `grammar.js` changes, you need to:

1. `cd tooling/tree-sitter-pho && npx tree-sitter generate`
2. `git add -A && git commit -m "..." && git rev-parse HEAD`
3. Update the `commit = "..."` line in `tooling/zed-pho/extension.toml`
   to the new SHA.
4. In Zed: command palette → **`zed: rebuild dev extension`** (or
   reinstall).

### 3. Verify

Open one of:

- `tooling/tree-sitter-pho/examples/showcase.pho` — synthetic file that
  exercises every grammar form.
- `script/std/io/io.phl` — a real stdlib file.

You should see:

- Comments in the dim/comment color.
- `'strings'` and `` `c` `` chars in the string color.
- Numbers (including `-5`) in the number color.
- `none` / `true` / `false` styled as constants.
- The `&` and `~` sigils colored as keyword operators.
- The head of each list (`fun`, `if`, `import`, `+`, `io.print_line`, …)
  treated as a function call / keyword / operator depending on which.
- After a `.`, the right-hand identifier styled as a property/field.
- Title_Snake_Case identifiers (`Point`, `Number`) styled as types.

If a category looks wrong, the fix is in
`languages/pho/highlights.scm` (mirror of
`tooling/tree-sitter-pho/queries/highlights.scm`).

## Layout

```
zed-pho/
├── extension.toml              manifest: grammar + language server entries
├── Cargo.toml                  Rust crate metadata
├── src/
│   └── pho.rs                 the WASM shim that tells Zed how to spawn pho-lsp
├── languages/
│   └── pho/                   tree-sitter queries (synced from tree-sitter-pho)
│       ├── config.toml         file extension, comments, brackets
│       ├── highlights.scm      node → highlight group mappings
│       ├── brackets.scm        bracket pair matching
│       ├── outline.scm         items shown in the outline view
│       ├── folds.scm           fold ranges
│       ├── indents.scm         auto-indent rules
│       └── locals.scm          scopes, definitions, references
└── README.md
```

The Rust crate is intentionally tiny — its only job is to tell Zed
where to find `pho-lsp` on PATH and how to invoke it. All of the
analysis logic lives in `pkg/lint` (Go); the LSP itself in
`cmd/pho-lsp` (Go). Updating the LSP's behavior is a `go install`
away — no need to recompile the Rust shim.

## Tweaking highlights live

Zed re-reads query files on extension reload, so iterating on
`highlights.scm` is fast:

1. Edit `languages/pho/highlights.scm`.
2. Command palette → **`zed: rebuild dev extension`** (or `reload extension`).
3. Re-open the `.pho` file to see the new highlights.

When the highlights stabilize, copy the file back to
`tooling/tree-sitter-pho/queries/highlights.scm` so the canonical
grammar repo stays in sync.

## Troubleshooting

- **"No grammar found for pho" / language doesn't activate**: confirm
  step 1 — `tooling/tree-sitter-pho/src/parser.c` should exist after
  `tree-sitter generate`.
- **"pho-lsp not found on PATH"** (Zed log): the Rust shim couldn't
  resolve the binary. Verify `which pho-lsp` works in your shell from
  the same environment Zed was launched in (Zed inherits its parent
  shell's PATH on launch).
- **Diagnostics don't appear**: check `zed: open log` for LSP startup
  errors. The Pho LSP logs nothing to stdout; any output there is a
  bug. Errors show up under `[lsp]` entries.
- **Highlights look wrong / nothing styled**: check Zed's log
  (`zed: open log` in the command palette). Tree-sitter query parse
  errors show up there with a line/column.
- **Outline view is empty**: the queries match the grammar's node names
  exactly; if `grammar.js` changed (e.g. a node was renamed), re-run
  `tree-sitter generate` and re-install the dev extension.
- **Rust build fails on install**: install/update Rust via `rustup`.
  Zed needs `wasm32-wasi` or similar to build the extension's Rust
  shim; rustup usually fetches the right target on its own.
