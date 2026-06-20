package main

import (
	"flag"
	"fmt"
	"os"
)

// main is the CLI driver: `desigil [-w] [-go] <file>...`. Without -w it is a
// dry run, reporting which files would change. With -w it rewrites in place.
// With -go it migrates the Pho embedded in Go string literals (test
// fixtures) instead of treating the file as Pho source. Files with parse
// errors are reported and skipped, never partially written.
func main() {
	write := flag.Bool("w", false, "rewrite files in place (default: dry run)")
	goMode := flag.Bool("go", false, "migrate Pho embedded in Go string literals")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: desigil [-w] [-go] <file>...")
		os.Exit(2)
	}

	transform := Transform
	unit := "sigil"
	if *goMode {
		transform = MigrateGoFile
		unit = "literal"
	}

	var changed, failed int
	for _, path := range flag.Args() {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed++
			continue
		}

		out, n, err := transform(string(src))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed++
			continue
		}
		if n == 0 {
			continue // already in the new syntax
		}
		changed++

		if *write {
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				failed++
				continue
			}
			fmt.Printf("%s: migrated %d %s(s)\n", path, n, unit)
		} else {
			fmt.Printf("%s: would migrate %d %s(s)\n", path, n, unit)
		}
	}

	if !*write && changed > 0 {
		fmt.Printf("\n%d file(s) would change. Re-run with -w to apply.\n", changed)
	}
	if failed > 0 {
		os.Exit(1)
	}
}
