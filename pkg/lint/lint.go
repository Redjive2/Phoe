// Package lint provides static analysis for Pho source code: parses
// .pho / .phl files into a positioned AST and runs a set of checkers
// against them, producing structured Diagnostics.
//
// This package has no LSP-shaped concerns; the future pho-lsp wraps it.
// It also has no IO concerns beyond reading source files for
// AnalyzePackage; AnalyzeFile takes raw bytes so an editor can lint
// unsaved buffers.
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// Severity is the LSP-aligned level of a diagnostic.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityInfo
	SeverityHint
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	case SeverityHint:
		return "hint"
	}
	return "unknown"
}

// Diagnostic is a single problem found in source code. Code is a stable
// short identifier (e.g. "no-top-level-var") that tooling can filter on.
type Diagnostic struct {
	File     string
	Span     core.Span
	Severity Severity
	Code     string
	Message  string
}

// Format renders a Diagnostic in GCC-style:
//
//	main.pho:5:3: error[no-top-level-var]: 'var' is not allowed at the top level
func (d Diagnostic) Format() string {
	return fmt.Sprintf("%s:%d:%d: %s[%s]: %s",
		d.File, d.Span.StartLine, d.Span.StartCol,
		d.Severity, d.Code, d.Message,
	)
}

// fileMode mirrors modload's classification: ".pho" is a program, ".phl"
// is a library, anything else returns "" and the caller should skip.
func fileMode(name string) string {
	switch {
	case strings.HasSuffix(name, ".pho"):
		return "program"
	case strings.HasSuffix(name, ".phl"):
		return "library"
	}
	return ""
}

// AnalyzeFile lints a single file's source text. The path is used purely
// for the File field of returned diagnostics and to determine .pho vs
// .phl from the extension.
func AnalyzeFile(path string, src []byte) []Diagnostic {
	mode := fileMode(path)
	if mode == "" {
		return nil
	}

	tokens, lexErrs := syntax.LexPos(string(src))
	tree, parseErrs := syntax.ParsePos(tokens)

	var diags []Diagnostic

	// Lex/parse errors come first — they explain why subsequent checks
	// might miss things or behave oddly.
	for _, e := range lexErrs {
		diags = append(diags, Diagnostic{
			File:     path,
			Span:     e.Span,
			Severity: SeverityError,
			Code:     "parse-error",
			Message:  e.Message,
		})
	}
	for _, e := range parseErrs {
		diags = append(diags, Diagnostic{
			File:     path,
			Span:     e.Span,
			Severity: SeverityError,
			Code:     "parse-error",
			Message:  e.Message,
		})
	}

	if mode == "library" {
		// Top-level `var` is rejected in libraries (cross-file
		// reasoning needs package-level decls to be immutable) but
		// allowed in programs (scripts are sequences of statements
		// with mutation as a core part of the model).
		diags = append(diags, checkNoTopLevelVar(path, tree)...)
		diags = append(diags, checkPhlSideEffects(path, tree)...)
	}

	// Scope-aware checks: redeclaration, unresolved-identifier,
	// set-on-constant. The walker fires all three as it descends.
	// The parent scope holds top-level decls from sibling .pho/.phl
	// files in the same directory so cross-file references resolve.
	w := newWalker(path)
	w.walkFile(tree, PackageScope(path))
	diags = append(diags, w.diagnostics...)

	sortDiagnostics(diags)
	return diags
}

// AnalyzePackage lints every .pho and .phl file in a directory.
// Diagnostics from all files are merged into one slice, sorted by file
// then position. Returns an error only for IO failures; per-file
// problems show up as Diagnostics.
func AnalyzePackage(path string) ([]Diagnostic, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if fileMode(e.Name()) != "" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var all []Diagnostic
	for _, name := range names {
		full := filepath.Join(path, name)
		src, readErr := os.ReadFile(full)
		if readErr != nil {
			return nil, readErr
		}
		all = append(all, AnalyzeFile(full, src)...)
	}
	return all, nil
}

func sortDiagnostics(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].File != ds[j].File {
			return ds[i].File < ds[j].File
		}
		if ds[i].Span.StartLine != ds[j].Span.StartLine {
			return ds[i].Span.StartLine < ds[j].Span.StartLine
		}
		return ds[i].Span.StartCol < ds[j].Span.StartCol
	})
}

// ----------------------------------------------------------------------
// Helpers shared by checkers
// ----------------------------------------------------------------------

// asList returns (branch, true) if n is a parenthesized form. Returns
// (nil, false) for atoms, arrays, or dicts.
func asList(n core.PNode) (*core.PBranch, bool) {
	br, ok := n.(*core.PBranch)
	if !ok || br.Open != "(" {
		return nil, false
	}
	return br, true
}

// headIdent returns the head identifier of a list, or "" if the head
// isn't a bare identifier (e.g. a quoted form, another list, an atom).
func headIdent(br *core.PBranch) string {
	if br == nil || len(br.Children) == 0 {
		return ""
	}
	leaf, ok := br.Children[0].(*core.PLeaf)
	if !ok {
		return ""
	}
	return leaf.Value
}
