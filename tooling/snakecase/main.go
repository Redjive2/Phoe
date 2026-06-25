// Command snakecase migrates Pho source from the legacy syntax to the new
// syntax (Doc/PlanV1/Syntax.md). It handles the MECHANICAL, behavior-preserving
// transforms — the ones the tolerant Phases 1-6 already accept, so applying
// them keeps the suite green:
//
//	Nil / True / False   →  none / true / false
//	Self                 →  self
//	(const n v …)        →  (let n = v …)
//	(var n v …)          →  (let var n = v …)
//	{k v …}              →  [k -> v …]
//
// It does NOT do the casing reclassification (camelCase→snake_case, and
// distinguishing a Capitalized TYPE from a Capitalized VALUE) — that needs
// per-name semantic judgment and is handled separately. It also leaves the
// `(= x v)`→`(x = v)` and struct-init `.{f v}`→`.{f = v}` forms alone, since
// both still parse tolerantly.
//
// Like tooling/requote, it reuses the real lexer+parser (pkg/syntax) so the
// rewrite is format-preserving and refuses (returns an error) on any lex/parse
// error rather than risk corrupting a file. Modes: `-go` rewrites Pho embedded
// in Go string literals; `-n` is a dry run.
package main

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/syntax"
)

// offsetOf converts a 1-based (line, col) position into a byte offset in src.
// Spans are end-exclusive (EndCol = StartCol + length), so offsetOf(end) lands
// just past the last byte of a node — exactly where a slice should stop.
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

type edit struct {
	start, end int    // byte range to replace ([start,end))
	text       string // replacement (empty end==start means pure insertion)
}

func startOf(src string, n ast.PNode) int {
	s := n.GetSpan()
	return offsetOf(src, s.StartLine, s.StartCol)
}

func endOf(src string, n ast.PNode) int {
	s := n.GetSpan()
	return offsetOf(src, s.EndLine, s.EndCol)
}

// literalRepl is the set of bare-token renames (literals + the receiver name).
var literalRepl = map[string]string{
	"Nil": "none", "True": "true", "False": "false", "Self": "self",
}

// Transform rewrites one Pho source string and returns the new source plus the
// number of edits applied. It refuses on lex/parse errors.
func Transform(src string) (string, int, error) {
	toks, lexErrs := syntax.LexPos(src)
	if len(lexErrs) > 0 {
		return src, 0, fmt.Errorf("refusing to transform: %d lex error(s)", len(lexErrs))
	}
	tree, parseErrs := syntax.ParsePos(toks)
	if len(parseErrs) > 0 {
		return src, 0, fmt.Errorf("refusing to transform: %d parse error(s)", len(parseErrs))
	}

	var edits []edit

	// 1. Bare-token renames: literals and `Self`. A token whose text is exactly
	//    one of the renamed words is replaced wherever it appears.
	for _, t := range toks {
		if repl, ok := literalRepl[t.Value]; ok {
			start := offsetOf(src, t.Span.StartLine, t.Span.StartCol)
			edits = append(edits, edit{start, start + len(t.Value), repl})
		}
	}

	// 2. Structural renames via the AST: const/var → let, and `{…}` maps → `[…]`.
	for _, form := range tree {
		collectStructuralEdits(src, form, &edits)
	}

	if len(edits) == 0 {
		return src, 0, nil
	}
	return applyEdits(src, edits), len(edits), nil
}

// collectStructuralEdits walks a PNode subtree, appending edits for the
// const/var and map transforms.
func collectStructuralEdits(src string, n ast.PNode, edits *[]edit) {
	switch node := n.(type) {
	case *ast.PBranch:
		switch node.Open {
		case "(":
			if len(node.Children) >= 1 {
				if head, ok := node.Children[0].(*ast.PLeaf); ok {
					switch head.Value {
					case "const", "var":
						*edits = append(*edits, declEdits(src, node, head)...)
					}
				}
			}
		case "{":
			*edits = append(*edits, mapEdits(src, node)...)
		}
		for _, c := range node.Children {
			collectStructuralEdits(src, c, edits)
		}
	case *ast.PDot:
		collectStructuralEdits(src, node.LHS, edits)
		collectStructuralEdits(src, node.RHS, edits)
	case *ast.PMacroCall:
		collectStructuralEdits(src, node.Head, edits)
		for _, a := range node.Args {
			collectStructuralEdits(src, a, edits)
		}
	case *ast.PSigil:
		collectStructuralEdits(src, node.Inner, edits)
	}
}

// declEdits rewrites `(const n v …)` → `(let n = v …)` and `(var n v …)` →
// `(let var n = v …)`: it renames the head and inserts an `=` marker after each
// binding name (the odd-indexed children).
func declEdits(src string, br *ast.PBranch, head *ast.PLeaf) []edit {
	repl := "let"
	if head.Value == "var" {
		repl = "let var"
	}
	hs := offsetOf(src, head.Span.StartLine, head.Span.StartCol)
	out := []edit{{hs, hs + len(head.Value), repl}}
	for i := 1; i+1 < len(br.Children); i += 2 {
		nameEnd := endOf(src, br.Children[i])
		out = append(out, edit{nameEnd, nameEnd, " ="})
	}
	return out
}

// mapEdits rewrites a `{k v …}` map literal to `[k -> v …]`: swap the brackets
// and insert ` ->` after each key (the even-indexed children).
func mapEdits(src string, br *ast.PBranch) []edit {
	open := offsetOf(src, br.Span.StartLine, br.Span.StartCol)
	end := offsetOf(src, br.Span.EndLine, br.Span.EndCol)
	out := []edit{
		{open, open + 1, "["},
		{end - 1, end, "]"},
	}
	for i := 0; i+1 < len(br.Children); i += 2 {
		keyEnd := endOf(src, br.Children[i])
		out = append(out, edit{keyEnd, keyEnd, " ->"})
	}
	return out
}

// applyEdits applies non-overlapping edits to src. Edits are sorted by start
// (insertions at the same offset keep insertion order) and applied back-to-front
// so earlier offsets stay valid.
func applyEdits(src string, edits []edit) string {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	out := src
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		out = out[:e.start] + e.text + out[e.end:]
	}
	return out
}

// ---- Go-embedded migration (mirrors tooling/requote) ----

func looksLikePhoCode(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	if t == "" {
		return false
	}
	return t[0] == '(' || t[0] == '[' || t[0] == '{' || strings.HasPrefix(t, "--")
}

// MigrateGoFile rewrites Pho embedded in Go string literals. When renames is
// nil it does the mechanical Transform only; otherwise it also recases each
// snippet with the package-wide map (used by the full migration).
func MigrateGoFile(src string, renames map[string]string, types map[string]bool) (string, int, error) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), nil, 0)

	type lit struct {
		off  int
		text string
	}
	var lits []lit
	for {
		pos, tok, litText := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.STRING {
			lits = append(lits, lit{off: file.Offset(pos), text: litText})
		}
	}

	// Collect type names FILE-WIDE first: a test often builds one Pho program by
	// concatenating several Go string fragments, so a type declared in one
	// fragment is used (e.g. constructed, or as a sig param) in another. Without
	// this, the using fragment wouldn't know the name is a type and would snake it.
	fileTypes := map[string]bool{}
	for k := range types {
		fileTypes[k] = true
	}
	if renames != nil {
		for _, l := range lits {
			val, err := strconv.Unquote(l.text)
			if err != nil || !looksLikePhoCode(val) {
				continue
			}
			toks, lexErrs := syntax.LexPos(val)
			if len(lexErrs) > 0 {
				continue
			}
			if tree, parseErrs := syntax.ParsePos(toks); len(parseErrs) == 0 {
				for k := range collectTypeNames(tree) {
					fileTypes[k] = true
				}
			}
		}
	}

	out := src
	changed := 0
	for i := len(lits) - 1; i >= 0; i-- {
		l := lits[i]
		newLit, ok := migrateGoLiteral(l.text, renames, fileTypes)
		if !ok || newLit == l.text {
			continue
		}
		out = out[:l.off] + newLit + out[l.off+len(l.text):]
		changed++
	}
	return out, changed, nil
}

func migrateGoLiteral(goLit string, renames map[string]string, types map[string]bool) (result string, changed bool) {
	// Recover-and-skip: a single edge-case snippet that trips the rewriter is
	// left unchanged (still tolerantly accepted) rather than aborting the whole
	// migration. The skip is logged so it can be reviewed.
	defer func() {
		if r := recover(); r != nil {
			prefix := goLit
			if len(prefix) > 50 {
				prefix = prefix[:50]
			}
			fmt.Fprintf(os.Stderr, "  skip (panic %v): %s…\n", r, prefix)
			result, changed = goLit, false
		}
	}()
	if len(goLit) < 2 {
		return goLit, false
	}
	val, err := strconv.Unquote(goLit)
	if err != nil || !looksLikePhoCode(val) {
		return goLit, false
	}
	migrated, n, err := Transform(val)
	if err != nil {
		return goLit, false
	}
	// Casing pass: an embedded snippet is a PROGRAM. Augment the global type
	// set with the snippet's own struct/type decls so a local type still
	// Title_Snakes, and collect its goimports so Go-module members are spared.
	if renames != nil {
		toks, lexErrs := syntax.LexPos(migrated)
		if len(lexErrs) == 0 {
			if tree, parseErrs := syntax.ParsePos(toks); len(parseErrs) == 0 {
				snipTypes := map[string]bool{}
				for k := range types {
					snipTypes[k] = true
				}
				for k := range collectTypeNames(tree) {
					snipTypes[k] = true
				}
				if recased, nc, err := Recase(migrated, renames, snipTypes, collectGoimports(tree)); err == nil {
					migrated = recased
					n += nc
				}
			}
		}
	}
	if n == 0 {
		return goLit, false
	}
	if goLit[0] == '`' && !strings.Contains(migrated, "`") {
		return "`" + migrated + "`", true
	}
	return strconv.Quote(migrated), true
}

// runRecase does the full two-pass migration (mechanical Transform, then casing
// Recase) over a set of files that form one package: it parses every file to
// build the package-wide rename map, then rewrites each. The map is built from
// the ORIGINAL source (which still has const/var) but the names it keys on
// survive the mechanical pass unchanged, so applying it to the transformed
// output is sound.
func runRecase(paths []string, dry bool) {
	type fileInfo struct {
		path      string
		src       string
		goimports map[string]bool
	}
	var infos []fileInfo
	var files []fileTree
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			continue
		}
		src := string(data)
		toks, lexErrs := syntax.LexPos(src)
		if len(lexErrs) > 0 {
			fmt.Fprintf(os.Stderr, "%s: %d lex error(s); skipping\n", filepath.Base(path), len(lexErrs))
			continue
		}
		tree, parseErrs := syntax.ParsePos(toks)
		if len(parseErrs) > 0 {
			fmt.Fprintf(os.Stderr, "%s: %d parse error(s); skipping\n", filepath.Base(path), len(parseErrs))
			continue
		}
		infos = append(infos, fileInfo{path, src, collectGoimports(tree)})
		files = append(files, fileTree{tree, strings.HasSuffix(path, ".phl")})
	}
	renames, types := buildGlobalMaps(files)

	total := 0
	for _, f := range infos {
		mech, nm, err := Transform(f.src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(f.path), err)
			continue
		}
		out, nc, err := Recase(mech, renames, types, f.goimports)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(f.path), err)
			continue
		}
		n := nm + nc
		if n == 0 {
			continue
		}
		total += n
		if dry {
			fmt.Printf("%s: would apply %d edit(s) (%d mechanical, %d casing)\n", filepath.Base(f.path), n, nm, nc)
			continue
		}
		if err := os.WriteFile(f.path, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f.path, err)
			continue
		}
		fmt.Printf("%s: applied %d edit(s) (%d mechanical, %d casing)\n", filepath.Base(f.path), n, nm, nc)
	}
	fmt.Printf("total: %d edit(s)\n", total)
}

// runMigrate is the full atomic cutover: it builds ONE package-wide rename map
// + type set from every Pho source, then recases all `.phl`/`.pho` files AND the
// Pho embedded in `.go` files with that shared map. A name renames identically
// everywhere it appears.
func runMigrate(paths []string, dry bool) {
	var phoPaths, goPaths []string
	for _, p := range paths {
		switch {
		case strings.HasSuffix(p, ".phl"), strings.HasSuffix(p, ".pho"):
			phoPaths = append(phoPaths, p)
		case strings.HasSuffix(p, ".go"):
			goPaths = append(goPaths, p)
		}
	}

	type phoInfo struct {
		path, src string
		goimports map[string]bool
	}
	var phos []phoInfo
	var files []fileTree
	for _, p := range phoPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
			continue
		}
		src := string(data)
		toks, lexErrs := syntax.LexPos(src)
		if len(lexErrs) > 0 {
			fmt.Fprintf(os.Stderr, "%s: %d lex error(s); skipping\n", filepath.Base(p), len(lexErrs))
			continue
		}
		tree, parseErrs := syntax.ParsePos(toks)
		if len(parseErrs) > 0 {
			fmt.Fprintf(os.Stderr, "%s: %d parse error(s); skipping\n", filepath.Base(p), len(parseErrs))
			continue
		}
		phos = append(phos, phoInfo{p, src, collectGoimports(tree)})
		files = append(files, fileTree{tree, strings.HasSuffix(p, ".phl")})
	}
	renames, types := buildGlobalMaps(files)

	total := 0
	report := func(path string, n int) {
		if dry {
			fmt.Printf("%s: would apply %d edit(s)\n", filepath.Base(path), n)
		} else {
			fmt.Printf("%s: applied %d edit(s)\n", filepath.Base(path), n)
		}
	}
	for _, ph := range phos {
		mech, nm, err := Transform(ph.src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(ph.path), err)
			continue
		}
		out, nc, err := Recase(mech, renames, types, ph.goimports)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(ph.path), err)
			continue
		}
		if nm+nc == 0 {
			continue
		}
		total += nm + nc
		if !dry {
			if err := os.WriteFile(ph.path, []byte(out), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", ph.path, err)
				continue
			}
		}
		report(ph.path, nm+nc)
	}
	for _, gp := range goPaths {
		data, err := os.ReadFile(gp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", gp, err)
			continue
		}
		out, n, err := MigrateGoFile(string(data), renames, types)
		if err != nil || n == 0 {
			continue
		}
		total += n
		if !dry {
			if err := os.WriteFile(gp, []byte(out), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", gp, err)
				continue
			}
		}
		report(gp, n)
	}
	fmt.Printf("total: %d edit(s)\n", total)
}

func main() {
	args := os.Args[1:]
	goMode := false
	recaseMode := false
	migrateMode := false
	dry := false
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-go":
			goMode = true
		case "-recase":
			recaseMode = true
		case "-migrate":
			migrateMode = true
		case "-n":
			dry = true
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", args[0])
			os.Exit(2)
		}
		args = args[1:]
	}

	if migrateMode {
		runMigrate(args, dry)
		return
	}
	if recaseMode {
		runRecase(args, dry)
		return
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
			out, n, err = MigrateGoFile(string(data), nil, nil)
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
			fmt.Printf("%s: would apply %d edit(s)\n", filepath.Base(path), n)
			continue
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			continue
		}
		fmt.Printf("%s: applied %d edit(s)\n", filepath.Base(path), n)
	}
	fmt.Printf("total: %d edit(s)\n", total)
}
