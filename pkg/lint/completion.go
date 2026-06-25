package lint

import (
	"sort"

	"pho/pkg/ast"
	"pho/pkg/span"
	"pho/pkg/syntax"
)

// CompletionsAt returns the set of names visible at (line, col) in
// file. Lines and columns are 1-based, matching span.Span.
//
// The strategy is straightforward:
//  1. Build the file scope (collect all top-level declarations and
//     their inferred shapes).
//  2. Walk top-level forms; if the cursor is inside a fun/method body,
//     open a body scope, define its params, recurse.
//  3. If the cursor sits right after a dot whose receiver has a known
//     shape (struct instance, dict, import), return that receiver's
//     members instead of scope names.
//  4. Otherwise dump every visible name (chained up through parents).
//
// Returned definitions are deduplicated by name (innermost wins) and
// sorted alphabetically.
func CompletionsAt(path string, src []byte, line, col int) (defs []Definition) {
	defer func() {
		if r := recover(); r != nil {
			recoverEntry("CompletionsAt", r)
			defs = nil
		}
	}()
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	w := newWalker(path)
	root := newScope(PackageScope(path))
	w.collect(root, tree)
	w.assignDeclShapes(root, tree)

	cursor := root
	for _, form := range tree {
		if !spanContains(form.GetSpan(), line, col) {
			continue
		}
		if br, ok := asList(form); ok {
			head := headIdent(br)
			switch head {
			case "fun", "macro":
				if s := bodyScopeFor(w, root, br, false, line, col); s != nil {
					cursor = s
				}
			case "method":
				if s := bodyScopeFor(w, root, br, true, line, col); s != nil {
					cursor = s
				}
			case "property":
				if s := propertyBodyScope(w, root, br, line, col); s != nil {
					cursor = s
				}
			case "foreach":
				if s := forBodyScope(w, root, br, line, col); s != nil {
					cursor = s
				}
			}
		}
		break
	}

	// Inside a `&` block the implicit one-argument parameter `it` is in scope
	// (see the `block` builtin), so offer it as a completion. A child scope
	// keeps it from leaking into the returned `cursor` for callers that reuse
	// it. Nested blocks all share the name `it`, so a single entry suffices.
	if cursorInAmpBlock(tree, line, col) {
		blockScope := newScope(cursor)
		blockScope.Defs["it"] = Definition{Name: "it", Kind: DefParam}
		cursor = blockScope
	}

	if recv, inDot := dotReceiverAt(tokens, line, col); inDot {
		if recv != "" {
			if members, ok := memberCompletions(w, cursor, recv); ok {
				return members
			}
		}
		// In a dot context but the receiver's members are unknown — an
		// unresolved receiver, an untyped value, or a chain like `a.b.`
		// whose type we don't track. Offer nothing rather than dumping
		// every name in scope (none of which are valid after a dot).
		return nil
	}

	return flattenScope(cursor)
}

// cursorInAmpBlock reports whether (line, col) falls inside an `&` block
// anywhere in the tree, so completion there can offer the implicit `it`. The
// tree is already NormalizeDo'd, so `&do …` has become `&(do …)` and every
// block is a plain `&` sigil.
func cursorInAmpBlock(nodes []ast.PNode, line, col int) bool {
	for _, n := range nodes {
		if n == nil || !spanContains(n.GetSpan(), line, col) {
			continue
		}
		switch v := n.(type) {
		case *ast.PSigil:
			if v.Sigil == "&" {
				return true
			}
			return cursorInAmpBlock([]ast.PNode{v.Inner}, line, col)
		case *ast.PBranch:
			return cursorInAmpBlock(v.Children, line, col)
		case *ast.PDot:
			return cursorInAmpBlock([]ast.PNode{v.LHS, v.RHS}, line, col)
		case *ast.PMacroCall:
			if cursorInAmpBlock([]ast.PNode{v.Head}, line, col) {
				return true
			}
			return cursorInAmpBlock(v.Args, line, col)
		}
	}
	return false
}

// dotReceiverAt detects whether the cursor is in a `recv.` / `recv.partial`
// completion context. inDot is true for any such context; recv is the
// receiver's bare-identifier name, or "" when the receiver is itself a
// member-access chain segment (e.g. the `b` in `a.b.`) whose type we don't
// track — distinguishing those lets the caller offer member completions or
// nothing, never a misleading whole-scope dump.
func dotReceiverAt(tokens []syntax.Token, line, col int) (recv string, inDot bool) {
	// Index of the last token that ends at or before the cursor.
	k := -1
	for i, t := range tokens {
		if t.Span.EndLine < line || (t.Span.EndLine == line && t.Span.EndCol <= col) {
			k = i
			continue
		}
		break
	}
	if k < 1 {
		return "", false
	}
	// recvOf returns the identifier at index i — but only as a genuine
	// receiver, not a chain segment that is itself preceded by a dot
	// (a.b.), since the chain's intermediate type isn't tracked.
	recvOf := func(i int) string {
		if i < 0 || !looksLikeIdentifier(tokens[i].Value) {
			return ""
		}
		if i >= 1 && tokens[i-1].Value == "." {
			return ""
		}
		return tokens[i].Value
	}
	// `recv.|` — last token is the dot itself.
	if tokens[k].Value == "." {
		return recvOf(k - 1), true
	}
	// `recv.par|tial` — last token is the partial member, preceded by a dot.
	if k >= 2 && tokens[k-1].Value == "." && looksLikeIdentifier(tokens[k].Value) &&
		tokens[k].Span.EndLine == line && tokens[k].Span.EndCol == col {
		return recvOf(k - 2), true
	}
	return "", false
}

// memberCompletions resolves the receiver and lists its members:
// package exports for imports, fields and methods for struct
// instances (privacy-filtered), known keys for dicts. Returns
// ok=false when the receiver's shape isn't known — the caller falls
// back to plain scope completion.
func memberCompletions(w *walker, scope *Scope, recv string) ([]Definition, bool) {
	def, _, found := scope.Lookup(recv)
	if !found {
		return nil, false
	}

	if def.Kind == DefImport && def.Path != "" {
		exports := w.exportsFor(def.Path)
		if exports == nil {
			return nil, false
		}
		out := make([]Definition, 0, len(exports))
		for _, d := range exports {
			out = append(out, d)
		}
		sortDefs(out)
		return out, true
	}

	sh := def.Shape
	var out []Definition

	switch sh.Kind {
	case ShapeInstance:
		if si, ok := w.resolveStruct(scope, sh); ok {
			visible := func(name string) bool {
				return sh.Privileged || !isHashPrivate(name)
			}
			for name, span := range si.Fields {
				if visible(name) {
					out = append(out, Definition{Name: name, Kind: DefField, Span: span, File: si.File})
				}
			}
			for name, span := range si.Methods {
				if visible(name) {
					out = append(out, Definition{Name: name, Kind: DefMethod, Span: span, File: si.MethodFiles[name]})
				}
			}
		}

	case ShapeDict:
		for name, span := range sh.Keys {
			// Suggest the full bracket-index form — dict lookup is dynamic
			// indexing (`d.['key']`), not bare field access, so completing
			// after the dot inserts the brackets and the quoted literal.
			out = append(out, Definition{Name: `['` + name + `']`, Kind: DefField, Span: span})
		}
	}

	// Object-model members: the built-in primitive members for the inferred
	// type plus the universal (Unknown) members that apply to every value, and
	// any primitive extensions declared in scope (collected like struct methods
	// under the type name, so a user method on e.g. Number surfaces here).
	out = append(out, builtinMemberDefs(sh.Kind)...)
	if tn := shapeTypeName(sh.Kind); tn != "" {
		if si, ok := scope.LookupStruct(tn); ok {
			for name, span := range si.Methods {
				out = append(out, Definition{Name: name, Kind: DefMethod, Span: span, File: si.MethodFiles[name]})
			}
		}
		out = append(out, w.importedPrimitiveExtensions(scope, tn)...)
	}

	out = dedupDefsByName(out)
	if len(out) == 0 {
		return nil, false
	}
	sortDefs(out)
	return out, true
}

// dedupDefsByName keeps the first Definition seen for each name, so a struct's
// own member or a user extension takes precedence over a built-in of the same
// name.
func dedupDefsByName(defs []Definition) []Definition {
	seen := make(map[string]bool, len(defs))
	out := make([]Definition, 0, len(defs))
	for _, d := range defs {
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		out = append(out, d)
	}
	return out
}

func sortDefs(defs []Definition) {
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
}

func startsLower(name string) bool {
	return name != "" && name[0] >= 'a' && name[0] <= 'z'
}

// isHashPrivate reports whether name carries the new `#` private marker. During
// the migration this composes with the lowercase rule (both signal private);
// at the flip `#` becomes the sole visibility marker (Doc/PlanV1/Syntax.md).
func isHashPrivate(name string) bool {
	return name != "" && name[0] == '#'
}

// propertyBodyScope opens the body scope when the cursor sits inside a
// property's getter or setter — anonymous fun/method forms at children 3 and
// 5 of (property <Receiver.>Name get getter [set setter]).
func propertyBodyScope(w *walker, parent *Scope, br *ast.PBranch, line, col int) *Scope {
	for i := 3; i < len(br.Children); i += 2 {
		if !spanContains(br.Children[i].GetSpan(), line, col) {
			continue
		}
		if ibr, ok := asList(br.Children[i]); ok {
			return bodyScopeFor(w, parent, ibr, headIdent(ibr) == "method", line, col)
		}
	}
	return nil
}

// bodyScopeFor opens a body scope for a fun/method form when the
// cursor is inside its body. argList is the params quote, body is the
// body quote — different positions for fun vs method, so we resolve
// inside.
func bodyScopeFor(w *walker, parent *Scope, br *ast.PBranch, isMethod bool, line, col int) *Scope {
	var argList, body ast.PNode

	switch {
	case isMethod:
		// (method Receiver.Name (args) body) — argList@2, body@3.
		if len(br.Children) < 4 {
			return nil
		}
		argList, body = br.Children[2], br.Children[3]
	case headIdent(br) == "macro":
		// (macro ~name (params) body) — argList@3, body@4 (`~`@1, name@2).
		if len(br.Children) < 5 {
			return nil
		}
		argList, body = br.Children[3], br.Children[4]
	default:
		switch len(br.Children) {
		case 3:
			argList, body = br.Children[1], br.Children[2]
		case 4:
			argList, body = br.Children[2], br.Children[3]
		default:
			return nil
		}
	}

	bodyScope := newScope(parent)

	if items, ok := declList(argList); ok {
		for _, item := range items {
			if leaf, ok := item.(*ast.PLeaf); ok && looksLikeIdentifier(leaf.Value) {
				bodyScope.Defs[leaf.Value] = Definition{Name: leaf.Value, Kind: DefParam, Span: leaf.Span}
				continue
			}
			if abr, ok := item.(*ast.PBranch); ok && abr.Open == "(" && len(abr.Children) == 2 {
				if h, ok := abr.Children[0].(*ast.PLeaf); ok && (h.Value == "spread" || h.Value == "optional") {
					if name, ok := abr.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
						bodyScope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
					}
				}
			}
		}
	}

	// In a method body the receiver is a privileged instance of the
	// owner struct — that's what makes `self.` completion work.
	if isMethod {
		// The receiver/owner is the LHS of the `Receiver.Name` dot pattern, or
		// the bare first child for an anonymous method.
		owner := ""
		if dot, ok := br.Children[1].(*ast.PDot); ok {
			if recv, ok := dot.LHS.(*ast.PLeaf); ok {
				owner = recv.Value
			}
		} else if len(br.Children) >= 2 {
			if recv, ok := br.Children[1].(*ast.PLeaf); ok {
				owner = recv.Value
			}
		}
		if looksLikeIdentifier(owner) {
			if d, ok := bodyScope.Defs["self"]; ok {
				// Same owner→shape rule the walker uses: a struct receiver is a
				// privileged instance; a built-in collection/primitive receiver
				// takes that type's shape so `self.` completes its members.
				d.Shape = selfShapeForOwner(parent, owner)
				bodyScope.Defs["self"] = d
			}
		}
	}

	// Hoist body declarations so completion sees forward references,
	// and record their shapes so dot completion works on body locals.
	if items, ok := declList(body); ok {
		w.collect(bodyScope, items)
		w.assignDeclShapes(bodyScope, items)
	}

	// Recurse: the cursor might be inside a nested fun/method.
	if items, ok := declList(body); ok {
		for _, inner := range items {
			if !spanContains(inner.GetSpan(), line, col) {
				continue
			}
			if ibr, ok := asList(inner); ok {
				head := headIdent(ibr)
				switch head {
				case "fun", "macro":
					if s := bodyScopeFor(w, bodyScope, ibr, false, line, col); s != nil {
						return s
					}
				case "method":
					if s := bodyScopeFor(w, bodyScope, ibr, true, line, col); s != nil {
						return s
					}
				case "foreach":
					if s := forBodyScope(w, bodyScope, ibr, line, col); s != nil {
						return s
					}
				}
			}
			break
		}
	}

	return bodyScope
}

// forBodyScope opens a body scope for `(foreach name in collection body)`
// when the cursor is inside its body, so completion there sees the loop
// variable. Returns nil when the cursor isn't in the body subtree.
func forBodyScope(w *walker, parent *Scope, br *ast.PBranch, line, col int) *Scope {
	// (foreach name in collection body) — 5 children; body is the last.
	if len(br.Children) != 5 {
		return nil
	}
	body := br.Children[4]
	if !spanContains(body.GetSpan(), line, col) {
		return nil
	}

	bodyScope := newScope(parent)
	if name, span, ok := declIdent(br.Children[1]); ok {
		bodyScope.Defs[name] = Definition{Name: name, Kind: DefConst, Span: span}
	}

	// Body is `&expr`; descend through the sigil so nested fun/method/
	// for forms can push their own scopes.
	inner := body
	if sig, ok := body.(*ast.PSigil); ok && sig.Sigil == "&" {
		inner = sig.Inner
	}
	if ibr, ok := asList(inner); ok && spanContains(ibr.GetSpan(), line, col) {
		switch headIdent(ibr) {
		case "fun", "macro":
			if s := bodyScopeFor(w, bodyScope, ibr, false, line, col); s != nil {
				return s
			}
		case "method":
			if s := bodyScopeFor(w, bodyScope, ibr, true, line, col); s != nil {
				return s
			}
		case "foreach":
			if s := forBodyScope(w, bodyScope, ibr, line, col); s != nil {
				return s
			}
		}
	}

	return bodyScope
}

// flattenScope returns every definition visible from `s` up through
// its parents, deduplicated (innermost wins) and sorted by name.
func flattenScope(s *Scope) []Definition {
	seen := map[string]Definition{}
	for cur := s; cur != nil; cur = cur.Parent {
		for n, d := range cur.Defs {
			// Methods aren't bindable by bare name — they're reached via
			// `instance.method`. They sit in scope only under a
			// receiver-qualified key ("Owner.name") so the redeclaration
			// check can spot a method defined twice on the same owner;
			// they must never surface as bare-name completions.
			if d.Kind == DefMethod {
				continue
			}
			if _, ok := seen[n]; !ok {
				seen[n] = d
			}
		}
	}
	out := make([]Definition, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// spanContains reports whether the (line, col) point lies inside span.
// Both are 1-based; a point matching the span's exclusive end is
// outside.
func spanContains(span span.Span, line, col int) bool {
	if line < span.StartLine || line > span.EndLine {
		return false
	}
	if line == span.StartLine && col < span.StartCol {
		return false
	}
	if line == span.EndLine && col > span.EndCol {
		return false
	}
	return true
}
