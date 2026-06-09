// Pho binary entry point. Takes a single source-file argument and
// either runs it (.pho) or lints it (.phl).
//
// The blank import of pkg/builtins triggers the init() that wires
// builtins.NewEnv into modload.EnvFactory — without it, the package
// loader has no way to construct envs.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pho/pkg/core"
	"pho/pkg/goop"
	"pho/pkg/lint"
	"pho/pkg/modload"

	_ "pho/pkg/builtins"
)

type Module struct {}

func (Module) DoStuff(s string, f float64, m map[core.Tval]core.Tval) []core.Tval {
	return []core.Tval{
		core.TvBool(true),
	}
} 

func main() {
	goop.Expose(&goop.PhoModule{
		Name: "Module",
		Children: map[string]*goop.PhoModule{},
		Data: Module{},
	})

	
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: pho <file.pho | file.phl>")
		os.Exit(2)
	}

	path := os.Args[1]
	ext := strings.ToLower(filepath.Ext(path))

	oldWd, _ := os.Getwd()
	os.Chdir(filepath.Dir(path))

	switch ext {
	case ".pho":
		runProgram(filepath.Base(path))
	case ".phl":
		lintLibrary(path)
	default:
		fmt.Fprintf(os.Stderr, "pho: unsupported file extension %q (expected .pho or .phl)\n", ext)
		os.Chdir(oldWd)
		os.Exit(2)
	}
}

// runProgram loads and evaluates a .pho file as a one-file package.
// Side effects (I/O, top-level expressions) happen as the loader
// evaluates the file's top-level forms in order.
func runProgram(path string) {
	goop.Expose(goop.StdDependenciesModule())
	if _, err := modload.LoadPackage(path); err != nil {
		fmt.Fprintf(os.Stderr, "pho: %v\n", err)
		os.Exit(1)
	}
}

// lintLibrary runs static analysis on a .phl file and prints any
// diagnostics. Library files are declaration-only by design — they
// can't have side effects to "run" — so the CLI's contract for them
// is to surface problems and stop.
func lintLibrary(path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pho: %v\n", err)
		os.Exit(1)
	}
	diags := lint.AnalyzeFile(path, src)
	exitCode := 0
	for _, d := range diags {
		fmt.Println(d.Format())
		if d.Severity == lint.SeverityError {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
