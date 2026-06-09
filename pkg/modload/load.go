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
	"sort"
	"strings"
	"unicode"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// EnvFactory is the function used to construct a fresh package env with
// all builtins installed. Set by `builtins.init()` so modload can use it
// without importing builtins (which itself imports modload).
var EnvFactory func() core.Env

var (
	packageCache    = map[string]*core.Package{}
	loadingPackages = map[string]bool{}
)

// libraryForms is the allow-list of head identifiers permitted at the
// top level of a .phl file. Anything else is a side effect and gets
// rejected by the loader.
var libraryForms = map[string]bool{
	"import":   true,
	"goimport": true,
	"fun":      true,
	"method":   true,
	"struct":   true,
	"const":    true,
}

// isLibraryForm returns true if a top-level node in a .phl file is an
// allowed declaration / import. The check is purely syntactic: we look
// at the head identifier of a list and consult libraryForms.
func isLibraryForm(form core.Node) bool {
	branch, ok := form.(core.Branch)
	if !ok {
		return false // bare atoms at top level are side effects
	}
	if len(branch) == 0 {
		return false
	}
	head, ok := branch[0].(core.Leaf)
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
func LoadPackage(path string) (*core.Package, error) {
	path = filepath.Clean(path)

	if pkg, ok := packageCache[path]; ok {
		return pkg, nil
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
	pkgCtx := core.Context{Env: pkg.Env, Package: pkg}
	pkgCtx.PushFrame()

	for _, fname := range sourceFiles {
		contents, err := os.ReadFile(filepath.Join(pkgDir, fname))
		if err != nil {
			return nil, err
		}

		// Positioned parser → syntactic-transform adapter → desugared
		// core.Node tree the existing builtins know how to walk.
		// Lex/parse errors are reported by the linter (cmd/pho-lint /
		// the LSP); modload silently consumes whatever recoverable tree
		// the parser produced.
		ptokens, _ := syntax.LexPos(string(contents))
		ptree, _ := syntax.ParsePos(ptokens)
		file := &core.File{
			FileName: fname,
			Pkg:      pkg,
			Imports:  map[string]core.Value{},
			Tree:     syntax.Lower(ptree),
			Mode:     fileMode(fname),
		}
		pkg.Files[fname] = file

		fileCtx := pkgCtx.WithFile(file)

		// The parser returns a top-level Branch whose children are the
		// file's top-level forms. Evaluate each in sequence rather than
		// dispatching the outer branch as a single call. For .phl files
		// each form is gated through the library allow-list.
		if topLevel, ok := file.Tree.(core.Branch); ok {
			for _, form := range topLevel {
				if file.Mode == core.ModeLibrary && !isLibraryForm(form) {
					fmt.Println("(ERR) library file '" + fname +
						"' may only contain declarations and imports at the top level; rejected '" +
						core.Inspect(form) + "' @ 'modload.LoadPackage'.")
					continue
				}
				evalTopLevel(fileCtx, fname, form)
			}
		} else if file.Tree != nil {
			evalTopLevel(fileCtx, fname, file.Tree)
		}
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

	if !isScriptEntrypoint {
		for name, entry := range pkg.Env.Stack[0] {
			if len(name) == 0 || !unicode.IsUpper(rune(name[0])) {
				continue
			}

			v := entry.Val
			if v.Kind != core.KindFun && v.Kind != core.KindMethod && v.Kind != core.KindConstructor {
				fmt.Println("(ERR): Cannot export symbol '" + name + "' of type '" + v.Kind + "'; only functions may be exported @ 'modload.LoadPackage'.")
				continue
			}

			pkg.Exports[name] = v
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
	defer func() {
		switch r := recover(); r.(type) {
		case nil:
		case core.ReturnSignal:
			fmt.Println("(ERR) 'return' at the top level of '" + fname +
				"' — return is only valid inside a function or method body @ 'modload.evalTopLevel'.")
		case core.BreakSignal:
			fmt.Println("(ERR) 'break' at the top level of '" + fname +
				"' — break is only valid inside a 'for' loop @ 'modload.evalTopLevel'.")
		case core.ContinueSignal:
			fmt.Println("(ERR) 'continue' at the top level of '" + fname +
				"' — continue is only valid inside a 'for' loop @ 'modload.evalTopLevel'.")
		default:
			fmt.Printf(">>> %s %s %s", fname, core.Inspect(form), fmt.Sprint(r))
		}
	}()
	form.Evaluate(ctx)
}
