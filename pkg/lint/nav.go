package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pho/pkg/annot"
	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
	"pho/pkg/syntax"
)

// Navigation: go-to-definition, hover, document symbols, references.
//
// Definition/hover/references all work the same way: run the regular
// diagnostic walk with the walker's resolution hooks installed and
// record every resolved reference, member access, and declaration as a
// `hit`. That reuses the exact scoping and shape inference the
// diagnostics use, so navigation never disagrees with what the
// squiggles say.

// DefSite is a definition location: a file plus the span of the
// defining name within it.
type DefSite struct {
	File string
	Span span.Span
}

// hit is one resolved site recorded during a hooked walk.
type hit struct {
	Span span.Span // the reference site in the analyzed file

	// Exactly one flavor is set:
	Def        Definition  // identifier / export / declaration
	SI         *structInfo // struct member access...
	Member     string      // ...member name
	MemberKind DefKind     // ...DefField or DefMethod
	Note       string      // ready-rendered hover for a synthetic target (a built-in object-model member); no jumpable definition

	IsDecl bool // the site IS the declaration (quoted decl name)
}

// collectHits runs a full hooked analysis of src and returns every
// resolved site, in walk order.
func collectHits(path string, src []byte) []hit {
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	var hits []hit
	w := newWalker(path)
	w.onLeafResolve = func(span span.Span, def Definition) {
		hits = append(hits, hit{Span: span, Def: def})
	}
	w.onExportResolve = func(span span.Span, def Definition) {
		hits = append(hits, hit{Span: span, Def: def})
	}
	w.onMemberResolve = func(span span.Span, si *structInfo, member string, kind DefKind) {
		hits = append(hits, hit{Span: span, SI: si, Member: member, MemberKind: kind})
	}
	w.onBuiltinMember = func(span span.Span, hoverMD string) {
		hits = append(hits, hit{Span: span, Note: hoverMD})
	}
	w.onDefine = func(span span.Span, def Definition) {
		hits = append(hits, hit{Span: span, Def: def, IsDecl: true})
	}
	w.walkFile(tree, PackageScope(path))
	return hits
}

// hitAt returns the innermost hit whose span contains the cursor.
// Later hits win ties — the walk visits declarations before uses, and
// a more specific resolution (member) is recorded after the generic
// one (receiver leaf).
func hitAt(hits []hit, line, col int) (hit, bool) {
	var found hit
	ok := false
	for _, h := range hits {
		if spanContains(h.Span, line, col) {
			found = h
			ok = true
		}
	}
	return found, ok
}

// defSiteOf turns a hit into a jumpable location. Builtins (zero
// spans) aren't jumpable.
func defSiteOf(h hit, path string) (DefSite, bool) {
	if h.SI != nil {
		if span, ok := h.SI.Fields[h.Member]; ok && h.MemberKind == DefField {
			return DefSite{File: orPath(h.SI.File, path), Span: span}, true
		}
		if span, ok := h.SI.Methods[h.Member]; ok {
			file := h.SI.MethodFiles[h.Member]
			if file == "" {
				file = h.SI.File
			}
			return DefSite{File: orPath(file, path), Span: span}, true
		}
		return DefSite{}, false
	}
	if h.Def.Kind == DefBuiltin || (h.Def.Span == span.Span{}) {
		return DefSite{}, false
	}
	return DefSite{File: orPath(h.Def.File, path), Span: h.Def.Span}, true
}

func orPath(file, path string) string {
	if file == "" {
		return path
	}
	return file
}

// DefinitionAt resolves the symbol at (line, col) to its definition
// site. Lines and columns are 1-based.
func DefinitionAt(path string, src []byte, line, col int) (site DefSite, found bool) {
	defer func() {
		if r := recover(); r != nil {
			recoverEntry("DefinitionAt", r)
			site, found = DefSite{}, false
		}
	}()
	h, ok := hitAt(collectHits(path, src), line, col)
	if !ok {
		return DefSite{}, false
	}
	return defSiteOf(h, path)
}

// ----------------------------------------------------------------------
// Hover
// ----------------------------------------------------------------------

// HoverAt renders a markdown summary of the symbol at (line, col):
// its kind, signature (reconstructed from the declaring source), any
// `--` doc comment lines directly above the declaration, and the
// inferred shape for variables. The returned span is the hovered
// token, so the editor can highlight it.
func HoverAt(path string, src []byte, line, col int) (md string, sp span.Span, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			recoverEntry("HoverAt", r)
			md, sp, ok = "", span.Span{}, false
		}
	}()
	h, ok := hitAt(collectHits(path, src), line, col)
	if !ok {
		return "", span.Span{}, false
	}

	// A built-in object-model member: hover markdown is pre-rendered (no
	// workspace definition site).
	if h.Note != "" {
		return h.Note, h.Span, true
	}

	if h.SI != nil {
		return hoverMember(h, path), h.Span, true
	}

	def := h.Def
	switch def.Kind {
	case DefBuiltin:
		return fmt.Sprintf("```pho\n%s\n```\nbuiltin", def.Name), h.Span, true
	case DefImport:
		return fmt.Sprintf("```pho\n(import \"%s\")\n```\nimported package", def.Path), h.Span, true
	}

	file := orPath(def.File, path)
	header, doc := declHeader(file, path, src, def.Span)
	if header == "" {
		// Fallback when the declaration form can't be located (e.g. a
		// mid-edit parse). Strip any internal "Owner." qualifier —
		// fields and methods are tracked under receiver-qualified keys
		// ("File.id") that must never surface to the user.
		name := def.Name
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		header = name
	}
	text := fmt.Sprintf("```pho\n%s\n```\n%s", header, def.Kind)
	if def.Shape.Kind != ShapeUnknown {
		text += " — " + shapeLabel(def.Shape)
	}
	if doc != "" {
		text += "\n\n" + doc
	}
	// Parse-time annotation metadata, for a declaration in this file. (A
	// cross-file declaration's annotations live in its own file's source,
	// which we don't re-read here.)
	if def.File == "" || def.File == path {
		if ann := annotationHover(path, src, def.Span); ann != "" {
			text += "\n\n" + ann
		}
	}
	return text, h.Span, true
}

// annotationHover renders the evaluated parse-time annotations of the
// top-level declaration whose name is at defSpan, or "" if it carries none.
// src is the declaration's own file. Evaluation reuses the process-wide
// evaluator's memo (annotations already ran during linting), so this is
// cheap.
func annotationHover(path string, src []byte, defSpan span.Span) string {
	annot.EnsureDefault(resolveImportPath(path, "std/annot"))
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)
	br := declFormContaining(tree, defSpan)
	if br == nil || len(br.Annotations) == 0 {
		return ""
	}
	var b strings.Builder
	for _, res := range annot.Default().EvaluateBranch(br) {
		for _, e := range res.Entries {
			if b.Len() > 0 {
				b.WriteString("  \n")
			}
			fmt.Fprintf(&b, "`%s` = `%s`", e.Key, core.Stringify(e.Value))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "**annotations**  \n" + b.String()
}

func shapeLabel(sh Shape) string {
	if sh.Kind == ShapeInstance && sh.Owner != "" {
		return "instance of " + sh.Owner
	}
	return sh.Kind.String()
}

// hoverMember renders a struct field or method.
func hoverMember(h hit, path string) string {
	si := h.SI
	if h.MemberKind == DefMethod {
		file := si.MethodFiles[h.Member]
		if file == "" {
			file = si.File
		}
		header, doc := declHeader(orPath(file, path), path, nil, si.Methods[h.Member])
		if header == "" {
			header = fmt.Sprintf("(method %s.%s ...)", si.Name, h.Member)
		}
		text := fmt.Sprintf("```pho\n%s\n```\nmethod of struct %s", header, si.Name)
		if doc != "" {
			text += "\n\n" + doc
		}
		return text
	}
	return fmt.Sprintf("```pho\n%s\n```\nfield of struct %s", h.Member, si.Name)
}

// declHeader finds the declaration form containing nameSpan in `file`
// and returns a one-line rendering of its header plus any doc comment
// above it. `src` is the in-memory text when file == path (unsaved
// edits); other files are read from disk.
func declHeader(file, path string, src []byte, nameSpan span.Span) (header, doc string) {
	if nameSpan == (span.Span{}) {
		return "", ""
	}
	var text []byte
	if file == path && src != nil {
		text = src
	} else {
		read, err := readSource(file)
		if err != nil {
			return "", ""
		}
		text = read
	}

	tokens, _ := syntax.LexPos(string(text))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)
	form := declFormContaining(tree, nameSpan)
	if form == nil {
		return "", ""
	}
	return capHeader(renderDeclHeader(form)), docCommentAbove(string(text), form.GetSpan().StartLine)
}

// capHeader bounds a hover header's length. A malformed mid-edit form
// can make the parser's recovery swallow following code into one
// branch; without a cap, reconstructing it would dump that whole blob
// into the hover. Counts runes so a cut never splits a multibyte
// character.
func capHeader(s string) string {
	const max = 160
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + " …"
}

// declFormContaining finds the innermost fun/method/struct/var/const
// list whose span contains the given name span.
func declFormContaining(tree []ast.PNode, span span.Span) *ast.PBranch {
	var found *ast.PBranch
	var visit func(n ast.PNode)
	visit = func(n ast.PNode) {
		switch node := n.(type) {
		case *ast.PBranch:
			if spanCovers(node.Span, span) {
				switch headIdent(node) {
				case "fun", "macro", "method", "struct", "property", "var", "const", "let":
					found = node
				}
			}
			for _, c := range node.Children {
				visit(c)
			}
		case *ast.PSigil:
			visit(node.Inner)
		case *ast.PDot:
			visit(node.LHS)
			visit(node.RHS)
		case *ast.PMacroCall:
			visit(node.Head)
			for _, a := range node.Args {
				visit(a)
			}
		}
	}
	for _, form := range tree {
		visit(form)
	}
	return found
}

func spanCovers(outer, inner span.Span) bool {
	return spanContains(outer, inner.StartLine, inner.StartCol)
}

// renderDeclHeader reconstructs the readable head of a declaration:
// everything except function bodies.
func renderDeclHeader(br *ast.PBranch) string {
	switch headIdent(br) {
	case "fun":
		switch len(br.Children) {
		case 4:
			return fmt.Sprintf("(fun %s %s ...)", pnodeText(br.Children[1]), pnodeText(br.Children[2]))
		case 3:
			return fmt.Sprintf("(fun %s ...)", pnodeText(br.Children[1]))
		}
	case "macro":
		// (macro ~name (params) body) — `~`@1, name@2, params@3.
		if len(br.Children) >= 4 {
			return fmt.Sprintf("(macro ~%s %s ...)", pnodeText(br.Children[2]), pnodeText(br.Children[3]))
		}
	case "method":
		if len(br.Children) >= 3 {
			// child 1 is the `Receiver[.Name]` pattern, child 2 the arg list.
			return fmt.Sprintf("(method %s %s ...)",
				pnodeText(br.Children[1]), pnodeText(br.Children[2]))
		}
	case "property":
		// (property <Receiver.>Name get getter [set setter]) — show the name
		// and which accessors it has.
		if len(br.Children) >= 2 {
			accessors := "get"
			if len(br.Children) >= 6 {
				accessors = "get set"
			}
			return fmt.Sprintf("(property %s %s ...)", pnodeText(br.Children[1]), accessors)
		}
	case "struct":
		return renderStructHeader(br)
	case "var", "const", "let":
		return pnodeText(br)
	}
	return pnodeText(br)
}

// renderStructHeader rebuilds `(struct Name f1 f2 …)` from the
// declaration's actual fields rather than reconstructing the whole
// branch. Struct fields are bare identifiers, so a parenthesized form
// appearing among them is code the parser's recovery swallowed during a
// mid-edit unbalance — filtering to identifier leaves drops it, keeping
// the hover bounded and faithful to what the struct really declares.
// Falls back to the raw reconstruction only when the name isn't a plain
// quoted identifier (nothing better to show).
func renderStructHeader(br *ast.PBranch) string {
	d, ok := declOf(br)
	if !ok || d.Name == "" {
		return pnodeText(br)
	}
	if len(d.Fields) == 0 {
		return fmt.Sprintf("(struct %s)", d.Name)
	}
	typed := false
	parts := make([]string, 0, len(d.Fields))
	for _, f := range d.Fields {
		if f.Type != nil {
			typed = true
			parts = append(parts, f.Name+" "+pnodeText(f.Type))
		} else {
			parts = append(parts, f.Name)
		}
	}
	// Render the typed-field form `(struct Name.{ F T … })` when any field
	// carries a type, else the bare `(struct Name f …)`.
	if typed {
		return fmt.Sprintf("(struct %s.{ %s })", d.Name, strings.Join(parts, " "))
	}
	return fmt.Sprintf("(struct %s %s)", d.Name, strings.Join(parts, " "))
}

// pnodeText reconstructs approximate source text for a node. Used for
// hover headers only — whitespace is normalized, content is verbatim.
func pnodeText(n ast.PNode) string {
	switch node := n.(type) {
	case *ast.PLeaf:
		return node.Value
	case *ast.PBranch:
		parts := make([]string, 0, len(node.Children))
		for _, c := range node.Children {
			parts = append(parts, pnodeText(c))
		}
		return node.Open + strings.Join(parts, " ") + node.Close
	case *ast.PSigil:
		return node.Sigil + pnodeText(node.Inner)
	case *ast.PDot:
		return pnodeText(node.LHS) + "." + pnodeText(node.RHS)
	case *ast.PMacroCall:
		parts := make([]string, 0, len(node.Args))
		for _, a := range node.Args {
			parts = append(parts, pnodeText(a))
		}
		out := "(~" + pnodeText(node.Head)
		if len(parts) > 0 {
			out += " " + strings.Join(parts, " ")
		}
		return out + ")"
	}
	return ""
}

// docCommentAbove collects the contiguous `--` comment block directly
// above declLine (1-based), with the leading dashes stripped.
func docCommentAbove(text string, declLine int) string {
	lines := strings.Split(text, "\n")
	if declLine-2 < 0 || declLine-2 >= len(lines) {
		return ""
	}
	var doc []string
	for i := declLine - 2; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "--") {
			break
		}
		// A `--@ ` line is a parse-time annotation, not a doc comment — its
		// metadata is surfaced separately (annotationHover). Skip it but keep
		// scanning, so a doc comment above an annotation still resolves.
		if strings.HasPrefix(trimmed, "--@ ") || strings.HasPrefix(trimmed, "--@\t") {
			continue
		}
		doc = append([]string{strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))}, doc...)
	}
	return strings.Join(doc, "\n")
}

// ----------------------------------------------------------------------
// References
// ----------------------------------------------------------------------

// ReferencesAt returns every reference to the symbol at (line, col),
// searched along the module system's reachability:
//
//	local bindings (params, body vars, import aliases) — current file
//	top-level decls in a .phl — plus every sibling .phl in its package
//	exported symbols (capitalized, incl. members of capitalized
//	structs) — plus every workspace file whose imports resolve to the
//	declaring package
//
// `root` bounds the workspace scan; empty root skips it (siblings are
// still searched). Reference identity is the definition site: a hit
// in any file counts iff it resolves to the same (file, span) the
// target does — the same scoping + shape machinery diagnostics use.
func ReferencesAt(root, path string, src []byte, line, col int) (sites []DefSite) {
	defer func() {
		if r := recover(); r != nil {
			recoverEntry("ReferencesAt", r)
			sites = nil
		}
	}()
	hits := collectHits(path, src)
	target, ok := hitAt(hits, line, col)
	if !ok {
		return nil
	}
	// Builtins resolve everywhere; "find references of `if`" is noise.
	if target.SI == nil && target.Def.Kind == DefBuiltin {
		return nil
	}
	targetSite, ok := defSiteOf(target, path)
	if !ok {
		return nil
	}

	candidates := []string{path}
	if reachableBeyondFile(target, targetSite, path, src) {
		declDir := filepath.Dir(targetSite.File)
		candidates = append(candidates, siblingLibraries(declDir)...)
		if exportedTarget(target) {
			candidates = append(candidates, importersOf(root, declDir)...)
		}
	}

	seen := map[DefSite]bool{}
	var out []DefSite
	add := func(site DefSite) {
		if !seen[site] {
			seen[site] = true
			out = append(out, site)
		}
	}
	// The declaration site itself is always a reference result, even
	// when its file isn't otherwise scanned.
	add(targetSite)

	scanned := map[string]bool{}
	for _, file := range candidates {
		if scanned[file] {
			continue
		}
		scanned[file] = true

		fhits := hits
		if file != path {
			fsrc, err := readSource(file)
			if err != nil {
				continue
			}
			fhits = collectHits(file, fsrc)
		}
		for _, h := range fhits {
			if site, ok := defSiteOf(h, file); ok && site == targetSite {
				add(DefSite{File: file, Span: h.Span})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Span.StartLine != out[j].Span.StartLine {
			return out[i].Span.StartLine < out[j].Span.StartLine
		}
		return out[i].Span.StartCol < out[j].Span.StartCol
	})
	return out
}

// reachableBeyondFile reports whether the target can be referenced
// from outside the file it's declared in: it must be a top-level
// declaration in a .phl library (package scope), and not an import
// alias (file-scoped by language rule) or a parameter/local.
func reachableBeyondFile(target hit, targetSite DefSite, path string, src []byte) bool {
	if target.SI == nil {
		switch target.Def.Kind {
		case DefImport, DefParam:
			return false
		}
	}
	if !isLibrary(targetSite.File) {
		return false
	}
	return topLevelDeclSpans(targetSite.File, path, src)[targetSite.Span]
}

// exportedTarget reports whether the target crosses package
// boundaries: a public decl (no leading '#'), or any member of a
// public struct ('#'-private members of public structs are only
// reachable via self, which can't occur in an importer, so scanning
// importers for them is harmless but pointless — the '#'-prefix test
// keeps it simple and correct).
func exportedTarget(target hit) bool {
	name := target.Def.Name
	if target.SI != nil {
		name = target.SI.Name
	}
	return name != "" && name[0] != '#'
}

// topLevelDeclSpans returns the set of declaration-name spans that sit
// at the top level of `file` — exactly what a collect pass (which
// never descends into bodies) defines. Fields and methods surface via
// the same onDefine hook with their member spans.
func topLevelDeclSpans(file, path string, src []byte) map[span.Span]bool {
	var text []byte
	if file == path {
		text = src
	} else {
		read, err := readSource(file)
		if err != nil {
			return nil
		}
		text = read
	}
	tokens, _ := syntax.LexPos(string(text))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	spans := map[span.Span]bool{}
	w := &walker{file: file}
	w.onDefine = func(span span.Span, def Definition) {
		spans[span] = true
	}
	w.collect(newScope(newBuiltinScope()), tree)
	return spans
}

// siblingLibraries lists the .phl files in a package directory
// (including the declaring file itself).
func siblingLibraries(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !isLibrary(e.Name()) {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	return files
}

// ----------------------------------------------------------------------
// Document symbols
// ----------------------------------------------------------------------

// Symbol is one outline entry. Span covers the whole declaration form;
// SelectionSpan is the name itself. Methods nest under their struct
// when it's declared in the same file; struct fields nest under the
// struct.
type Symbol struct {
	Name          string
	Kind          DefKind
	Span          span.Span
	SelectionSpan span.Span
	Children      []Symbol
}

// DocumentSymbols returns the outline of a file's top-level
// declarations.
func DocumentSymbols(path string, src []byte) (syms []Symbol) {
	defer func() {
		if r := recover(); r != nil {
			recoverEntry("DocumentSymbols", r)
			syms = nil
		}
	}()
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	var out []Symbol
	structIdx := map[string]int{} // struct name → index in out

	for _, form := range tree {
		br, ok := asList(form)
		if !ok {
			continue
		}
		switch headIdent(br) {
		case "fun":
			if len(br.Children) >= 4 {
				if name, span, ok := declIdent(br.Children[1]); ok {
					out = append(out, Symbol{Name: name, Kind: DefFun, Span: br.Span, SelectionSpan: span})
				}
			}
		case "macro":
			// (macro ~name (params) body) — `~`@1, the name is child 2.
			if len(br.Children) >= 3 {
				if name, span, ok := declIdent(br.Children[2]); ok {
					out = append(out, Symbol{Name: name, Kind: DefMacro, Span: br.Span, SelectionSpan: span})
				}
			}
		case "struct":
			// declOf reads both the bare `(struct Name f …)` and the typed-field
			// `(struct Name.{ F T … })` forms, so the outline covers either.
			if d, ok := declOf(br); ok && d.Name != "" {
				sym := Symbol{Name: d.Name, Kind: DefStruct, Span: br.Span, SelectionSpan: d.NameSpan}
				for _, f := range d.Fields {
					sym.Children = append(sym.Children, Symbol{
						Name: f.Name, Kind: DefField,
						Span: f.Span, SelectionSpan: f.Span,
					})
				}
				structIdx[d.Name] = len(out)
				out = append(out, sym)
			}
		case "method":
			// (method Receiver.Name (args) body) — owner/name are the LHS/RHS
			// of the dot pattern at child 1.
			var dot *ast.PDot
			if len(br.Children) >= 2 {
				dot, _ = br.Children[1].(*ast.PDot)
			}
			if dot != nil {
				if name, span, ok := declIdent(dot.RHS); ok {
					owner := ""
					if recv, ok := dot.LHS.(*ast.PLeaf); ok {
						owner = recv.Value
					}
					sym := Symbol{Name: name, Kind: DefMethod, Span: br.Span, SelectionSpan: span}
					if idx, ok := structIdx[owner]; ok {
						out[idx].Children = append(out[idx].Children, sym)
					} else {
						if owner != "" {
							sym.Name = owner + "." + name
						}
						out = append(out, sym)
					}
				}
			}
		case "property":
			// (property <Receiver.>Name …) — a struct-field property nests
			// under its owner as a member (DefField); a free-standing one is
			// a top-level faux variable (DefVar).
			d, _ := declOf(br)
			if d.Name != "" {
				kind := DefVar
				if d.Owner != "" {
					kind = DefField
				}
				sym := Symbol{Name: d.Name, Kind: kind, Span: br.Span, SelectionSpan: d.NameSpan}
				switch {
				case d.Owner == "":
					out = append(out, sym)
				default:
					if idx, ok := structIdx[d.Owner]; ok {
						out[idx].Children = append(out[idx].Children, sym)
					} else {
						sym.Name = d.Owner + "." + d.Name
						out = append(out, sym)
					}
				}
			}
		case "var", "const", "let":
			// declOf normalizes a `let`/`let var` form to const/var + binds.
			decl, _ := declOf(br)
			kind := DefVar
			if decl.Head == "const" {
				kind = DefConst
			}
			for _, b := range decl.Binds {
				out = append(out, Symbol{Name: b.Name, Kind: kind, Span: br.Span, SelectionSpan: b.Span})
			}
		}
	}
	return out
}
