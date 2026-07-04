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
	// Backfill a declaration hit's inferred shape: onDefine fired during collect,
	// before shapes were assigned, so the recorded Definition has ShapeUnknown.
	// Match by the binding name span and copy the shape in, so hovering a binding
	// at its declaration shows its type (references already resolve post-inference).
	w.onShapeAssigned = func(sp span.Span, sh Shape) {
		for i := range hits {
			if hits[i].IsDecl && hits[i].Span == sp {
				hits[i].Def.Shape = sh
			}
		}
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
		return fmt.Sprintf("```pho\n(import '%s')\n```\nimported package", def.Path), h.Span, true
	case DefParam:
		// Show ONLY the parameter (where it is declared) — not the whole function
		// form it sits inside — with its declared type from the enclosing
		// callable's signature, if any.
		text := fmt.Sprintf("```pho\n%s\n```\nparameter", def.Name)
		if pt := paramTypeFor(path, src, def.Span); pt != "" {
			text += " — " + pt
		}
		return text, h.Span, true
	}

	// A TYPE symbol (struct / trait / type alias) declared in this file gets a
	// rich body: its kind, generic parameters, and members or alias target.
	if (def.Kind == DefStruct || def.Kind == DefType) && (def.File == "" || def.File == path) {
		tokens, _ := syntax.LexPos(string(src))
		tree, _ := syntax.ParsePos(tokens)
		tree = syntax.NormalizeDo(tree)
		if rich := typeHover(def, tree); rich != "" {
			out := "```pho\n" + def.Name + "\n```\n" + rich
			if _, doc := declHeader(orPath(def.File, path), path, src, def.Span); doc != "" {
				out += "\n\n" + doc
			}
			return out, h.Span, true
		}
	}

	file := orPath(def.File, path)
	// Hover on a fun/method shows its SIGNATURE (the declared interface —
	// parameter and result types), not its `(= …)` implementation body. Prefer a
	// matching sig's span for the rendered header; the effect surface below still
	// reads the impl via def.Span.
	headerSpan := def.Span
	if def.Kind == DefFun || def.Kind == DefMethod {
		owner, name := "", def.Name
		if i := strings.LastIndex(name, "."); i >= 0 {
			owner, name = name[:i], name[i+1:]
		}
		if sigSpan, ok := funSigNameSpan(file, path, src, owner, name); ok {
			headerSpan = sigSpan
		}
	}
	header, doc := declHeader(file, path, src, headerSpan)
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
	text := fmt.Sprintf("```pho\n%s\n```\n%s", header, hoverKindLabel(def.Kind))
	if def.Shape.Kind != ShapeUnknown {
		text += " — " + shapeLabel(def.Shape)
	}
	// Effect surface for a fun/method declared in this file (Effects.md Phase 6).
	if def.Kind == DefFun || def.Kind == DefMethod {
		if def.File == "" || def.File == path {
			tokens, _ := syntax.LexPos(string(src))
			tree, _ := syntax.ParsePos(tokens)
			tree = syntax.NormalizeDo(tree)
			if label := callableEffectLabel(tree, def.Span); label != "" {
				text += "\n\n**effects**: " + label
			}
		}
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

// hoverKindLabel renders a definition's kind for hover. A `let` binding
// normalizes to DefConst and `let var` to DefVar (declOf reads the surface
// `let`/`let var` into const/var), so render them with the surface keyword the
// user actually wrote rather than the internal kind. Other kinds are unchanged.
func hoverKindLabel(k DefKind) string {
	switch k {
	case DefConst:
		return "let"
	case DefVar:
		return "let var"
	}
	return k.String()
}

// shapeLabel renders an inferred shape as a Pho TYPE name for hover — the
// struct name for an instance, or the built-in type (Number/String/List/Map/
// Boolean/Char/Atom/None/Function) for a primitive. Falls back to the coarse
// shape word only when there is no canonical type name.
func shapeLabel(sh Shape) string {
	if sh.Kind == ShapeInstance {
		if sh.Owner != "" {
			return sh.Owner
		}
		return "struct instance"
	}
	if tn := shapeTypeName(sh.Kind); tn != "" {
		return tn
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
		hdrFile := orPath(file, path)
		// Prefer the method's SIGNATURE (declared interface) over its `(= …)`
		// implementation, matching how a free function hovers.
		hdrSpan := si.Methods[h.Member]
		if sigSpan, ok := funSigNameSpan(hdrFile, path, nil, si.Name, h.Member); ok {
			hdrSpan = sigSpan
		}
		header, doc := declHeader(hdrFile, path, nil, hdrSpan)
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
// funSigNameSpan returns the name-span of a `(fun name …)` / `(method
// Owner.name …)` type SIGNATURE matching the given owner+name in the declaring
// file, if one exists. Hover prefers it over the `(= name …)` implementation so
// a callable's hover shows its declared interface (parameter + result types),
// not its body. `owner` is "" for a free function; for a method it is the
// receiver struct's name, so a same-named method on another struct won't match.
func funSigNameSpan(file, path string, src []byte, owner, name string) (span.Span, bool) {
	var text []byte
	if file == path && src != nil {
		text = src
	} else if read, err := readSource(file); err == nil {
		text = read
	} else {
		return span.Span{}, false
	}
	tokens, _ := syntax.LexPos(string(text))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)
	for _, node := range tree {
		d, ok := declOf(node)
		if ok && d.IsSig && d.Name == name && d.Owner == owner && (d.Head == "fun" || d.Head == "method") {
			return d.NameSpan, true
		}
	}
	return span.Span{}, false
}

// paramTypeFor returns the declared type text of the parameter at paramSpan,
// read from the enclosing callable's inline signature (funSignatureIndex), or ""
// when the parameter is untyped or has no signature. The receiver slot is
// aligned: a method impl's parameter list includes `self` at index 0, which the
// signature — whose receiver slot funSignatureIndex drops — does not.
func paramTypeFor(path string, src []byte, paramSpan span.Span) string {
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	form := declFormContaining(tree, paramSpan)
	if form == nil {
		return ""
	}
	d, ok := declOf(form)
	if !ok || d.Name == "" {
		return ""
	}
	al, ok := d.ArgList.(*ast.PBranch)
	if !ok {
		return ""
	}
	idx := -1
	for i, p := range al.Children {
		if spanCovers(p.GetSpan(), paramSpan) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ""
	}
	if d.Head == "method" {
		if idx == 0 {
			return d.Owner // the receiver `self` — its type is the owner struct
		}
		idx-- // the impl's `self` isn't in the receiver-dropped signature params
	}
	entries := funSignatureIndex(tree)[d.Name]
	if len(entries) == 0 || idx >= len(entries[0].Params) {
		return ""
	}
	return entries[0].Params[idx]
}

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
				case "=":
					// A 4-child `(= …)` is a fun/method IMPLEMENTATION (a decl form);
					// a 2-arg `=` is reassignment, not a declaration.
					if d, ok := declOf(node); ok && (d.Head == "fun" || d.Head == "method") {
						found = node
					}
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
	case "fun", "method", "operator":
		// A flat type SIGNATURE — `(fun name Type… -> Result)` /
		// `(method Recv.name Self Type… -> Result)`. These heads are
		// signature-only now, so render the form verbatim.
		parts := make([]string, len(br.Children))
		for i, c := range br.Children {
			parts[i] = pnodeText(c)
		}
		return "(" + strings.Join(parts, " ") + ")"
	case "macro":
		// (macro ~name (params) body) — `~`@1, name@2, params@3.
		if len(br.Children) >= 4 {
			return fmt.Sprintf("(macro ~%s %s ...)", pnodeText(br.Children[2]), pnodeText(br.Children[3]))
		}
	case "=":
		// A 4-child `(= name (params) body)` / `(= Owner.name …)` impl — render its
		// header like a fun/method decl. (A 2-arg reassign never reaches here.)
		if len(br.Children) == 4 {
			return fmt.Sprintf("(= %s %s ...)",
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
	// Render the typed-field form `(struct Name.{ T F … })` when any field
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
	case *ast.PSlash:
		return pnodeText(node.LHS) + "/" + pnodeText(node.RHS)
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

// typeHover renders the rich hover body for a TYPE symbol declared in this
// file: its precise KIND, any generic TEMPLATE parameters, and — for a struct
// its fields + methods, for a trait its required methods + properties, for a
// type alias what it EQUALS. Returns "" when def.Name isn't a struct/trait/type
// declared in the tree.
func typeHover(def Definition, tree []ast.PNode) string {
	var form *ast.PBranch
	var d topLevelDecl
	idx := -1
	for i, node := range tree {
		dd, ok := declOf(node)
		if !ok || dd.Name != def.Name {
			continue
		}
		if dd.Head == "struct" || dd.Head == "trait" || dd.Head == "type" {
			form, _ = node.(*ast.PBranch)
			d, idx = dd, i
			break
		}
	}
	if form == nil {
		return ""
	}

	var b strings.Builder
	kind := map[string]string{"struct": "struct", "trait": "trait", "type": "type alias"}[d.Head]
	b.WriteString("**" + kind + "**")
	if gp := genericParamText(tree, idx); gp != "" {
		b.WriteString("  ·  generic `" + gp + "`")
	}

	switch d.Head {
	case "struct":
		if len(d.Fields) > 0 {
			b.WriteString("\n\n**fields**\n")
			for _, f := range d.Fields {
				if f.Type != nil {
					fmt.Fprintf(&b, "- `%s`: %s\n", f.Name, pnodeText(f.Type))
				} else {
					fmt.Fprintf(&b, "- `%s`\n", f.Name)
				}
			}
		}
		if ms := structMethodLines(tree, def.Name); len(ms) > 0 {
			b.WriteString("\n**methods**\n")
			for _, m := range ms {
				b.WriteString("- `" + m + "`\n")
			}
		}
	case "trait":
		writeTraitMembers(&b, form)
	case "type":
		if d.Body != nil {
			b.WriteString("\n\n= `" + pnodeText(d.Body) + "`")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// genericParamText renders the parameters of a `(template …)` immediately
// preceding the declaration at idx, or "" if there is none. A bounded parameter
// shows its bound: `B <: Some_Type`.
func genericParamText(tree []ast.PNode, idx int) string {
	if idx <= 0 {
		return ""
	}
	d, ok := declOf(tree[idx-1])
	if !ok || d.Head != "template" {
		return ""
	}
	ps := make([]string, 0, len(d.TemplateParams))
	for _, p := range d.TemplateParams {
		if p.Bound != nil {
			ps = append(ps, p.Name+" <: "+pnodeText(p.Bound))
		} else {
			ps = append(ps, p.Name)
		}
	}
	return strings.Join(ps, ", ")
}

// structMethodLines renders one line per method declared on `structName`,
// preferring a type SIGNATURE (which carries the parameter TYPES and result
// type) over the implementation (which has only argument names). Both instance
// `(method Owner.name …)` and `(static method Owner.name …)` declarations count.
func structMethodLines(tree []ast.PNode, structName string) []string {
	sig := map[string]string{}
	var order []string
	for _, node := range tree {
		d, ok := declOf(node)
		if !ok || d.Owner != structName || d.Name == "" {
			continue
		}
		if d.Head != "method" && !(d.Head == "static" && d.Sub == "method") {
			continue
		}
		_, seen := sig[d.Name]
		if !seen {
			order = append(order, d.Name)
		}
		// A signature wins over an implementation regardless of source order.
		if !seen || d.IsSig {
			sig[d.Name] = methodSigText(d)
		}
	}
	out := make([]string, 0, len(order))
	for _, n := range order {
		out = append(out, sig[n])
	}
	return out
}

// methodSigText renders a method declaration as `(name type…) [→ result]` — the
// raw signature shape: the parameter TYPES (from a signature) or argument names
// (from an impl fallback), and the result type when it's a signature. The
// receiver is dropped for an INSTANCE method (its param 0 is `Self`), but kept
// for a STATIC method, whose receiver type is implicit so param 0 is a real
// argument (see declOf's static case).
func methodSigText(d topLevelDecl) string {
	dropReceiver := d.Head != "static"
	var params []string
	if al, ok := d.ArgList.(*ast.PBranch); ok {
		for i, c := range al.Children {
			if i == 0 && dropReceiver {
				continue
			}
			params = append(params, pnodeText(c))
		}
	}
	s := "(" + d.Name
	if len(params) > 0 {
		s += " " + strings.Join(params, " ")
	}
	s += ")"
	if d.IsSig && d.Body != nil {
		s += " → " + pnodeText(d.Body)
	}
	return s
}

// writeTraitMembers renders a trait form's required methods and properties.
func writeTraitMembers(b *strings.Builder, form *ast.PBranch) {
	_, members := traitFormParts(form)
	var methods, props []string
	for _, sub := range members {
		sb, ok := sub.(*ast.PBranch)
		if !ok {
			continue
		}
		name := traitMemberName(sb)
		if name == "" {
			continue
		}
		switch headIdent(sb) {
		case "method":
			methods = append(methods, traitMethodText(sb, name))
		case "property":
			props = append(props, traitPropText(sb, name))
		}
	}
	if len(methods) > 0 {
		b.WriteString("\n\n**methods**\n")
		for _, m := range methods {
			b.WriteString("- `" + m + "`\n")
		}
	}
	if len(props) > 0 {
		b.WriteString("\n**properties**\n")
		for _, p := range props {
			b.WriteString("- `" + p + "`\n")
		}
	}
}

// traitMethodText renders `name(param…) [→ result]` for a trait method sub-form
// `(method self.name (self param…) result)`.
func traitMethodText(sb *ast.PBranch, name string) string {
	var params []string
	if len(sb.Children) >= 3 {
		if al, ok := sb.Children[2].(*ast.PBranch); ok {
			for i, c := range al.Children {
				if i == 0 {
					continue // receiver
				}
				params = append(params, pnodeText(c))
			}
		}
	}
	s := name + "(" + strings.Join(params, " ") + ")"
	if len(sb.Children) >= 4 {
		s += " → " + pnodeText(sb.Children[3])
	}
	return s
}

// traitPropText renders `name[: Type] (get[/set])` for a trait property
// sub-form, unwrapping a typed `(Type self.name)` name to show the type.
func traitPropText(sb *ast.PBranch, name string) string {
	s := name
	if len(sb.Children) >= 2 {
		if tb, ok := sb.Children[1].(*ast.PBranch); ok && len(tb.Children) == 2 {
			s += ": " + pnodeText(tb.Children[0])
		}
	}
	var acc []string
	for _, c := range sb.Children[2:] {
		if lf, ok := c.(*ast.PLeaf); ok && (lf.Value == "get" || lf.Value == "set") {
			acc = append(acc, lf.Value)
		}
	}
	if len(acc) > 0 {
		s += " (" + strings.Join(acc, "/") + ")"
	}
	return s
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
	structIdx := map[string]int{}   // struct name → index in out
	clauseSeen := map[string]bool{} // "Owner.name" of clause sets already emitted

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
		case "=":
			// A 4-child `(= name (params) body)` / `(= Owner.name …)` is a fun/method
			// IMPLEMENTATION (declOf normalizes it to Head fun/method); emit a symbol
			// like the fun/method cases. A 2-arg reassign yields no symbol.
			if d, ok := declOf(br); ok && d.Name != "" {
				switch d.Head {
				case "fun":
					out = append(out, Symbol{Name: d.Name, Kind: DefFun, Span: br.Span, SelectionSpan: d.NameSpan})
				case "method":
					sym := Symbol{Name: d.Name, Kind: DefMethod, Span: br.Span, SelectionSpan: d.NameSpan}
					if idx, ok := structIdx[d.Owner]; ok {
						out[idx].Children = append(out[idx].Children, sym)
					} else {
						if d.Owner != "" {
							sym.Name = d.Owner + "." + d.Name
						}
						out = append(out, sym)
					}
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
			// `(struct Name.{ T F … })` forms, so the outline covers either.
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
			// declOf normalizes a `let`/`let var` form to const/var + binds — or an
			// IMPLEMENTATION CLAUSE `(let name (patterns) [where g] = body)` to Head
			// fun/method. A clause SET (several clauses of one name) yields ONE
			// symbol, at the first clause.
			decl, ok := declOf(br)
			if ok && decl.IsClause && decl.Name != "" {
				key := decl.Owner + "." + decl.Name
				if clauseSeen[key] {
					continue
				}
				clauseSeen[key] = true
				switch decl.Head {
				case "fun":
					out = append(out, Symbol{Name: decl.Name, Kind: DefFun, Span: br.Span, SelectionSpan: decl.NameSpan})
				case "method":
					sym := Symbol{Name: decl.Name, Kind: DefMethod, Span: br.Span, SelectionSpan: decl.NameSpan}
					if idx, ok := structIdx[decl.Owner]; ok {
						out[idx].Children = append(out[idx].Children, sym)
					} else {
						if decl.Owner != "" {
							sym.Name = decl.Owner + "." + decl.Name
						}
						out = append(out, sym)
					}
				}
				continue
			}
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
