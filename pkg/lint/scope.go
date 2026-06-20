package lint

import (
	"regexp"

	"pho/pkg/ast"
	"pho/pkg/span"
)

// DefKind classifies a binding for diagnostic purposes — set-on-constant
// only fires for DefConst, redeclaration messages reference the kind, etc.
type DefKind int

const (
	DefBuiltin DefKind = iota
	DefImport
	DefConst
	DefVar
	DefFun
	DefMacro
	DefMethod
	DefStruct
	DefParam
	// DefField is never installed in a scope — struct fields aren't
	// bindable names. It exists so dot-completion and hover can label
	// struct-field results distinctly from variables.
	DefField
)

func (k DefKind) String() string {
	switch k {
	case DefBuiltin:
		return "builtin"
	case DefImport:
		return "import"
	case DefConst:
		return "const"
	case DefVar:
		return "var"
	case DefFun:
		return "function"
	case DefMacro:
		return "macro"
	case DefMethod:
		return "method"
	case DefStruct:
		return "struct"
	case DefParam:
		return "parameter"
	case DefField:
		return "field"
	}
	return "unknown"
}

// Definition is one entry in a Scope. Span points at the declaration
// site; for builtins it's the zero Span.
//
// Path is set only for DefImport entries — it's the resolved import
// path (the string passed to `import`), used by the dot-access check
// to find the imported package's exports on disk. Empty for every
// other kind.
//
// File is the path of the file the definition appears in — needed when
// a definition came from a package sibling or an imported package and
// navigation wants to jump there. Empty for builtins (and for defs in
// the file currently being analyzed, where the caller already knows
// the path).
//
// Shape is the statically inferred value shape for var/const/param
// bindings — see infer.go. Zero (ShapeUnknown) whenever inference
// can't make a confident call.
type Definition struct {
	Name  string
	Kind  DefKind
	Span  span.Span
	Path  string
	File  string
	Shape Shape
}

// Scope is a single lexical scope with a parent pointer for chained
// lookup. The chain is:
//
//	builtin → package (sibling-file decls) → file → body...
//
// IsPackage marks the package scope so the redeclaration checker can
// distinguish "this name shadows a const in an enclosing block" (a
// real shadow worth flagging) from "this name was also declared in a
// sibling file in the same package" (a cross-file collision the
// runtime will catch at load time, not the linter's job).
type Scope struct {
	Parent    *Scope
	Defs      map[string]Definition
	IsPackage bool

	// Structs maps struct name → field/method tables for structs
	// declared at this scope's level (file scope for same-file structs,
	// package scope for siblings). Lazily created; looked up through
	// the parent chain via LookupStruct.
	Structs map[string]*structInfo
}

func newScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Defs: map[string]Definition{}}
}

// LookupStruct walks the scope chain for a struct's field/method
// tables.
func (s *Scope) LookupStruct(name string) (*structInfo, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if cur.Structs != nil {
			if si, ok := cur.Structs[name]; ok {
				return si, true
			}
		}
	}
	return nil, false
}

// structAt returns (creating if needed) the structInfo for `name` in
// this scope. Methods can be collected before their struct declaration
// is seen, so creation is on-demand.
func (s *Scope) structAt(name string) *structInfo {
	if s.Structs == nil {
		s.Structs = map[string]*structInfo{}
	}
	si, ok := s.Structs[name]
	if !ok {
		si = &structInfo{
			Name:    name,
			Fields:  map[string]span.Span{},
			Methods: map[string]span.Span{},
		}
		s.Structs[name] = si
	}
	return si
}

// Define adds a binding to the current scope. Returns the prior
// definition (if any) and whether one existed — useful for emitting
// redeclaration diagnostics without doing a separate Lookup.
func (s *Scope) Define(name string, kind DefKind, span span.Span) (Definition, bool) {
	prior, existed := s.Defs[name]
	s.Defs[name] = Definition{Name: name, Kind: kind, Span: span}
	return prior, existed
}

// Lookup walks the scope chain. Returns the definition, the scope it
// was found in, and whether it was found.
func (s *Scope) Lookup(name string) (Definition, *Scope, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if d, ok := cur.Defs[name]; ok {
			return d, cur, true
		}
	}
	return Definition{}, nil, false
}

// ----------------------------------------------------------------------
// Builtin scope
// ----------------------------------------------------------------------

// builtinNames lists every name predeclared by the runtime — every entry
// in builtins.NewEnv plus the Pho-side atoms (True/False/Nil) that the
// leaf evaluator recognizes specially. Used to seed the root scope so
// references to builtins don't trip the unresolved-identifier checker.
var builtinNames = []string{
	// Arithmetic / comparison.
	"+", "-", "*", "/", "mod",
	"==", "~=", "<=", ">=", "<", ">",
	// Boolean.
	"~", "and", "or",
	// Control flow.
	"if", "unless", "foreach", "while", "until", "do", "return", "break", "continue",
	// Declarations / bindings.
	"fun", "macro", "method", "struct", "property", "var", "const", "=", "block",
	// Collections.
	"slice", "map", "get", "has", "len", "append", "drop", "range", "keyof", "list?",
	// Atoms.
	"atom?", "atom", "atomName",
	// Meta / code-as-data.
	"pause", "resume", "inspect", "identity", "spread", "optional",
	// Module imports.
	"import", "goimport",
	// Type system: first-class type values + runtime type operations.
	"Number", "String", "List", "Dict", "Boolean", "Char", "Atom", "Function",
	"NilT", "Type", "Unknown", "None",
	"typeof", "Is?", "subtype?",
	// Atoms recognized by the leaf evaluator.
	"True", "False", "Nil",
	// Soft keyword: `self` is the conventional method-receiver name.
	// The runtime doesn't bind `self` specially — it's whatever the
	// first parameter of a method happens to be called — but treating
	// it as predeclared keeps the linter from flagging legitimate
	// receiver references and lets us highlight it consistently.
	"self",
}

// newBuiltinScope returns a fresh scope pre-populated with every
// builtin name. Each definition has a zero Span (no source location).
func newBuiltinScope() *Scope {
	s := newScope(nil)
	for _, n := range builtinNames {
		s.Defs[n] = Definition{Name: n, Kind: DefBuiltin}
	}
	return s
}

// ----------------------------------------------------------------------
// Identifier classification
// ----------------------------------------------------------------------

// identRe matches the same identifier syntax the runtime's leaf
// evaluator recognizes. We use it to distinguish "this leaf names
// something" from "this leaf is punctuation / an operator / a literal".
// A single optional trailing '?' is allowed (the predicate convention).
var identRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*\??$`)

// looksLikeIdentifier reports whether a leaf value should be treated as
// a name for resolution purposes. Operators are reachable via Resolve
// at runtime but are seeded into the builtin scope, so they pass.
func looksLikeIdentifier(v string) bool {
	if identRe.MatchString(v) {
		return true
	}
	// Symbol operators — present in the builtin scope, treated as refs.
	for _, op := range []string{
		"+", "-", "*", "/", "==", "~=", "<=", ">=", "<", ">", "~", "=",
	} {
		if v == op {
			return true
		}
	}
	return false
}

// declIdent returns the identifier in a declaration NAME position —
// fun/method/struct name, var/const name, for loop variable, = target.
// Post-cutover these are bare leaves: `(fun add …)`, never `(fun 'add …)`.
// A non-leaf (a quote, a list, a computed expression) returns ok=false; the
// shape checker reports it. Distinct from quotedIdent, which stays for the
// VALUE positions that still carry a quote (member keys, import aliases).
func declIdent(n ast.PNode) (string, span.Span, bool) {
	leaf, ok := n.(*ast.PLeaf)
	if !ok || !looksLikeIdentifier(leaf.Value) {
		return "", span.Span{}, false
	}
	return leaf.Value, leaf.Span, true
}

// unquoteForm strips a single leading `'` quote and returns the inner node.
// It bridges the de-sigiling migration: a fun/method parameter list or body
// may still be written in the legacy quoted style `'(...)`. The quote is only
// syntax — the runtime unwraps it and runs the body as code either way — so
// the analysis call sites (param collection, body walking) unquote it to
// resolve the names inside. The shape checker is deliberately NOT one of them:
// it sees the raw form so it can still flag the un-migrated quote. A
// non-quoted node is returned unchanged.
func unquoteForm(n ast.PNode) ast.PNode {
	if sig, ok := n.(*ast.PSigil); ok && sig.Sigil == "'" {
		return sig.Inner
	}
	return n
}

// declList returns the children of a bare parameter or field list `(a b …)`,
// or nil if n isn't a bare parenthesized form. It deliberately rejects the
// legacy quoted form `'(a b …)`: the shape checker (expectQuotedList) relies
// on that rejection to flag an un-migrated decl with bad-form-shape. Call
// sites that need to ANALYZE an old-style list (resolving its names rather
// than judging its shape) unquote it first with unquoteForm.
func declList(n ast.PNode) ([]ast.PNode, bool) {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "(" {
		return nil, false
	}
	return br.Children, true
}

// quotedIdent returns the identifier wrapped by `'name`, or "" if the
// node isn't a quote of a bare identifier.
func quotedIdent(n ast.PNode) (string, span.Span, bool) {
	sig, ok := n.(*ast.PSigil)
	if !ok || sig.Sigil != "'" {
		return "", span.Span{}, false
	}
	leaf, ok := sig.Inner.(*ast.PLeaf)
	if !ok {
		return "", span.Span{}, false
	}
	if !looksLikeIdentifier(leaf.Value) {
		return "", span.Span{}, false
	}
	return leaf.Value, leaf.Span, true
}

// quotedList returns the children inside `'(...)`, or nil if n isn't a
// quoted parenthesized form.
func quotedList(n ast.PNode) ([]ast.PNode, bool) {
	sig, ok := n.(*ast.PSigil)
	if !ok || sig.Sigil != "'" {
		return nil, false
	}
	br, ok := sig.Inner.(*ast.PBranch)
	if !ok || br.Open != "(" {
		return nil, false
	}
	return br.Children, true
}

// stringLiteral returns the string value of a string-literal leaf, with
// the surrounding quotes stripped, or ("", false) if n isn't a string.
func stringLiteral(n ast.PNode) (string, bool) {
	leaf, ok := n.(*ast.PLeaf)
	if !ok {
		return "", false
	}
	v := leaf.Value
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", false
	}
	return v[1 : len(v)-1], true
}
