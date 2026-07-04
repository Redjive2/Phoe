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
	// `(struct Name.{ T F … })`; nil for a bare untyped field.
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
	Name    string
	Span    span.Span
	Value   ast.PNode // the segment's RHS (nil on the extra binders of a destructure)
	Mutable bool      // written `(var …)` — reassignable even under a plain `let`
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

// isEqMarker reports whether n is the bare `=` structural marker separating a
// binding's target from its value in a `let` form.
func isEqMarker(n ast.PNode) bool {
	lf, ok := n.(*ast.PLeaf)
	return ok && lf.Value == "="
}

// letBinding reads one binding segment of a `let` form at children[i]:
// `target = value`, where the target is a name, a typed `(Type name)`, or a
// destructuring pattern (`[a b …]`, `Type.{ … }`). Returns the target slot, the
// value node, and the index just past the segment. ok=false when the layout is
// malformed (no `=` at i+1). (The retired ungrouped `Type name = value` form —
// two bare leaves before `=` — is no longer read here; the shape checker flags
// it and points at the grouped `(Type name)` replacement.)
func letBinding(children []ast.PNode, i int) (targetNode, valueNode ast.PNode, next int, ok bool) {
	if i+2 < len(children) && isEqMarker(children[i+1]) {
		return children[i], children[i+2], i + 3, true
	}
	return nil, nil, i, false
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
			return false // capitalized VALUE literals, not types (the nil TYPE is None)
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
			// A function TYPE is written `(fun (P…) R)` — the same head as a
			// function value, but in a type position (a sig param). It reads as a
			// type, so a method whose receiver-or-param is a function type is a
			// SIGNATURE, not an impl with a `(fun …)`-shaped param name.
			return typeConnectives[head.Value] || head.Value == "fun"
		}
	}
	return false
}

// looksLikeSigParam reports whether a signature param slot is a type — either a
// bare type node, or a `(var/spread/optional/const <type>)` modifier wrapping
// one, so a method signature can declare a mutable `(var Self)` receiver, a
// variadic `(spread T)` / optional `(optional T)` param, a defaulted optional
// `(optional T else DEFAULT)`, or a parse-time-constant `(const T)` slot
// (Features.md §1). The inner must still be a type (capitalized), so an
// implementation's lowercase `(var self)`/`(spread xs)` param is NOT mistaken
// for a signature. Mirrors isSigParamNode in pkg/builtins/decl.go.
func looksLikeSigParam(p ast.PNode) bool {
	if br, ok := asList(p); ok && len(br.Children) >= 2 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			switch head.Value {
			case "var", "spread", "const", "disc":
				return len(br.Children) == 2 && looksLikeTypePNode(br.Children[1])
			case "optional":
				if len(br.Children) == 2 {
					return looksLikeTypePNode(br.Children[1])
				}
				// (optional Type else DEFAULT)
				if len(br.Children) == 4 {
					kw, ok := br.Children[2].(*ast.PLeaf)
					return ok && kw.Value == "else" && looksLikeTypePNode(br.Children[1])
				}
				return false
			}
		}
	}
	return looksLikeTypePNode(p)
}

// sigParamType unwraps a signature param slot to its TYPE node: the inner type
// of a `(var/spread/optional/const T)` modifier (including the defaulted
// `(optional T else D)`), or the node itself when unwrapped. Every sig-param
// reader goes through this so the modifiers stay invisible to type plumbing.
func sigParamType(p ast.PNode) ast.PNode {
	if br, ok := asList(p); ok && len(br.Children) >= 2 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			switch head.Value {
			case "var", "spread", "optional", "const", "disc":
				return br.Children[1]
			}
		}
	}
	return p
}

// arrowSplit splits the flat tail of a signature (the nodes after the callable
// name) into its parameter nodes and the single return-type node, on the
// top-level `->` marker: (fun add Number Number -> Number) → [Number Number],
// Number; (fun cwd -> String) → [], String. ok=false when there's no top-level
// `->` followed by exactly one return node. Mirrors splitArrow in builtins.
func arrowSplit(nodes []ast.PNode) (params []ast.PNode, ret ast.PNode, ok bool) {
	for i, n := range nodes {
		if lf, isLeaf := n.(*ast.PLeaf); isLeaf && lf.Value == "->" {
			if i != len(nodes)-2 {
				return nil, nil, false
			}
			return nodes[:i], nodes[i+1], true
		}
	}
	return nil, nil, false
}

// synthParamList wraps flat parameter nodes in a synthetic `(…)` branch so every
// consumer that reads a decl's ArgList as a PBranch (funSignatureIndex,
// paramTypeFor, emitSigTypes, walkFunctionLike, …) keeps working now that the
// parameter list was flattened out of the surface syntax. The span covers the
// parameters (or falls back when there are none).
func synthParamList(params []ast.PNode, fallback span.Span) *ast.PBranch {
	sp := fallback
	if len(params) > 0 {
		a, b := params[0].GetSpan(), params[len(params)-1].GetSpan()
		sp = span.Span{StartLine: a.StartLine, StartCol: a.StartCol, EndLine: b.EndLine, EndCol: b.EndCol}
	}
	return &ast.PBranch{Open: "(", Children: params, Span: sp}
}

// isFlatSig reports whether a flat parameter sequence + return node reads as a
// fun/method type SIGNATURE: every parameter is a type node and the return is a
// real TYPE. Mirrors isFunSig in pkg/builtins/decl.go.
func isFlatSig(params []ast.PNode, ret ast.PNode) bool {
	for _, p := range params {
		if !looksLikeSigParam(p) {
			return false
		}
	}
	return looksLikeTypePNode(ret)
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
	// PropType is the declared value type of a TYPED property
	// `(property (Type name) …)` — nil for an untyped property. Erased at
	// runtime; read by the checker. See Doc/PlanV1/TypeSignatures.md.
	PropType ast.PNode
	// IsSig marks a fun/method form recognized as a type SIGNATURE rather than
	// an implementation (`(fun add (Number Number) Number)`). Phase 1 erases it
	// — collect/check skip it so it neither binds a name nor collects its type
	// slots as params; Phase 3 reads its types into the checker. See
	// Doc/PlanV1/TypeSignatures.md §3.
	IsSig bool
	// IsClause marks a `(let name (patterns) [where guard] = body)`
	// implementation CLAUSE (normalized to Head fun/method). Guard is its
	// `where` expression, nil when unguarded. (Features.md §1/§2.)
	IsClause bool
	Guard    ast.PNode
	// ArgList and Body are the '(params) and body forms of a fun/method,
	// resolved here so the diagnostic and semantic-token walkers locate
	// them identically (nil when the form is too short to have them).
	ArgList ast.PNode
	Body    ast.PNode
	// TemplateParams holds the type-parameter names of a `(template …)` form
	// (the bound names of bounded params), empty for every other head. Phase 1
	// recognizes them as gradual type names; later phases bind and instantiate.
	TemplateParams []templateParam
}

// templateParam is one type parameter of a `(template …)` form: its name and
// span, plus the optional bound expression (`(Bound P)` → Bound), nil when
// unbound. Phase 1 records the bound but does not enforce it.
type templateParam struct {
	Name  string
	Span  span.Span
	Bound ast.PNode
}

// templateParams extracts the type parameters of a `(template …)` branch. An
// unbound parameter is a bare leaf `P`; a bounded one is `(Bound P)`, where the
// parameter is the LAST element and the bound is everything before it.
func templateParams(br *ast.PBranch) []templateParam {
	var out []templateParam
	for _, c := range br.Children[1:] {
		switch x := c.(type) {
		case *ast.PLeaf:
			out = append(out, templateParam{Name: x.Value, Span: x.Span})
		case *ast.PBranch:
			if n := len(x.Children); n >= 2 {
				if lf, ok := x.Children[n-1].(*ast.PLeaf); ok {
					out = append(out, templateParam{Name: lf.Value, Span: lf.Span, Bound: x.Children[n-2]})
				}
			}
		}
	}
	return out
}

// isVarLeaf reports whether n is the bare `var` modifier leaf.
func isVarLeaf(n ast.PNode) bool {
	lf, ok := n.(*ast.PLeaf)
	return ok && lf.Value == "var"
}

// overloadableOperatorNames are the operator names a type may overload
// (Features.md §7). Mirrors pkg/builtins overloadableOperators. `[]` / `[]=`
// arrive here as synthetic leaves after desugarOperatorChildren.
var overloadableOperatorNames = map[string]bool{
	"+": true, "-": true, "*": true, "/": true, "mod": true,
	"<": true, "<=": true, ">": true, ">=": true, "==": true, "~=": true,
	"[]": true, "[]=": true,
}

// operatorName returns the operator a `Recv.OP` target's RHS names, when it is an
// overloadable operator leaf (`+`, `<`, or a canonicalized `[]`/`[]=`).
func operatorName(rhs ast.PNode) (string, span.Span, bool) {
	if leaf, ok := rhs.(*ast.PLeaf); ok && overloadableOperatorNames[leaf.Value] {
		return leaf.Value, leaf.Span, true
	}
	return "", span.Span{}, false
}

// desugarOperatorChildren canonicalizes an index-operator target in a top-level
// `operator`/`let` form so the rest of declOf sees a plain `Recv.opname` PDot
// (Features.md §7). Mirrors pkg/builtins desugarOperatorTarget: `Recv.[]` (an
// empty-`[`-bracket RHS) becomes `Recv."[]"`; `Recv.[]=` (that target plus a bare
// `=` sibling) becomes `Recv."[]="` with the `=` removed. Returns (children,
// false) unchanged for every other form.
func desugarOperatorChildren(children []ast.PNode) ([]ast.PNode, bool) {
	if len(children) < 2 {
		return children, false
	}
	dot, ok := children[1].(*ast.PDot)
	if !ok {
		return children, false
	}
	rhs, ok := dot.RHS.(*ast.PBranch)
	if !ok || rhs.Open != "[" || len(rhs.Children) != 0 {
		return children, false
	}
	name := "[]"
	rest := children[2:]
	if len(children) >= 3 {
		if eq, ok := children[2].(*ast.PLeaf); ok && eq.Value == "=" {
			name, rest = "[]=", children[3:]
		}
	}
	newDot := &ast.PDot{LHS: dot.LHS, RHS: &ast.PLeaf{Value: name, Span: rhs.Span}, Span: dot.Span}
	return append([]ast.PNode{children[0], newDot}, rest...), true
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

	// Operator overloading (Features.md §7): canonicalize an index-operator
	// target (`Recv.[]` / `Recv.[]=`) so both the `operator` sig and the `let`
	// impl clause read a plain `Recv.opname` PDot below.
	if d.Head == "operator" || d.Head == "let" {
		if canon, changed := desugarOperatorChildren(br.Children); changed {
			br = &ast.PBranch{Open: br.Open, Children: canon, Span: br.Span}
			d.Branch = br
		}
	}

	switch d.Head {
	case "fun":
		// (fun name Type… -> Result) — a type SIGNATURE: flat parameter TYPES,
		// then `->`, then the return type. name@1; params and return split on the
		// top-level `->`. `fun` is signature-only now (impls are `let` clauses).
		if len(br.Children) >= 2 {
			if name, sp, ok := declIdent(br.Children[1]); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		if params, ret, ok := arrowSplit(br.Children[2:]); ok {
			d.ArgList = synthParamList(params, d.NameSpan)
			d.Body = ret
			d.IsSig = true
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
		// (method Receiver.Name Self Type… -> Result) — a method type SIGNATURE.
		// The first argument is a PATTERN, not code: a dot naming the owning
		// struct (the receiver, a reference) and the method (a bare identifier).
		// The receiver + parameter TYPES are flat, split from the return on `->`.
		if len(br.Children) >= 2 {
			if dot, ok := br.Children[1].(*ast.PDot); ok {
				if owner, ok := dot.LHS.(*ast.PLeaf); ok && looksLikeIdentifier(owner.Value) {
					d.Owner, d.OwnerSpan = owner.Value, owner.Span
				}
				if name, sp, ok := declIdent(dot.RHS); ok {
					d.Name, d.NameSpan = name, sp
				}
			}
		}
		if params, ret, ok := arrowSplit(br.Children[2:]); ok && d.Name != "" {
			d.ArgList = synthParamList(params, d.NameSpan)
			d.Body = ret
			d.IsSig = true
		}
		return d, true

	case "operator":
		// (operator Receiver.OP Self Type… -> Result) — an operator overload
		// SIGNATURE (Features.md §7): structurally a method sig whose name is an
		// operator symbol (`+`, `<`, `[]`, `[]=`, …). Normalized to Head "method"
		// so the adjacent `(let Receiver.OP …)` clauses associate and it
		// type-checks like any method sig. Index targets were canonicalized at
		// the top of declOf (`Receiver."[]"` / `Receiver."[]="`).
		d.Head = "method"
		if len(br.Children) >= 2 {
			if dot, ok := br.Children[1].(*ast.PDot); ok {
				if owner, ok := dot.LHS.(*ast.PLeaf); ok && looksLikeIdentifier(owner.Value) {
					d.Owner, d.OwnerSpan = owner.Value, owner.Span
				}
				if name, sp, ok := operatorName(dot.RHS); ok {
					d.Name, d.NameSpan = name, sp
				}
			}
		}
		if params, ret, ok := arrowSplit(br.Children[2:]); ok && d.Name != "" {
			d.ArgList = synthParamList(params, d.NameSpan)
			d.Body = ret
			d.IsSig = true
		}
		return d, true

	case "=":
		// `=` is REASSIGNMENT only — `(= target value)`, never a declaration
		// (returns ok=false). The old define form `(= name (params) body)` is
		// retired: an implementation is a `let` clause `(let name (params) = body)`
		// (see the "let" case). A 3-arg `=` is not recognized here; the shape
		// checker flags its arity.
		return d, false

	case "property":
		// (property Name (get …)) — Name is a free-standing declaration; or
		// (property Receiver.Name (get …)) — Name is a member of Receiver; or the
		// TYPED form (property (Type Name) …) / (property (Type Receiver.Name) …)
		// — a `(Type target)` wrapper carrying the property's value type, which
		// the checker reads and Phase 1 otherwise erases.
		if len(br.Children) >= 2 {
			nameSlot := br.Children[1]
			if inner, ok := asList(nameSlot); ok && len(inner.Children) == 2 {
				d.PropType = inner.Children[0]
				nameSlot = inner.Children[1]
			}
			if dot, ok := nameSlot.(*ast.PDot); ok {
				if owner, ok := dot.LHS.(*ast.PLeaf); ok && looksLikeIdentifier(owner.Value) {
					d.Owner, d.OwnerSpan = owner.Value, owner.Span
				}
				if name, sp, ok := declIdent(dot.RHS); ok {
					d.Name, d.NameSpan = name, sp
				}
			} else if name, sp, ok := declIdent(nameSlot); ok {
				d.Name, d.NameSpan = name, sp
			}
		}
		return d, true

	case "template":
		// (template P (Bound Q) …) — declares type parameters scoped to the
		// following declaration: a bare leaf is an unbound parameter, `(Bound P)`
		// a bounded one (P constrained by Bound). Phase 1 recognizes the form so
		// it isn't mis-read as a call, and treats the parameters as gradual;
		// instantiation and bound-enforcement are later phases.
		d.TemplateParams = templateParams(br)
		return d, true

	case "struct":
		// Typed-field form `(struct Name.{ T0 F0 T1 F1 … })` parses (via the
		// `.{}` sugar) to `(struct (Name T0 "F0" T1 "F1" …))` — a single branch
		// whose head is the name and whose remaining children are alternating
		// type-expression / quoted-field-name pairs (Type name).
		if len(br.Children) >= 2 {
			if inner, ok := br.Children[1].(*ast.PBranch); ok && inner.Open == "(" && len(inner.Children) >= 1 {
				if name, sp, ok := declIdent(inner.Children[0]); ok {
					d.Name, d.NameSpan = name, sp
				}
				for i := 1; i+1 < len(inner.Children); i += 2 {
					lf, ok := inner.Children[i+1].(*ast.PLeaf)
					if !ok {
						continue
					}
					fname, ok := unquoteField(lf.Value)
					if !ok {
						continue
					}
					d.Fields = append(d.Fields, fieldDecl{Name: fname, Span: lf.Span, Type: inner.Children[i]})
				}
				return d, true
			}
			// Generic typed-field form `(struct Name { T0 f0 T1 f1 … })` — a `{}`
			// brace of alternating Type / field-name pairs (Phase 1 generics). The
			// field name is the SECOND of each pair; its type (first) may name a
			// template type parameter, which Phase 1 resolves gradually.
			if brace, ok := br.Children[len(br.Children)-1].(*ast.PBranch); ok && brace.Open == "{" && len(br.Children) == 3 {
				if name, sp, ok := declIdent(br.Children[1]); ok {
					d.Name, d.NameSpan = name, sp
				}
				for i := 0; i+1 < len(brace.Children); i += 2 {
					if lf, ok := brace.Children[i+1].(*ast.PLeaf); ok {
						d.Fields = append(d.Fields, fieldDecl{Name: lf.Value, Span: lf.Span, Type: brace.Children[i]})
					}
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
				d.Binds = append(d.Binds, bindDecl{Name: name, Span: sp, Value: br.Children[i+1]})
			}
		}
		return d, true

	case "let":
		// (let [Owner.]name (patterns) [where guard] = body) — an
		// IMPLEMENTATION CLAUSE (Features.md §1/§2): normalize to Head
		// fun/method like the retired `=` impl form, carrying the guard.
		// Recognized by shape: params list at @2 and `=`/`where` right after.
		// A `var` target is NOT a clause — `(let var (Type x) = v)` is a typed
		// MUTABLE BINDING whose `(Type x)` branch happens to sit where a
		// clause's param list would (`var` is a keyword, never a fun name).
		// A clause target is a value NAME (a free function) or an `Owner.name`
		// dot (a method/operator) — never `var` and never a grouped `(Type name)`
		// or destructuring pattern (those are value bindings). The slot after the
		// name must be a real parameter, not the `=`/`where` marker (that shape,
		// (let name = body), is a value binding — or a 0-arg clause, reconciled by
		// the collector against the signature table, not here).
		clauseTarget := false
		switch t := br.Children[1].(type) {
		case *ast.PLeaf:
			clauseTarget = looksLikeIdentifier(t.Value) && !isVarLeaf(t) && !looksLikeTypePNode(t)
		case *ast.PDot:
			clauseTarget = true
		}
		if clauseTarget && len(br.Children) >= 4 {
			if lf, ok := br.Children[2].(*ast.PLeaf); ok && (lf.Value == "=" || lf.Value == "where") {
				clauseTarget = false // 0 parameters — value-binding shape
			}
		}
		if clauseTarget {
			// Split flat parameters from the body on the top-level `=`, with an
			// optional `where guard` just before it.
			tail := br.Children[2:]
			eqIdx, whereIdx := -1, -1
			for i, n := range tail {
				if lf, ok := n.(*ast.PLeaf); ok {
					if lf.Value == "where" && whereIdx < 0 {
						whereIdx = i
					}
					if lf.Value == "=" {
						eqIdx = i
						break
					}
				}
			}
			if eqIdx >= 0 {
				d.IsClause = true
				paramEnd := eqIdx
				if whereIdx >= 0 && whereIdx < eqIdx {
					d.Guard = tail[whereIdx+1]
					paramEnd = whereIdx
				}
				d.ArgList = synthParamList(tail[:paramEnd], d.NameSpan)
				if eqIdx+1 < len(tail) {
					d.Body = tail[eqIdx+1]
				}
				switch target := br.Children[1].(type) {
				case *ast.PLeaf:
					d.Head = "fun"
					d.Name, d.NameSpan = target.Value, target.Span
					return d, true
				case *ast.PDot:
					if owner, ok := target.LHS.(*ast.PLeaf); ok && looksLikeIdentifier(owner.Value) {
						name, sp, ok := declIdent(target.RHS)
						if !ok {
							name, sp, ok = operatorName(target.RHS)
						}
						if ok {
							d.Head = "method"
							d.Owner, d.OwnerSpan = owner.Value, owner.Span
							d.Name, d.NameSpan = name, sp
							return d, true
						}
					}
				}
				// Malformed target — fall through to the value-binding reading.
				d.ArgList, d.Body, d.Guard, d.IsClause = nil, nil, nil, false
			}
		}

		// (let [var] [Type] name = value  …) — the first-class declaration form.
		// Normalize to the const/var shape the rest of the linter already
		// consumes: `let` → const (immutable), `let var` → var (mutable). Each
		// binding is `name = value` (untyped) or `Type name = value` (ungrouped
		// typed, the leading type read by the checker); the `=` markers are
		// structural and dropped. d.Branch keeps the original `let` form, so hover
		// and document-symbols render the surface syntax.
		i := 1
		d.Head = "const"
		if i < len(br.Children) {
			if mod, ok := br.Children[i].(*ast.PLeaf); ok && mod.Value == "var" {
				d.Head = "var"
				i++
			}
		}
		for i < len(br.Children) {
			targetNode, valueNode, next, ok := letBinding(br.Children, i)
			if !ok {
				break // malformed; the shape checker reports it
			}
			// A simple target (a name / (Type name) / (var name)) IS the whole
			// value, so its one binder carries the RHS for type inference. A
			// DESTRUCTURING target's binders are PARTS of the value, not the
			// value, so they carry no RHS (else each would wrongly infer the
			// whole value's type). The RHS is still evaluated at runtime.
			binders := letTargetBinders(targetNode, true, false)
			destructure := letTargetIsDestructure(targetNode)
			for j, b := range binders {
				val := ast.PNode(nil)
				if !destructure && j == 0 {
					val = valueNode
				}
				d.Binds = append(d.Binds, bindDecl{Name: b.name, Span: b.span, Value: val, Mutable: b.mutable})
			}
			i = next
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
		// (static method Recv.Name (args) body)        — Sub="method", ArgList@3/Body@4.
		// (static property Recv.Name (get …) [(set …)]) — Sub="property".
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
		if d.Sub == "method" {
			// A static method SIGNATURE — e.g.
			// `(static method File.open! String String (optional Atom) -> File)`.
			// The receiver TYPE (`Self`) is implicit, so every flat slot before the
			// `->` is a real argument type. The implementation is a
			// `(let Recv.Name params… = body)` clause.
			if params, ret, ok := arrowSplit(br.Children[3:]); ok && d.Name != "" {
				d.ArgList = synthParamList(params, d.NameSpan)
				d.Body = ret
				d.IsSig = true
			}
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
