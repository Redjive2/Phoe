// Command pho-lint runs static analysis on Pho source files and
// prints diagnostics in GCC-style. Exits 0 on no errors, 1 if any
// error-severity diagnostic was emitted.
//
// Usage:
//
//	pho-lint <file-or-directory> [...]
//
// Each argument can be either a single .pho/.phl file or a package
// directory (any directory containing .pho/.phl files).
package main

import (
	"fmt"
	"os"

	"pho/pkg/lint"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pho-lint <file-or-directory> [...]")
		os.Exit(2)
	}

	exitCode := 0

	for _, arg := range os.Args[1:] {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pho-lint: %s: %v\n", arg, err)
			exitCode = 2
			continue
		}

		var diags []lint.Diagnostic
		if info.IsDir() {
			diags, err = lint.AnalyzePackage(arg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "pho-lint: %s: %v\n", arg, err)
				exitCode = 2
				continue
			}
		} else {
			src, readErr := os.ReadFile(arg)
			if readErr != nil {
				fmt.Fprintf(os.Stderr, "pho-lint: %s: %v\n", arg, readErr)
				exitCode = 2
				continue
			}
			diags = lint.AnalyzeFile(arg, src)
		}

		for _, d := range diags {
			fmt.Println(d.Format())
			if d.Severity == lint.SeverityError {
				exitCode = 1
			}
		}
	}

	os.Exit(exitCode)
}
