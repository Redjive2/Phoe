package lint

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/syntax"
)

// Workspace discovery for cross-file reference search: which files
// exist under the workspace root, and which of them import a given
// package. Scans run per-request — Pho workspaces are small (tens of
// files) and the prefilter below skips full parses for files that
// can't possibly import the target. If this ever measures slow, an
// mtime-keyed cache is the next step.

// sourceFilesUnder lists every .pho / .phl file under root,
// skipping dot-directories (.git, editor state, etc.). Paths come
// back absolute when root is.
func sourceFilesUnder(root string) []string {
	if root == "" {
		return nil
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree — skip, don't abort the scan
		}
		if d.IsDir() {
			if name := d.Name(); strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if fileMode(d.Name()) != "" {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// importersOf returns the workspace files whose imports resolve to
// pkgDir (an absolute, cleaned package directory). The importing
// package's own files are not included — siblings reach decls by bare
// name, not by import, and the caller handles them separately.
func importersOf(root, pkgDir string) []string {
	if root == "" || pkgDir == "" {
		return nil
	}
	needle := []byte(filepath.Base(pkgDir))

	var importers []string
	for _, file := range sourceFilesUnder(root) {
		if filepath.Dir(file) == pkgDir {
			continue
		}
		src, err := readSource(file)
		if err != nil {
			continue
		}
		// Prefilter: an import string reaching pkgDir must mention its
		// last path segment somewhere in the file.
		if !bytes.Contains(src, needle) {
			continue
		}
		for _, imp := range importPathsIn(src) {
			if resolveImportPath(file, imp) == pkgDir {
				importers = append(importers, file)
				break
			}
		}
	}
	return importers
}

// importPathsIn extracts the path strings of every top-level
// `(import ...)` form in src — both the bare-string and the
// ["path" 'alias] tuple shapes. goimports are skipped (no Pho-side
// directory).
func importPathsIn(src []byte) []string {
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	var paths []string
	for _, form := range tree {
		br, ok := asList(form)
		if !ok || headIdent(br) != "import" {
			continue
		}
		for _, arg := range br.Children[1:] {
			if path, ok := stringLiteral(arg); ok {
				paths = append(paths, path)
				continue
			}
			if abr, ok := arg.(*ast.PBranch); ok && abr.Open == "(" && len(abr.Children) == 2 {
				if path, ok := stringLiteral(abr.Children[0]); ok {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}
