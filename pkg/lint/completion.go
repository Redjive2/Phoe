package lint

import (
	"sort"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// CompletionsAt returns the set of names visible at (line, col) in
// file. Lines and columns are 1-based, matching core.Span.
//
// The strategy is straightforward:
//   1. Build the file scope (collect all top-level declarations).
//   2. Walk top-level forms; if the cursor is inside a fun/method body,
//      open a body scope, define its params, recurse.
//   3. When we land in the smallest enclosing scope, dump every visible
//      name (chained up through parents).
//
// Returned definitions are deduplicated by name (innermost wins) and
// sorted alphabetically.
func CompletionsAt(path string, src []byte, line, col int) []Definition {
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)

	root := newScope(PackageScope(path))
	collectFileScope(root, tree)

	cursor := root
	for _, form := range tree {
		if !spanContains(form.GetSpan(), line, col) {
			continue
		}
		if br, ok := asList(form); ok {
			head := headIdent(br)
			switch head {
			case "fun":
				if s := bodyScopeFor(root, br, false, line, col); s != nil {
					cursor = s
				}
			case "method":
				if s := bodyScopeFor(root, br, true, line, col); s != nil {
					cursor = s
				}
			case "for":
				if s := forBodyScope(root, br, line, col); s != nil {
					cursor = s
				}
			}
		}
		break
	}

	return flattenScope(cursor)
}

// bodyScopeFor opens a body scope for a fun/method form when the
// cursor is inside its body. argList is the params quote, body is the
// body quote — different positions for fun vs method, so we resolve
// inside.
func bodyScopeFor(parent *Scope, br *core.PBranch, isMethod bool, line, col int) *Scope {
	var argList, body core.PNode

	if isMethod {
		if len(br.Children) < 5 {
			return nil
		}
		argList, body = br.Children[3], br.Children[4]
	} else {
		switch len(br.Children) {
		case 3:
			argList, body = br.Children[1], br.Children[2]
		case 4:
			argList, body = br.Children[2], br.Children[3]
		default:
			return nil
		}
	}

	_ = isMethod
	bodyScope := newScope(parent)

	if items, ok := quotedList(argList); ok {
		for _, item := range items {
			if leaf, ok := item.(*core.PLeaf); ok && looksLikeIdentifier(leaf.Value) {
				bodyScope.Defs[leaf.Value] = Definition{Name: leaf.Value, Kind: DefParam, Span: leaf.Span}
				continue
			}
			if abr, ok := item.(*core.PBranch); ok && abr.Open == "(" && len(abr.Children) == 2 {
				if h, ok := abr.Children[0].(*core.PLeaf); ok && h.Value == "spread" {
					if name, ok := abr.Children[1].(*core.PLeaf); ok && looksLikeIdentifier(name.Value) {
						bodyScope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
					}
				}
			}
		}
	}

	// Hoist body declarations so completion sees forward references.
	if items, ok := quotedList(body); ok {
		w := &walker{}
		w.collect(bodyScope, items)
	}

	if !spanContains(body.GetSpan(), line, col) {
		// Cursor is in the params, not the body — body scope is still
		// the right answer (params + outer are visible).
	}

	// Recurse: the cursor might be inside a nested fun/method.
	if items, ok := quotedList(body); ok {
		for _, inner := range items {
			if !spanContains(inner.GetSpan(), line, col) {
				continue
			}
			if ibr, ok := asList(inner); ok {
				head := headIdent(ibr)
				switch head {
				case "fun":
					if s := bodyScopeFor(bodyScope, ibr, false, line, col); s != nil {
						return s
					}
				case "method":
					if s := bodyScopeFor(bodyScope, ibr, true, line, col); s != nil {
						return s
					}
				case "for":
					if s := forBodyScope(bodyScope, ibr, line, col); s != nil {
						return s
					}
				}
			}
			break
		}
	}

	return bodyScope
}

// forBodyScope opens a body scope for `(for 'name collection &body)`
// when the cursor is inside its body. Returns nil for the 2-arg
// while-style form (no binding to add) or when the cursor isn't in
// the body subtree.
func forBodyScope(parent *Scope, br *core.PBranch, line, col int) *Scope {
	if len(br.Children) != 4 {
		return nil
	}
	body := br.Children[3]
	if !spanContains(body.GetSpan(), line, col) {
		return nil
	}

	bodyScope := newScope(parent)
	if name, span, ok := quotedIdent(br.Children[1]); ok {
		bodyScope.Defs[name] = Definition{Name: name, Kind: DefConst, Span: span}
	}

	// Body is `&expr`; descend through the sigil so nested fun/method/
	// for forms can push their own scopes.
	inner := body
	if sig, ok := body.(*core.PSigil); ok && sig.Sigil == "&" {
		inner = sig.Inner
	}
	if ibr, ok := asList(inner); ok && spanContains(ibr.GetSpan(), line, col) {
		switch headIdent(ibr) {
		case "fun":
			if s := bodyScopeFor(bodyScope, ibr, false, line, col); s != nil {
				return s
			}
		case "method":
			if s := bodyScopeFor(bodyScope, ibr, true, line, col); s != nil {
				return s
			}
		case "for":
			if s := forBodyScope(bodyScope, ibr, line, col); s != nil {
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
func spanContains(span core.Span, line, col int) bool {
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
