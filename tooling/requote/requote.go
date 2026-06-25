// Command requote migrates Pho string literals from the legacy `"..."`
// delimiter to the new `'...'` delimiter (Phase 3 of the dequote/string
// migration). It rewrites `.pho`/`.phl` source directly and the Pho code
// embedded in Go test fixtures (`-go`).
//
// The rewrite is format-preserving and behavior-preserving: it reuses the
// real lexer (pkg/syntax) to locate string tokens, so comments, char
// literals, and interpolation are never mistaken for strings, and it
// re-escapes the body so the value is identical (`\"` becomes a bare `"`,
// which needs no escape inside `'...'`; a literal `'` becomes `\'`).
package main

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// requoteBody converts the BODY of a `"`-delimited string to the body of a
// `'`-delimited one. An escaped `\"` becomes a bare `"` (no longer special);
// a bare `'` becomes `\'` (now the delimiter). Every other escape and byte
// passes through untouched, so `\n`, `\\`, `\'`, `%(...)`, etc. are preserved.
func requoteBody(body string) string {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			if body[i+1] == '"' {
				b.WriteByte('"') // \" -> "
			} else {
				b.WriteByte('\\')
				b.WriteByte(body[i+1])
			}
			i++
			continue
		}
		if c == '\'' {
			b.WriteString("\\'") // ' -> \'
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// offsetOf converts a 1-based (line, col) position into a byte offset in src.
func offsetOf(src string, line, col int) int {
	curLine, off := 1, 0
	for off < len(src) && curLine < line {
		if src[off] == '\n' {
			curLine++
		}
		off++
	}
	return off + (col - 1)
}

type replacement struct {
	start, end int
	text       string
}

// Transform rewrites every `"`-delimited string token in a Pho source string
// to the `'`-delimited form, returning the new source and the number of
// strings migrated. It refuses (returns an error) on lex errors so a partial
// or malformed file is never silently corrupted.
func Transform(src string) (string, int, error) {
	tokens, errs := syntax.LexPos(src)
	if len(errs) > 0 {
		return src, 0, fmt.Errorf("refusing to transform: %d lex error(s)", len(errs))
	}
	var repls []replacement
	for _, t := range tokens {
		v := t.Value
		if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
			continue
		}
		start := offsetOf(src, t.Span.StartLine, t.Span.StartCol)
		end := start + len(v)
		if end > len(src) || src[start:end] != v {
			return src, 0, fmt.Errorf("span/source mismatch for token %q at %d:%d", v, t.Span.StartLine, t.Span.StartCol)
		}
		oldBody := v[1 : len(v)-1]
		newBody := requoteBody(oldBody)
		// Invariant: the unescaped runtime value must be identical. If not,
		// refuse rather than corrupt a string.
		if core.UnescapeStringLit(oldBody) != core.UnescapeStringLit(newBody) {
			return src, 0, fmt.Errorf("value changed requoting %q -> '%s'", v, newBody)
		}
		repls = append(repls, replacement{start, end, "'" + newBody + "'"})
	}
	// Apply back-to-front so earlier offsets stay valid.
	out := src
	for i := len(repls) - 1; i >= 0; i-- {
		r := repls[i]
		out = out[:r.start] + r.text + out[r.end:]
	}
	return out, len(repls), nil
}

// looksLikePhoCode reports whether a Go string literal's value is plausibly
// Pho CODE (rather than a prose error message or path). Only such strings are
// migrated, so a diagnostic message containing `"` is never corrupted.
func looksLikePhoCode(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	if t == "" {
		return false
	}
	// A form, a literal, or a Pho comment / `--@` annotation line. (Transform
	// refuses on lex errors and preserves every string value, so a non-Pho
	// string slipping through is left unchanged rather than corrupted.)
	if t[0] == '(' || t[0] == '[' || t[0] == '{' || strings.HasPrefix(t, "--") {
		return true
	}
	// A value that is exactly one Pho string-literal token — e.g. a backtick
	// raw Go string `"%Number"` holding a bare Pho string input. Go prose
	// strings reach here with their leading `"` already stripped, so they
	// don't start with `"` and won't match.
	toks, errs := syntax.LexPos(s)
	return len(errs) == 0 && len(toks) == 1 && len(toks[0].Value) >= 2 && toks[0].Value[0] == '"'
}

// MigrateGoFile rewrites the Pho `"..."` strings embedded inside Go string
// literals (backtick or interpreted) within a Go source file, leaving prose
// strings untouched. Returns the new source and the count of Go literals
// changed.
func MigrateGoFile(src string) (string, int, error) {
	var fset = token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), nil, 0)

	type lit struct {
		off  int // byte offset of the literal in src
		text string
	}
	var lits []lit
	for {
		pos, tok, litText := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING {
			continue
		}
		lits = append(lits, lit{off: file.Offset(pos), text: litText})
	}

	out := src
	changed := 0
	// Back-to-front so offsets stay valid.
	for i := len(lits) - 1; i >= 0; i-- {
		l := lits[i]
		newLit, ok := requoteGoLiteral(l.text)
		if !ok || newLit == l.text {
			continue
		}
		out = out[:l.off] + newLit + out[l.off+len(l.text):]
		changed++
	}
	return out, changed, nil
}

// requoteGoLiteral takes a Go string literal (including its surrounding ` or "
// quotes), and if its value is Pho code, returns the literal with the embedded
// Pho strings requoted. ok=false means leave it untouched.
func requoteGoLiteral(goLit string) (string, bool) {
	if len(goLit) < 2 {
		return goLit, false
	}
	val, err := strconv.Unquote(goLit)
	if err != nil {
		return goLit, false
	}
	if !looksLikePhoCode(val) {
		return goLit, false
	}
	migrated, n, err := Transform(val)
	if err != nil || n == 0 {
		return goLit, false
	}
	// Re-encode. Prefer a raw (backtick) literal when the original was raw and
	// the migrated value has no backtick; else an interpreted literal.
	if goLit[0] == '`' && !strings.Contains(migrated, "`") {
		return "`" + migrated + "`", true
	}
	return strconv.Quote(migrated), true
}

func main() {
	goMode := false
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "-go" {
		goMode = true
		args = args[1:]
	}
	dry := false
	if len(args) > 0 && args[0] == "-n" {
		dry = true
		args = args[1:]
	}
	total := 0
	for _, path := range args {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			continue
		}
		var out string
		var n int
		if goMode {
			out, n, err = MigrateGoFile(string(data))
		} else {
			out, n, err = Transform(string(data))
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			continue
		}
		if n == 0 {
			continue
		}
		total += n
		if dry {
			fmt.Printf("%s: would migrate %d string(s)\n", filepath.Base(path), n)
			continue
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			continue
		}
		fmt.Printf("%s: migrated %d string(s)\n", filepath.Base(path), n)
	}
	fmt.Printf("total: %d string(s)\n", total)
}
