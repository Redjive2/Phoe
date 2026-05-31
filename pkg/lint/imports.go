package lint

import (
	"os"
	"path/filepath"
	"unicode"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// PackageExports reads the directory at `path` and returns the set of
// names that an `(import "path")` would expose to other packages.
// Mirrors modload's runtime export rule: a top-level fun, method, or
// struct whose name starts with an uppercase letter. Const decls
// don't qualify — the runtime's export pass refuses to export
// non-callable values, so a hypothetical `cards.PI` would always
// fail at runtime even if `(const 'PI 3.14)` exists.
//
// Path is interpreted exactly as modload does: relative to the
// process cwd. If the directory doesn't exist or contains no
// readable .pho/.phl files, returns nil — the caller treats that as
// "I can't validate this import" and stays silent rather than
// drowning the user in `package not found` noise (the LSP's cwd is
// often not the project root).
//
// No caching: at LSP rates this is roughly "read 2-3 small files
// per import per keystroke", which is cheap and avoids stale data
// when the user edits an imported package's source.
func PackageExports(path string) map[string]Definition {
	if path == "" {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}

	exports := map[string]Definition{}
	any := false

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Only library files contribute to a package's import surface.
		// Sibling .pho programs are entrypoint scripts whose decls
		// the runtime never adds to pkg.Exports, so the linter
		// mirrors that and ignores them here.
		if fileMode(e.Name()) != "library" {
			continue
		}
		any = true

		full := filepath.Join(path, e.Name())
		src, err := os.ReadFile(full)
		if err != nil {
			continue
		}

		tokens, _ := syntax.LexPos(string(src))
		tree, _ := syntax.ParsePos(tokens)

		for _, form := range tree {
			collectExportedDecls(exports, form)
		}
	}

	if !any {
		return nil
	}
	return exports
}

// collectExportedDecls inspects one top-level form and adds any
// capitalized fun/method/struct declaration to `out`. Anything else
// (const, import, var) is ignored — those aren't reachable across
// packages even when the name happens to be capitalized.
func collectExportedDecls(out map[string]Definition, form core.PNode) {
	br, ok := asList(form)
	if !ok {
		return
	}

	add := func(name string, span core.Span, kind DefKind) {
		if name == "" {
			return
		}
		if !unicode.IsUpper(rune(name[0])) {
			return
		}
		out[name] = Definition{Name: name, Kind: kind, Span: span}
	}

	switch headIdent(br) {
	case "fun":
		// (fun 'name '(args) '(body)) — anonymous (2-arg) form has
		// no name to export.
		if len(br.Children) >= 3 {
			if name, span, ok := quotedIdent(br.Children[1]); ok {
				add(name, span, DefFun)
			}
		}
	case "method":
		if len(br.Children) >= 4 {
			if name, span, ok := quotedIdent(br.Children[2]); ok {
				add(name, span, DefMethod)
			}
		}
	case "struct":
		if len(br.Children) >= 2 {
			if name, span, ok := quotedIdent(br.Children[1]); ok {
				add(name, span, DefStruct)
			}
		}
	}
}
