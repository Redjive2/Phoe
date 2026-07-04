package lint

import (
	"strings"

	"pho/pkg/ast"
	"pho/pkg/span"
	"pho/pkg/syntax"
)

// This file backs the LSP features that don't reduce to the reference resolver:
// inlay hints (inferred binding types), signature help (call parameter hints),
// and go-to-implementation (trait → satisfying structs).

// InlayHintInfo is one inferred-type hint: the 1-based position to insert it at
// (the end of the bound name) and its label (e.g. ": Number").
type InlayHintInfo struct {
	Line, Col int
	Label     string
}

// InlayHintsAt returns an inferred-type hint for every `let`/`const`/`var`
// binding whose value has a known shape — surfacing the types a gradual-typing
// user would otherwise have to infer by hand. Bindings whose shape is unknown
// get no hint (no noise).
func InlayHintsAt(path string, src []byte) (hints []InlayHintInfo) {
	defer func() { _ = recover() }()
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)
	w := newWalker(path)
	w.walkFile(tree, PackageScope(path))
	for _, form := range tree {
		w.collectBindingHints(w.fileScope, form, &hints)
	}
	return hints
}

// collectBindingHints walks n emitting a type hint per binding, switching to a
// function/method body's scope when it descends into one so in-body bindings
// infer against the right locals.
func (w *walker) collectBindingHints(scope *Scope, n ast.PNode, hints *[]InlayHintInfo) {
	br, ok := asList(n)
	if !ok {
		return
	}
	// declOf normalizes `let`/`let var` to Head "const"/"var" and a fun/method
	// IMPL `(= …)` to "fun"/"method" — so switch on the NORMALIZED head, not the
	// raw keyword.
	if d, isDecl := declOf(br); isDecl {
		switch d.Head {
		case "fun", "method", "macro":
			if d.Body != nil {
				body := scope
				if bs, ok := w.bodyScopes[d.Body]; ok {
					body = bs
				}
				w.collectBindingHints(body, d.Body, hints)
				return
			}
		case "const", "var":
			for _, b := range d.Binds {
				if b.Value == nil {
					continue
				}
				if label := inlayTypeLabel(w.inferShape(scope, b.Value)); label != "" {
					*hints = append(*hints, InlayHintInfo{Line: b.Span.EndLine, Col: b.Span.EndCol, Label: ": " + label})
				}
				w.collectBindingHints(scope, b.Value, hints) // nested lambdas etc.
			}
			return
		}
	}
	for _, c := range br.Children {
		w.collectBindingHints(scope, c, hints)
	}
}

// inlayTypeLabel is a clean type name for a shape, or "" when there's nothing
// useful to show (an unknown shape).
func inlayTypeLabel(sh Shape) string {
	if sh.Kind == ShapeInstance && sh.Owner != "" {
		return sh.Owner
	}
	return shapeTypeName(sh.Kind) // "" for ShapeUnknown / non-primitive
}

// SigHelp is a resolved signature for a call the cursor is inside.
type SigHelp struct {
	Label       string   // e.g. "add(Number Number) → Number"
	Params      []string // per-parameter labels
	ActiveParam int      // which parameter the cursor is on
}

// SignatureHelpAt returns parameter help for the innermost call the cursor is
// inside, resolved from a same-file inline signature `(fun name (T…) R)` /
// `(method Recv.name (Self T…) R)`. Source-level: the params/result are shown as
// written. ok=false when the cursor isn't in a call with a known signature.
func SignatureHelpAt(path string, src []byte, line, col int) (out SigHelp, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			out, ok = SigHelp{}, false
		}
	}()
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	sigs := funSignatureIndex(tree)
	call := innermostCall(tree, line, col)
	if call == nil {
		return SigHelp{}, false
	}
	head, isLeaf := call.Children[0].(*ast.PLeaf)
	if !isLeaf {
		return SigHelp{}, false
	}
	entries := sigs[head.Value]
	if len(entries) == 0 {
		return SigHelp{}, false
	}
	// Pick the OVERLOAD (Features.md §9) whose param count fits the call's
	// argument count best: the first entry accepting at least as many params
	// as the call already has, else the first (widest hint beats none).
	args := len(call.Children) - 1
	sig := entries[0]
	for _, e := range entries {
		if len(e.Params) >= args {
			sig = e
			break
		}
	}
	active := 0
	for i := 1; i < len(call.Children); i++ {
		if beforeCursor(call.Children[i].GetSpan(), line, col) {
			active = i // cursor is past this argument
		}
	}
	if len(sig.Params) > 0 && active >= len(sig.Params) {
		active = len(sig.Params) - 1 // clamp: cursor past the last argument stays on it
	}
	return SigHelp{Label: sig.Label, Params: sig.Params, ActiveParam: active}, true
}

type sigEntry struct {
	Label  string
	Params []string
}

// funSignatureIndex scans the tree for inline function/method SIGNATURE forms —
// `(fun name (T…) R)` / `(method Recv.name (Self T…) R)` — and renders each into
// a display label + per-parameter list, keyed by the callable's bare name. A
// name may carry several entries: its §9 overloads, in source order.
func funSignatureIndex(tree []ast.PNode) map[string][]sigEntry {
	out := map[string][]sigEntry{}
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || !d.IsSig || d.Name == "" {
			continue
		}
		al, ok := d.ArgList.(*ast.PBranch)
		if !ok {
			continue
		}
		start := 0
		if d.Head == "method" && len(al.Children) > 0 {
			start = 1 // drop the receiver slot
		}
		params := make([]string, 0, len(al.Children))
		for _, p := range al.Children[start:] {
			params = append(params, pnodeText(p))
		}
		label := d.Name + "(" + strings.Join(params, " ") + ")"
		if d.Body != nil {
			label += " → " + pnodeText(d.Body)
		}
		out[d.Name] = append(out[d.Name], sigEntry{Label: label, Params: params})
	}
	return out
}

// innermostCall returns the deepest `(head …)` list whose span contains the
// cursor, or nil.
func innermostCall(tree []ast.PNode, line, col int) *ast.PBranch {
	var found *ast.PBranch
	var visit func(n ast.PNode)
	visit = func(n ast.PNode) {
		br, ok := n.(*ast.PBranch)
		if !ok {
			return
		}
		if br.Open == "(" && len(br.Children) >= 1 && spanContains(br.Span, line, col) {
			found = br // deeper matches overwrite shallower ones
		}
		for _, c := range br.Children {
			visit(c)
		}
	}
	for _, f := range tree {
		visit(f)
	}
	return found
}

// beforeCursor reports whether span sp ends at or before the cursor.
func beforeCursor(sp span.Span, line, col int) bool {
	return sp.EndLine < line || (sp.EndLine == line && sp.EndCol <= col)
}

// ImplementationsAt returns the declaration sites of local structs that satisfy
// the trait named at the cursor (structural satisfaction, by required-member
// name). Empty when the cursor isn't on a trait or nothing satisfies it.
func ImplementationsAt(path string, src []byte, line, col int) (sites []DefSite) {
	defer func() { _ = recover() }()
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	name := identAt(tree, line, col)
	if name == "" {
		return nil
	}
	req, isTrait := traitRequiredNames(tree, name)
	if !isTrait {
		return nil
	}
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || d.Head != "struct" || d.Name == "" {
			continue
		}
		if structSatisfies(tree, d.Name, req) {
			sites = append(sites, DefSite{File: path, Span: d.NameSpan})
		}
	}
	return sites
}

// traitRequiredNames returns the required member names of the trait `name` and
// whether `name` is a trait declared in the tree.
func traitRequiredNames(tree []ast.PNode, name string) (names []string, isTrait bool) {
	for _, form := range tree {
		br, ok := form.(*ast.PBranch)
		if !ok || headIdent(br) != "trait" {
			continue
		}
		if d, ok := declOf(br); !ok || d.Name != name {
			continue
		}
		_, members := traitFormParts(br)
		for _, sub := range members {
			if sb, ok := sub.(*ast.PBranch); ok {
				if n := traitMemberName(sb); n != "" {
					names = append(names, n)
				}
			}
		}
		return names, true
	}
	return nil, false
}

// structSatisfies reports whether struct `structName` provides every required
// member (as a field or a method), by name.
func structSatisfies(tree []ast.PNode, structName string, required []string) bool {
	have := map[string]bool{}
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok {
			continue
		}
		if d.Head == "struct" && d.Name == structName {
			for _, f := range d.Fields {
				have[f.Name] = true
			}
		}
		if d.Head == "method" && d.Owner == structName && d.Name != "" {
			have[d.Name] = true
		}
	}
	for _, r := range required {
		if !have[r] && !isUniversalMember(r) {
			return false
		}
	}
	return len(required) > 0
}

// identAt returns the identifier text at (line, col), or "".
func identAt(tree []ast.PNode, line, col int) string {
	var found string
	var visit func(n ast.PNode)
	visit = func(n ast.PNode) {
		switch node := n.(type) {
		case *ast.PLeaf:
			if spanContains(node.Span, line, col) {
				found = node.Value
			}
		case *ast.PBranch:
			for _, c := range node.Children {
				visit(c)
			}
		case *ast.PDot:
			visit(node.LHS)
			visit(node.RHS)
		}
	}
	for _, f := range tree {
		visit(f)
	}
	return found
}
