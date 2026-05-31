package lint

import (
	"os"
	"path/filepath"

	"pho/pkg/syntax"
)

// PackageScope builds the scope a single-file lint should chain its
// file scope off of. Returns a freshly-built builtin → package chain:
// the package level is populated with top-level fun/method/struct/
// const declarations from every sibling .phl file in the same
// directory as `path` (excluding `path` itself).
//
// Two intentional asymmetries:
//   - Sibling .pho files are ignored. Programs are entrypoint scripts,
//     not package members; the runtime won't expose their decls to
//     anyone else, so the linter doesn't either.
//   - If `path` itself is a .pho program, no siblings are read at
//     all. A program runs in isolation (its own decls + builtins +
//     explicit imports); it doesn't auto-see package siblings.
//
// Imports are deliberately NOT propagated across files — Pho imports
// are file-scoped, matching Go's per-file import semantics. A sibling
// file's `(import "std/io")` doesn't make `io` visible elsewhere.
//
// On any IO or parse failure we fall back to a plain builtin scope.
// Sibling diagnostics are discarded — they belong to that sibling's
// own lint pass, not this one.
func PackageScope(path string) *Scope {
	builtin := newBuiltinScope()
	if fileMode(filepath.Base(path)) != "library" {
		// Programs (and anything else) don't have a package scope.
		return builtin
	}
	dir := filepath.Dir(path)
	if dir == "" {
		return builtin
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return builtin
	}

	pkg := newScope(builtin)
	pkg.IsPackage = true

	self := filepath.Base(path)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == self {
			continue
		}
		if fileMode(name) != "library" {
			// Skip .pho programs — they aren't package members, so
			// their decls aren't visible to .phl siblings.
			continue
		}

		full := filepath.Join(dir, name)
		src, err := os.ReadFile(full)
		if err != nil {
			continue
		}

		tokens, _ := syntax.LexPos(string(src))
		tree, _ := syntax.ParsePos(tokens)

		// Collect top-level decls from the sibling. We use a discarding
		// walker so any redeclaration / unresolved-identifier issues in
		// that file don't leak into this file's diagnostics — they'll
		// fire when that file is itself the target of a lint pass.
		w := &walker{file: full}
		w.collect(pkg, tree)
	}

	// Imports collected above are file-scoped, not package-scoped.
	// Strip them so cross-file references to a sibling's import alias
	// don't accidentally resolve.
	for name, def := range pkg.Defs {
		if def.Kind == DefImport {
			delete(pkg.Defs, name)
		}
	}

	return pkg
}
