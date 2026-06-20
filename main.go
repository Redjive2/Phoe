// Pho binary entry point. Takes a single source-file argument and
// either runs it (.pho) or lints it (.phl).
//
// The blank import of pkg/builtins triggers the init() that wires
// builtins.NewEnv into modload.EnvFactory — without it, the package
// loader has no way to construct envs.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pho/pkg/diag"
	"pho/pkg/goop"
	"pho/pkg/lint"
	"pho/pkg/modload"

	_ "pho/pkg/builtins"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: pho <file.pho | file.phl>")
		os.Exit(2)
	}

	path := os.Args[1]
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".pho" && ext != ".phl" {
		fmt.Fprintf(os.Stderr, "pho: unsupported file extension %q (expected .pho or .phl)\n", ext)
		os.Exit(2)
	}

	// Imports (and the linter's sibling-file resolution) resolve relative
	// to the entry file's directory — that's how scripts in script/ reach
	// "std/...". Run from there; after the chdir the original path's
	// directory part is meaningless, so we address the file by base name.
	base := filepath.Base(path)
	if err := os.Chdir(filepath.Dir(path)); err != nil {
		fmt.Fprintf(os.Stderr, "pho: %v\n", err)
		os.Exit(1)
	}

	src, err := os.ReadFile(base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pho: %v\n", err)
		os.Exit(1)
	}

	// One session, one renderer, for every diagnostic the run produces —
	// static (linter) and runtime alike. Program output stays on stdout;
	// diagnostics go to stderr.
	session := diag.NewSession()
	session.Strict = os.Getenv("PHO_STRICT") != ""
	style := diag.StylePlain
	if diag.DetectColor(os.Stderr) {
		style = diag.StyleANSI
	}
	// After PHO_MAX_ERRORS full error blocks, the rest render compactly so
	// a cascade can't bury the run (default 10; <=0 means no cap).
	session.Report = diag.NewReporter(os.Stderr, style, envInt("PHO_MAX_ERRORS", 10))

	// Expose the Go-interop modules up front, before any evaluation —
	// including the parse-time annotation evaluation the linter will trigger
	// — so `goimport` resolves. Idempotent; runProgram no longer repeats it.
	// (The annotation macro library loads lazily during linting, resolved
	// relative to the file being analyzed — see pkg/lint walkAnnotations.)
	goop.Expose(goop.StdDependenciesModule())

	// Static analysis runs for both file kinds. Lint errors block before
	// any program is executed, mirroring how parse errors already abort —
	// there's no point running code the linter already proved broken.
	// Warnings render but don't block.
	parseErrors := lintFile(session, base, src)
	switch {
	case parseErrors > 0:
		// Parse errors keep their own exit code (2): the file never
		// became a valid program, distinct from a program that parsed
		// but failed a semantic check.
		finish(session, style, 2)
	case session.ErrorCount() > 0:
		finish(session, style, 1)
	}

	// Libraries are declaration-only; linting is their whole contract.
	if ext == ".phl" {
		finish(session, style, 0)
	}

	runProgram(session, style, base)
	if session.ErrorCount() > 0 {
		finish(session, style, 1)
	}
	finish(session, style, 0)
}

// finish writes the run's summary line (if any diagnostics were
// produced) and exits with code. The single exit point for every
// diagnostic-producing path, so the verdict is always the last line.
func finish(session *diag.Session, style diag.Style, code int) {
	diag.WriteSummary(os.Stderr, style, session.ErrorCount(), session.WarningCount())
	os.Exit(code)
}

// envInt reads a positive integer from the named environment variable,
// falling back to def when unset or unparseable.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// lintFile runs static analysis over src and emits every diagnostic
// through the session, so lint output renders identically to runtime
// errors (same excerpts, same color). The source File is synthesized
// purely so the reporter can quote the offending line — the linter
// doesn't produce call traces. The source text is handed to the reporter
// so it can quote the offending line. Returns the number of parse-error
// diagnostics, which the caller maps to the dedicated exit code 2.
func lintFile(session *diag.Session, path string, src []byte) (parseErrors int) {
	for _, d := range lint.AnalyzeFile(path, src) {
		if d.Code == diag.ErrParse {
			parseErrors++
		}
		session.Emit(diag.RuntimeError{Diagnostic: d, Source: string(src)})
	}
	return parseErrors
}

// runProgram loads and evaluates a .pho file as a one-file package.
// Side effects (I/O, top-level expressions) happen as the loader
// evaluates the file's top-level forms in order. Diagnostics report
// through the shared session. A parse failure exits 2 (the program
// never ran); any other load failure exits 1.
func runProgram(session *diag.Session, style diag.Style, path string) {
	// goop modules are already exposed in main() (before lint).
	modload.SetSession(session)

	if _, err := modload.LoadPackage(path); err != nil {
		var parseFailed *modload.ParseFailedError
		if errors.As(err, &parseFailed) {
			// The individual parse diagnostics already rendered.
			finish(session, style, 2)
		}
		fmt.Fprintf(os.Stderr, "pho: %v\n", err)
		finish(session, style, 1)
	}
}
