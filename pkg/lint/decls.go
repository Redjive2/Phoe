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
	// Type is the field's declared type expression in the typed-field form
	// `(struct Name.{ F T … })`; nil for a bare untyped field.
	Type ast.PNode
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

// sigSite records one inline type SIGNATURE seen during collection — a `fun`
// or `method` sig form (Phase 2 of the inline type-signature plan). Name is the
// qualified name an implementation must provide ("add" for a fun, "Owner.name"
// for a method); Kind is DefFun or DefMethod. After collection,
// checkMissingImpls verifies each has a matching implementation.
type sigSite struct {
	Name string
	Span span.Span
	Kind DefKind
}

// bindName reads a var/const binding name from a name slot: a bare identifier
// `x`, or the typed form `(Type x)` — a two-element list whose second child is
// the name (the inline type-signature plan, Phase 1). The declared type is not
// returned here: Phase 1 erases it; Phase 3 will record it on the Definition.
// (Enforcing that the type slot is actually a type is Phase 2.)
func bindName(n ast.PNode) (string, span.Span, bool) {
	if name, sp, ok := declIdent(n); ok {
		return name, sp, true
	}
	if br, ok := asList(n); ok && len(br.Children) == 2 {
		return declIdent(br.Children[1])
	}
	return "", span.Span{}, false
}

// typeConnectives are the heads of the parenthesized type-FORMS — the only
// `(…)` shapes read as a type. A `(…)` with any other (capitalized) head is a
// call/construction, e.g. `(Helper)`, NOT a type. Mirrors pkg/builtins/decl.go.
var typeConnectives = map[string]bool{
	"Or": true, "And": true, "Not": true, "Diff": true,
	"List": true, "Map": true, "Fun": true, "Struct": true, "Trait": true,
}

// looksLikeTypePNode reports whether n reads as a TYPE expression rather than a
// name/value: a Capitalized leaf (Number/Self/a struct) or a type-form
// `(Or …)`/`(List …)` (a `(…)` headed by a type connective). A capitalized CALL
// like `(Helper)` is NOT a type. The casing heuristic that tells a fun/method
// SIGNATURE from its IMPLEMENTATION (TypeSignatures.md §3). Mirrors isTypeNode.
func looksLikeTypePNode(n ast.PNode) bool {
	if leaf, ok := n.(*ast.PLeaf); ok {
		v := leaf.Value
		if v == "Nil" || v == "True" || v == "False" {
			return false // capitalized VALUE literals, not types (the nil TYPE is NilT)
		}
		// A private type is `#Type_Name`; the marker doesn't affect the casing
		// test (Title_Snake_Case = type, snake_case = value).
		if len(v) > 0 && v[0] == '#' {
			v = v[1:]
		}
		return v != "" && v[0] >= 'A' && v[0] <= 'Z'
	}
	if br, ok := asList(n); ok && len(br.Children) >= 1 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			return typeConnectives[head.Value]
		}
	}
	return false
}

// isFunSigForm reports whether `(params) ret` is a fun/method type SIGNATURE:
// every element of the parenthesized param list is a type node and the return
// slot is too (an empty param list counts — the 0-arg case, §3).
func isFunSigForm(params, ret ast.PNode) bool {
	br, ok := asList(params)
	if !ok {
		return false
	}
	for _, p := range br.Children {
		if !looksLikeTypePNode(p) {
			return false
		}
	}
	// Non-empty all-type params mark this a signature, so the return is a type:
	// admit Nil/True/False as NilT/Boolean (relaxed). An empty param list stays
	// strict — `(fun f () Nil)` is a nil-returning impl, not a sig. Mirrors
	// isFunSig in pkg/builtins/decl.go.
	if len(br.Children) > 0 {
		return looksLikeReturnTypePNode(ret)
	}
	return looksLikeTypePNode(ret)
}

// looksLikeReturnTypePNode is looksLikeTypePNode relaxed for the RETURN slot of
// a form whose params already mark it a signature: Nil/True/False are admitted
// as their types (NilT / Boolean).
func looksLikeReturnTypePNode(n ast.PNode) bool {
	if leaf, ok := n.(*ast.PLeaf); ok {
		switch leaf.Value {
		case "Nil", "True", "False":
			return true
		}
	}
	return looksLikeTypePNode(n)
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
	// Sub is the inner keyword of a `(static …)` declaration: "method" or
	// "property". Empty for every other head.
	Sub string
	// IsSig marks a fun/method form recognized as a type SIGNATURE rather than
	// an implementation (`(fun add (Number Number) Number)`). Phase 1 erases it
	// — collect/check skip it so it neither binds a name nor collects its type
	// slots as params; Phase 3 reads its types into the checker. See
	// Doc/PlanV1/TypeSignatures.md §3.
	IsSig bool
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
			d.IsSig = isFunSigForm(br.Children[2], br.Children[3])
		}
		return d, true

	case "macro":
		// (macro ~name (params) body) — the required `~` prefix sigil is its
		// own leaf at @1, so name@2, argList@3, body@4.
		if len(br.Children) >= 3 {
			if name, sp, ok := declIdent(br.Children[2]); ok {
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
			// A named method whose param list is all types and whose trailing
			// slot is a type is a method SIGNATURE (the receiver type sits in
			// param 0, e.g. `(method R.M (Self) Boolean)`).
			if d.Name != "" {
				d.IsSig = isFunSigForm(br.Children[2], br.Children[3])
			}
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
		// Typed-field form `(struct Name.{ F0 T0 F1 T1 … })` parses (via the
		// `.{}` sugar) to `(struct (Name "F0" T0 "F1" T1 …))` — a single branch
		// whose head is the name and whose remaining children are alternating
		// quoted-field-name / type-expression pairs.
		if len(br.Children) >= 2 {
			if inner, ok := br.Children[1].(*ast.PBranch); ok && inner.Open == "(" && len(inner.Children) >= 1 {
				if name, sp, ok := declIdent(inner.Children[0]); ok {
					d.Name, d.NameSpan = name, sp
				}
				for i := 1; i+1 < len(inner.Children); i += 2 {
					lf, ok := inner.Children[i].(*ast.PLeaf)
					if !ok {
						continue
					}
					fname, ok := unquoteField(lf.Value)
					if !ok {
						continue
					}
					d.Fields = append(d.Fields, fieldDecl{Name: fname, Span: lf.Span, Type: inner.Children[i+1]})
				}
				return d, true
			}
			// Bare form `(struct Name f0 f1 …)` — the name then bare field idents.
			if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		for _, lf := range structFieldLeaves(br) {
			d.Fields = append(d.Fields, fieldDecl{Name: lf.Value, Span: lf.Span})
		}
		return d, true

	case "const", "var":
		// (var a v1 b v2 ...) — name/value pairs; a name may be bare `a` or
		// the typed form `(Type a)`.
		for i := 1; i+1 < len(br.Children); i += 2 {
			if name, sp, ok := bindName(br.Children[i]); ok {
				d.Binds = append(d.Binds, bindDecl{name, sp, br.Children[i+1]})
			}
		}
		return d, true

	case "type":
		// (type Name T) — Name is a bare identifier @1, T the type expr @2
		// (stored in Body so the checker can resolve the alias).
		if len(br.Children) >= 2 {
			if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		if len(br.Children) >= 3 {
			d.Body = br.Children[2]
		}
		return d, true

	case "static":
		// (static method Recv.Name (args) body) — Sub="method", ArgList@3/Body@4.
		// (static property Recv.Name get …)      — Sub="property".
		// The Recv.Name dot at child 2 is a PATTERN (owner + member name being
		// declared), parsed like a method's receiver.
		if len(br.Children) >= 2 {
			if kw, _, ok := declIdent(br.Children[1]); ok {
				d.Sub = kw
			}
		}
		if len(br.Children) >= 3 {
			if dot, ok := br.Children[2].(*ast.PDot); ok {
				if owner, ok := dot.LHS.(*ast.PLeaf); ok && looksLikeIdentifier(owner.Value) {
					d.Owner, d.OwnerSpan = owner.Value, owner.Span
				}
				if name, sp, ok := declIdent(dot.RHS); ok {
					d.Name, d.NameSpan = name, sp
				}
			}
		}
		if d.Sub == "method" && len(br.Children) >= 5 {
			d.ArgList, d.Body = br.Children[3], br.Children[4]
		}
		return d, true

	case "trait":
		// (trait Name [(extends…)] member…) — Name is a bare identifier @1; the
		// extends-list/members are handled by resolveTraitNode/checkTrait.
		if len(br.Children) >= 2 {
			if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		return d, true

	case "import", "goimport":
		return d, true
	}

	return d, false
}
