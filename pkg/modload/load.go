// Package modload provides the Pho package loader: it reads a directory
// of .pho and .phl files, evaluates them under a fresh package env, and
// caches the result for subsequent imports of the same path.
//
// .pho ("program") files allow arbitrary expressions at the top level —
// function calls, control flow, anything. .phl ("library") files only
// allow declarations and imports at the top level; any side-effecting
// form is rejected before evaluation.
//
// To avoid an import cycle with builtins (which contains the (import)
// surface form that calls back into LoadPackage), modload does not import
// builtins directly. Instead, callers must populate the EnvFactory variable
// with a function returning a fully-populated env. The builtins package
// does this in its init().
package modload

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"pho/pkg/core"
	"pho/pkg/diag"
	"pho/pkg/syntax"
)

// EnvFactory is the function used to construct a fresh package env with
// all builtins installed. Set by `builtins.init()` so modload can use it
// without importing builtins (which itself imports modload).
// Notably: I hate this.
var EnvFactory func() core.Env

// AnnotationStasher, if set, is called for each file right after its
// top-level forms are parsed; it evaluates the file's parse-time
// annotations and stashes them on file.Annotations. Set by pkg/annot's
// init so modload need not import annot (which imports modload). The tree
// is the []ast.PNode the loader holds, passed as `any` to keep modload
// free of a pkg/ast import.
var AnnotationStasher func(tree any, file *core.File)

var (
	packageCache    = map[string]*core.Package{}
	loadingPackages = map[string]bool{}
	parseFailed     = map[string]*ParseFailedError{}
	session         *diag.Session
)

// SetSession installs the run-wide diagnostic session that loader and
// evaluation errors report through. Package-level (alongside
// packageCache) because LoadPackage's signature is shared with the
// import builtin; core itself stays free of global state. A nil session
// degrades to plain one-line stderr reports.
func SetSession(s *diag.Session) { session = s }

// Invalidate drops any cached load (a success or a remembered parse
// failure) for the package at path, so the next LoadPackage re-reads it
// from disk. A long-lived host (the LSP) uses this to pick up edits to a
// package — notably the annotation-macro library — without a restart.
func Invalidate(path string) {
	path = filepath.Clean(path)
	delete(packageCache, path)
	delete(parseFailed, path)
}

// ParseFailedError reports that a package was not evaluated because its
// sources failed to lex or parse. The individual diagnostics were
// already emitted through the session; this error carries the tally so
// the CLI can pick the parse-specific exit code.
type ParseFailedError struct {
	Path  string
	Count int
}

func (e *ParseFailedError) Error() string {
	plural := ""
	if e.Count != 1 {
		plural = "s"
	}
	return fmt.Sprintf("%d parse error%s in '%s'", e.Count, plural, e.Path)
}

// libraryForms is the allow-list of head identifiers permitted at the
// top level of a .phl file. Anything else is a side effect and gets
// rejected by the loader.
var libraryForms = map[string]bool{
	"import":   true,
	"goimport": true,
	"fun":      true,
	"macro":    true,
	"method":   true,
	"struct":   true,
	"const":    true,
	// A named type alias `(type Name T)` binds a constant KindType — a
	// declaration (exported when capitalized), permitted at the top level.
	"type": true,
	// A top-level `var` declares module-level state: mutable from within
	// the module, exported (when capitalized) but read-only from outside
	// it (the `=` builtin rejects `(= pkg.Name v)`).
	"var": true,
	// A top-level `property` is a declaration: free-standing it binds a
	// faux variable (a getter/setter delegate, exported when capitalized);
	// attached `(property Recv.Name …)` registers a computed member on a
	// struct declared in the same module.
	"property": true,
}

// isLibraryForm returns true if a top-level node in a .phl file is an
// allowed declaration / import. The check is purely syntactic: we look
// at the head identifier of a list and consult libraryForms.
func isLibraryForm(form core.Node) bool {
	branch, ok := core.AsBranch(form)
	if !ok {
		return false // bare atoms at top level are side effects
	}
	if len(branch) == 0 {
		return false
	}
	head, ok := core.AsLeaf(branch[0])
	if !ok {
		return false // a call whose head is itself a call isn't a declaration
	}
	return libraryForms[string(head)]
}

// fileMode returns ModeProgram or ModeLibrary based on the filename's
// extension. Anything other than .pho / .phl is rejected by the caller
// before this is reached.
func fileMode(name string) string {
	if strings.HasSuffix(name, ".phl") {
		return core.ModeLibrary
	}
	return core.ModeProgram
}

// LoadPackage loads a Pho package (a directory of .pho / .phl files),
// caches it, and returns the *core.Package. Subsequent loads of the same
// path return the cached value. Cycles are detected and reported.
//
// Inside the package's own env, files are evaluated in lexicographic
// filename order, sharing one package-level frame. Capitalized identifiers
// from that frame are then collected into pkg.Exports — except when the
// load is a single-file .pho (a CLI script entrypoint), in which case
// the package has no import surface and the export pass is skipped.
// .pho files appearing alongside .phl files in a directory are ignored
// by the directory load (only .phl files are read), so a sibling script
// cannot suppress a library's exports.
func LoadPackage(path string) (*core.Package, error) { return loadPackage(path, false) }

// LoadMacroLibrary loads the annotation macro library with builtin-shadowing
// permitted, so its helper funcs (e.g. `type`, which backs `~type`) may rebind
// same-named builtins — making them an overlay distinct from the builtins in
// the isolated annotation env.
func LoadMacroLibrary(path string) (*core.Package, error) { return loadPackage(path, true) }

func loadPackage(path string, allowShadow bool) (*core.Package, error) {
	path = filepath.Clean(path)

	if pkg, ok := packageCache[path]; ok {
		return pkg, nil
	}

	// Negative cache: a package that failed to parse stays failed for the
	// whole run. Without this, every repeated import of a broken package
	// would re-parse it and re-emit each parse diagnostic.
	if pf, ok := parseFailed[path]; ok {
		return nil, pf
	}

	if loadingPackages[path] {
		return nil, fmt.Errorf("import cycle detected at '%s'", path)
	}

	loadingPackages[path] = true
	defer delete(loadingPackages, path)

	// LoadPackage accepts either a directory (the standard case — every
	// .pho/.phl file in it becomes part of the package) or a single
	// file (a one-file synthetic package, used by the pho CLI to
	// "run this script"). pkgDir is the directory we resolve filenames
	// against.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var (
		sourceFiles []string
		pkgDir      string
	)

	if info.IsDir() {
		// Importing a directory loads only library (.phl) files.
		// Program (.pho) files in the same directory are entrypoint
		// scripts — they aren't part of the package and their
		// declarations are intentionally not visible to importers.
		// (Programs may declare top-level `var`s, mix declarations
		// with arbitrary side effects, etc., none of which should
		// leak into a package's import surface.)
		pkgDir = path
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".phl") {
				sourceFiles = append(sourceFiles, name)
			}
		}
		sort.Strings(sourceFiles)
	} else {
		name := filepath.Base(path)
		if !strings.HasSuffix(name, ".pho") && !strings.HasSuffix(name, ".phl") {
			return nil, fmt.Errorf("unsupported file '%s' (expected .pho or .phl)", path)
		}
		pkgDir = filepath.Dir(path)
		sourceFiles = []string{name}
	}

	if len(sourceFiles) == 0 {
		return nil, fmt.Errorf("no .phl files found in package '%s' (.pho scripts in a directory are entrypoints, not importable members)", path)
	}

	if EnvFactory == nil {
		return nil, fmt.Errorf("modload.EnvFactory is not set; the builtins package must be imported to wire it up")
	}

	pkgEnv := EnvFactory()
	pkgEnv.AllowShadow = allowShadow
	pkg := &core.Package{
		Path:    path,
		Files:   make(map[string]*core.File),
		Exports: make(map[string]core.Value),
		Env:     &pkgEnv,
	}

	// Build the package-loading context. PushFrame here puts package-level
	// declarations above the builtin globals; the frame stays live for the
	// lifetime of the package (closures captured during loading reference
	// it).
	pkgCtx := core.Context{Env: pkg.Env, Package: pkg, Diag: session}
	pkgCtx.PushFrame()

	// Parse every file before evaluating any: a package with syntax
	// errors does not run at all, and the diagnostics for all of its
	// files surface in one pass. (The linter reports the same errors
	// statically; this is the runtime's own surfacing of them.)
	parseFailures := 0
	for _, fname := range sourceFiles {
		contents, err := os.ReadFile(filepath.Join(pkgDir, fname))
		if err != nil {
			return nil, err
		}

		// Positioned parser → syntactic-transform adapter → desugared
		// core.Node tree the existing builtins know how to walk.
		ptokens, lexErrs := syntax.LexPos(string(contents))
		ptree, parseErrs := syntax.ParsePos(ptokens)
		file := &core.File{
			FileName: fname,
			Path:     filepath.Join(pkgDir, fname),
			Src:      string(contents),
			Pkg:      pkg,
			Imports:  map[string]core.Value{},
			Tree:     syntax.Lower(ptree),
			Mode:     fileMode(fname),
		}
		pkg.Files[fname] = file

		if AnnotationStasher != nil {
			AnnotationStasher(ptree, file)
		}

		for _, e := range append(lexErrs, parseErrs...) {
			session.Emit(diag.RuntimeError{
				Diagnostic: diag.Diagnostic{
					File:     file.Path,
					Span:     e.Span,
					Severity: diag.SeverityError,
					Code:     diag.ErrParse,
					Message:  e.Message,
				},
				Source: file.Src,
			})
			parseFailures++
		}
	}

	if parseFailures > 0 {
		pf := &ParseFailedError{Path: path, Count: parseFailures}
		parseFailed[path] = pf
		return nil, pf
	}

	// strictAbort reports whether PHO_STRICT is set and an error has
	// already been reported — in which case the loader stops evaluating
	// further top-level forms rather than continuing in print-and-continue
	// mode. Checked between forms (and files), so it halts after the first
	// form that errors.
	strictAbort := func() bool {
		return session != nil && session.Strict && session.ErrorCount() > 0
	}

	// A single-file .pho load is a script being run by the CLI, not a
	// library being imported. Scripts have no import surface, so the
	// export pass is skipped: there's nothing to expose, and any
	// capitalized top-level decls (e.g. `Main`) are the script's own
	// business. This does not affect packages where .pho files happen
	// to live alongside .phl files — the directory-load branch above
	// already ignores .pho files when assembling a package, so a
	// sibling script can't "kill" a library's exports.
	isScriptEntrypoint := len(sourceFiles) == 1 && fileMode(sourceFiles[0]) == core.ModeProgram

	// Collect every top-level form across the package's files, gating .phl
	// files through the library allow-list. The parser returns a top-level
	// Branch whose children are the file's top-level forms.
	var forms []orderedForm
	for _, fname := range sourceFiles {
		file := pkg.Files[fname]
		fileCtx := pkgCtx.WithFile(file)
		if topLevel, ok := file.Tree.(core.Branch); ok {
			for _, form := range topLevel {
				if file.Mode == core.ModeLibrary && !isLibraryForm(form) {
					fileCtx.Errorf(core.ErrLibraryForm,
						"library file '%s' may only contain declarations and imports at the top level; rejected '%s'",
						fname, core.Inspect(form))
					continue
				}
				forms = append(forms, orderedForm{form: form, file: file})
			}
		} else if file.Tree != nil {
			forms = append(forms, orderedForm{form: file.Tree, file: file})
		}
	}

	// Order-agnostic library loading: lift every pure definition above the
	// side-effecting const/var so a declaration can reference another declared
	// further down. Program (.pho) files are not reordered — their top-level
	// expressions may have observable side effects.
	ordered := forms
	if !isScriptEntrypoint {
		ordered = liftDefinitions(forms)
	}

	for _, of := range ordered {
		if strictAbort() {
			break
		}
		evalTopLevel(pkgCtx.WithFile(of.file), of.file.FileName, of.form)
	}

	if !isScriptEntrypoint {
		// Every capitalized top-level binding is exported. Functions,
		// methods, and struct constructors are exposed as callables; var
		// and const bindings are exposed as read-only values — an importer
		// can read `pkg.Name` but cannot assign to it (see the `=` builtin's
		// package case and the Dot accessor, which reads the live binding).
		for name, entry := range pkg.Env.Stack[0] {
			if len(name) == 0 || name[0] == '#' {
				continue
			}
			pkg.Exports[name] = entry.Val
		}
	}

	packageCache[path] = pkg
	return pkg, nil
}

// evalTopLevel evaluates one top-level form under a recover that
// catches the non-local control-flow signals (return, break, continue).
// Those signals are only meaningful inside a function body (return) or
// a `for` loop (break/continue); if one reaches the loader, the user
// wrote `(return)` / `(break)` / `(continue)` outside its valid scope.
// We turn it into a clean error message instead of letting it crash
// the host. The linter flags these cases too, but the runtime guard
// is a backstop for unlinted code or AST-level shenanigans.
func evalTopLevel(ctx core.Context, fname string, form core.Node) {
	// Seed the current span from the form itself so the recover paths
	// below report a position: ctx is a value copy, so span updates made
	// during evaluation are invisible here — without this, a stray
	// signal or Go panic would render with no location at all.
	if sp, ok := core.SpanOf(form); ok {
		ctx.At = &sp
	}

	// The root of every Pho stack trace. Record the frame depth on entry
	// and truncate back to it afterward (rather than clearing outright):
	// a nested package load via `import` runs more top-level forms while
	// outer frames are still live, and those must be preserved. Truncate
	// also cleans up frames a foreign panic left behind (the per-call
	// pops are skipped during a panic unwind).
	base := ctx.Diag.Depth()
	ctx.PushCallFrame("<top level>")
	defer ctx.Diag.Truncate(base)

	defer func() {
		switch r := recover(); r.(type) {
		case nil:
		case core.ReturnSignal:
			ctx.Errorf(core.ErrTopLevelFlow,
				"'return' at the top level of '%s' — return is only valid inside a function or method body", fname)
		case core.BreakSignal:
			ctx.Errorf(core.ErrTopLevelFlow,
				"'break' at the top level of '%s' — break is only valid inside a 'for' loop", fname)
		case core.ContinueSignal:
			ctx.Errorf(core.ErrTopLevelFlow,
				"'continue' at the top level of '%s' — continue is only valid inside a 'for' loop", fname)
		case core.RecursionSignal:
			// The recursion guard fired deep in a call chain; the frame
			// stack is intact, so EmitPanic shows the (collapsed) trace.
			ctx.EmitPanic(core.ErrRecursion,
				fmt.Sprintf("recursion limit exceeded (%d calls)", core.MaxCallDepth()),
				"deep or infinite recursion — if this is intentional, raise the limit with PHO_MAX_DEPTH")
		default:
			// A foreign Go panic: the frame stack is still intact (the
			// per-call pops were skipped during unwind), so EmitPanic
			// snapshots a call-site trace. The precise throw point inside
			// the innermost frame is unknown, hence no excerpt.
			note := "this is likely a bug in the interpreter; re-run with PHO_DEBUG=1 for the Go stack"
			if core.DebugMode {
				note = "Go stack:\n" + string(debug.Stack())
			}
			ctx.EmitPanic(core.ErrGoPanic,
				fmt.Sprintf("runtime panic while evaluating '%s': %v", core.Inspect(form), r), note)
		}
	}()
	form.Evaluate(ctx)
}
