package lint

import (
	"pho/pkg/ast"
	"pho/pkg/span"
)

// Top-level declaration normalization.
//
// Several passes need to know what a top-level form declares and where:
// the scope collector (walker.collectOne), the package export surface
// (PackageExports), and the imported-struct tables (PackageStructs). Each
// used to re-index the form's children itself — fun's name at child 1,
// method's owner at 1 and name at 2, struct's fields at 2 — and those
// positions drifting apart between passes is a real bug source (e.g. one
// pass accepting a 4-child method while another wanted 5). declOf encodes
// each form's layout exactly once; every consumer reads the normalized
// result and applies its own policy (which kinds to keep, capitalization,
// scope vs export, ...).

// fieldDecl is one declared struct field.
type fieldDecl struct {
	Name string
	Span span.Span
}

// structFieldLeaves returns the declared field-name leaves of a struct form
// `(struct Name f0 f1 …)` — the bare identifier children after the name.
// Non-identifier children (parser-recovery debris from a mid-edit, or a stray
// parenthesized form) are skipped, so consumers stay faithful to what the
// struct really declares.
func structFieldLeaves(br *ast.PBranch) []*ast.PLeaf {
	if len(br.Children) < 2 {
		return nil
	}
	var out []*ast.PLeaf
	for _, c := range br.Children[2:] {
		if lf, ok := c.(*ast.PLeaf); ok && looksLikeIdentifier(lf.Value) {
			out = append(out, lf)
		}
	}
	return out
}

// bindDecl is one (name value) pair from a var/const form.
type bindDecl struct {
	Name  string
	Span  span.Span
	Value ast.PNode
}

// topLevelDecl is the normalized declaration a top-level form makes.
// Head is the form keyword ("fun"/"method"/"struct"/"const"/"var"/
// "import"/"goimport"); the populated fields depend on it:
//
//	fun     — Name/NameSpan (empty for the anonymous 2-arg form)
//	method  — Owner/OwnerSpan (receiver) + Name/NameSpan (method name)
//	struct  — Name/NameSpan + Fields
//	var/con — Binds (the name/value pairs)
//	import  — nothing beyond Branch (args parsed by collectImports)
type topLevelDecl struct {
	Head      string
	Branch    *ast.PBranch
	Name      string
	NameSpan  span.Span
	Owner     string
	OwnerSpan span.Span
	Fields    []fieldDecl
	Binds     []bindDecl
	// ArgList and Body are the '(params) and body forms of a fun/method,
	// resolved here so the diagnostic and semantic-token walkers locate
	// them identically (nil when the form is too short to have them).
	ArgList ast.PNode
	Body    ast.PNode
}

// declOf normalizes a top-level form into the declaration it makes, or
// ok=false if the form isn't a recognized declaration. The arity guards
// here are the single source of truth for "enough children to read this
// position"; consumers must not re-check them.
func declOf(form ast.PNode) (topLevelDecl, bool) {
	br, ok := asList(form)
	if !ok {
		return topLevelDecl{}, false
	}
	d := topLevelDecl{Head: headIdent(br), Branch: br}

	switch d.Head {
	case "fun":
		// (fun (args) body)        — anonymous: argList@1, body@2
		// (fun name (args) body)   — named:     name@1, argList@2, body@3
		switch len(br.Children) {
		case 3:
			d.ArgList, d.Body = br.Children[1], br.Children[2]
		case 4:
			if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
			d.ArgList, d.Body = br.Children[2], br.Children[3]
		}
		return d, true

	case "macro":
		// (macro name! (params) body) — the required `!` is its own leaf at
		// @2, so name@1, argList@3, body@4.
		if len(br.Children) >= 2 {
			if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		if len(br.Children) >= 5 {
			d.ArgList, d.Body = br.Children[3], br.Children[4]
		}
		return d, true

	case "method":
		// (method Receiver.Name (args) body) — the first argument is a
		// PATTERN, not code: a dot naming the owning struct (the receiver, a
		// reference) and the method (a bare identifier). Then argList@2, body@3.
		if len(br.Children) >= 2 {
			if dot, ok := br.Children[1].(*ast.PDot); ok {
				// Named: Receiver.Name.
				if owner, ok := dot.LHS.(*ast.PLeaf); ok && looksLikeIdentifier(owner.Value) {
					d.Owner, d.OwnerSpan = owner.Value, owner.Span
				}
				if name, sp, ok := declIdent(dot.RHS); ok {
					d.Name, d.NameSpan = name, sp
				}
			} else if recv, ok := br.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(recv.Value) {
				// Anonymous method `(method Receiver (args) body)` — a bare
				// receiver, no name. Used as a property get/set delegate.
				d.Owner, d.OwnerSpan = recv.Value, recv.Span
			}
		}
		if len(br.Children) >= 4 {
			d.ArgList, d.Body = br.Children[2], br.Children[3]
		}
		return d, true

	case "property":
		// (property Name get …) — Name is a free-standing declaration; or
		// (property Receiver.Name get …) — Name is a member of Receiver.
		if len(br.Children) >= 2 {
			if dot, ok := br.Children[1].(*ast.PDot); ok {
				if owner, ok := dot.LHS.(*ast.PLeaf); ok && looksLikeIdentifier(owner.Value) {
					d.Owner, d.OwnerSpan = owner.Value, owner.Span
				}
				if name, sp, ok := declIdent(dot.RHS); ok {
					d.Name, d.NameSpan = name, sp
				}
			} else if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		return d, true

	case "struct":
		// (struct Name f0 f1 …) — the name then the bare field identifiers.
		if len(br.Children) >= 2 {
			if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		for _, lf := range structFieldLeaves(br) {
			d.Fields = append(d.Fields, fieldDecl{lf.Value, lf.Span})
		}
		return d, true

	case "const", "var":
		// (var a v1 b v2 ...) — name/value pairs.
		for i := 1; i+1 < len(br.Children); i += 2 {
			if name, sp, ok := declIdent(br.Children[i]); ok {
				d.Binds = append(d.Binds, bindDecl{name, sp, br.Children[i+1]})
			}
		}
		return d, true

	case "import", "goimport":
		return d, true
	}

	return d, false
}
