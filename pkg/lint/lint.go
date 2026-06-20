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
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/diag"
	"pho/pkg/syntax"
)

// Severity and Diagnostic live in pkg/diag, the leaf package that owns
// the diagnostic model. The aliases keep lint.s public surface (and its
// consumers: main.go, cmd/pho-lint, cmd/pho-lsp) source-compatible.
type Severity = diag.Severity

const (
	SeverityError   = diag.SeverityError
	SeverityWarning = diag.SeverityWarning
	SeverityInfo    = diag.SeverityInfo
	SeverityHint    = diag.SeverityHint
)

type Diagnostic = diag.Diagnostic

// PanicHook, if non-nil, is called when a top-level analysis entrypoint
// (HoverAt, DefinitionAt, CompletionsAt, …) recovers a panic. The
// entrypoints are panic-safe by contract — a latent analyzer bug on
// some odd mid-edit input degrades to an empty result rather than
// propagating to (and potentially destabilizing) the caller — and this
// hook lets the host still record the stack trace. The LSP wires it to
// its log file. Default nil: recover silently.
var PanicHook func(op string, recovered any, stack []byte)

// recoverEntry reports a recovered entrypoint panic through PanicHook.
// Call it from a deferred closure that also resets the function's named
// returns to their zero (safe) values:
//
//	func HoverAt(...) (md string, sp span.Span, ok bool) {
//	    defer func() { if r := recover(); r != nil { recoverEntry("HoverAt", r); md, sp, ok = "", span.Span{}, false } }()
//	    ...
//	}
func recoverEntry(op string, r any) {
	if PanicHook != nil {
		PanicHook(op, r, debug.Stack())
	}
}

// readSource is how every cross-file read in this package loads
// source text. An editor host can override it so analysis sees
// unsaved buffer contents instead of what's on disk.
var readSource = os.ReadFile

// SetSourceReader overrides the source loader used for cross-file
// analysis (package siblings, imported packages, declaration
// rendering, workspace reference search). Pass nil to restore plain
// disk reads. The reader is consulted for every file EXCEPT the one
// whose bytes were handed to AnalyzeFile directly.
func SetSourceReader(fn func(path string) ([]byte, error)) {
	if fn == nil {
		readSource = os.ReadFile
		return
	}
	readSource = fn
}

// fileMode mirrors modload's classification: ".pho" is a program, ".phl"
// is a library, anything else returns "" and the caller should skip.
func fileMode(name string) string {
	switch {
	case strings.HasSuffix(name, ".pho"):
		return core.ModeProgram
	case strings.HasSuffix(name, ".phl"):
		return core.ModeLibrary
	}
	return ""
}

// isLibrary reports whether name is a .phl library file. A predicate (vs a
// scattered `fileMode(x) == "library"`) keeps the mode-string in one place
// — fileMode — so callers across the package don't repeat the literal or
// each import core just to name the constant.
func isLibrary(name string) bool { return fileMode(name) == core.ModeLibrary }

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
	// Apply do-notation the way the runtime's Lower does, so the linter sees a
	// bare `do` as the (core.Do …) it becomes rather than counting it as an
	// extra argument to the enclosing form.
	tree = syntax.NormalizeDo(tree)

	var diags []Diagnostic

	// Lex/parse errors come first — they explain why subsequent checks
	// might miss things or behave oddly.
	for _, e := range lexErrs {
		diags = append(diags, Diagnostic{
			File:     path,
			Span:     e.Span,
			Severity: SeverityError,
			Code:     diag.ErrParse,
			Message:  e.Message,
		})
	}
	for _, e := range parseErrs {
		diags = append(diags, Diagnostic{
			File:     path,
			Span:     e.Span,
			Severity: SeverityError,
			Code:     diag.ErrParse,
			Message:  e.Message,
		})
	}

	if mode == core.ModeLibrary {
		// Libraries may only contain declarations and imports at the top
		// level — no side-effecting forms. Top-level `var` IS permitted:
		// it declares module-level state, mutable within the module but
		// read-only from outside it.
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
		src, readErr := readSource(full)
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
func asList(n ast.PNode) (*ast.PBranch, bool) {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "(" {
		return nil, false
	}
	return br, true
}

// headIdent returns the head identifier of a list, or "" if the head
// isn't a bare identifier (e.g. a quoted form, another list, an atom).
func headIdent(br *ast.PBranch) string {
	if br == nil || len(br.Children) == 0 {
		return ""
	}
	leaf, ok := br.Children[0].(*ast.PLeaf)
	if !ok {
		return ""
	}
	return leaf.Value
}
