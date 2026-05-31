# Notes

Loose operational notes for working on Pho. Things worth remembering
that don't fit in the source comments.

## LSP changes look stale after rebuild

If a fix to `pkg/lint` (or anywhere else the LSP links in) doesn't
seem to land in Zed even after rebuilding `pho-lsp`, restarting the
language server, and reinstalling the extension — check for a stale
binary on `$PATH` ahead of the one you updated.

`go install ./cmd/pho-lsp` drops the binary in `~/go/bin`, and
manual builds usually land in `/usr/local/bin`. Zed inherits a
system PATH that puts `~/go/bin` first, so a months-old `go install`
silently wins over a fresh `cp` to `/usr/local/bin`.

```sh
find / -name 'pho-lsp' -type f 2>/dev/null   # see every copy
ls -la $(which -a pho-lsp)                   # check timestamps
```

Either rebuild both, or delete the ones you don't want and keep one
canonical install location.

Symptom that pointed us at this: tree-sitter highlighting picked up
a new builtin (correct color) but the LSP still reported it as an
unresolved identifier. Tree-sitter runs in-editor and doesn't go
through the LSP, so the two disagreeing is the giveaway that the LSP
binary is stale.
