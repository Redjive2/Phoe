// Package diag owns Pho's diagnostic model — Severity, codes, Diagnostic,
// RuntimeError, and the run-wide Session — and renders diagnostics as
// colored, structured terminal output: a severity header, the source
// location, an excerpt of the offending code with a caret underline,
// optional notes, and a Pho call stack. It is a leaf package (it imports
// only pkg/span): core depends on diag to raise errors, never the
// reverse, and package main injects NewReporter into Session.Report.
package diag

import "os"

// Style holds the raw SGR escape used for each emphasis level. Only the
// 16-color base palette plus bold/dim are used so user terminal themes
// stay readable on both dark and light backgrounds. StylePlain's empty
// strings make plain output byte-identical to colored output modulo
// escape sequences — golden tests render with it.
type Style struct {
	Bold, Dim, Red, Yellow, Blue, Cyan, Reset string
}

var StylePlain Style

var StyleANSI = Style{
	Bold:   "\x1b[1m",
	Dim:    "\x1b[2m",
	Red:    "\x1b[1;31m", // bold red: error headers and carets
	Yellow: "\x1b[1;33m", // bold yellow: warning headers and carets
	Blue:   "\x1b[1;34m", // bold blue: gutter bars, line numbers, arrows
	Cyan:   "\x1b[1;36m", // bold cyan: help keyword
	Reset:  "\x1b[0m",
}

func (st Style) paint(sgr, s string) string {
	if sgr == "" || s == "" {
		return s
	}
	return sgr + s + st.Reset
}

// DetectColor decides whether to emit ANSI colors on f. Precedence:
// FORCE_COLOR / CLICOLOR_FORCE (non-empty and not "0", per the chalk and
// CLICOLOR conventions) force color on even when piped; NO_COLOR
// (present at all, per no-color.org) forces it off; TERM=dumb forces it
// off; otherwise color is on iff f is a terminal.
func DetectColor(f *os.File) bool {
	force := func(name string) bool {
		v := os.Getenv(name)
		return v != "" && v != "0"
	}
	if force("FORCE_COLOR") || force("CLICOLOR_FORCE") {
		return true
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
