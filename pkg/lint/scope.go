package lint

import (
	"regexp"

	"pho/pkg/core"
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
	DefMethod
	DefStruct
	DefParam
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
	case DefMethod:
		return "method"
	case DefStruct:
		return "struct"
	case DefParam:
		return "parameter"
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
type Definition struct {
	Name string
	Kind DefKind
	Span core.Span
	Path string
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
}

func newScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Defs: map[string]Definition{}}
}

// Define adds a binding to the current scope. Returns the prior
// definition (if any) and whether one existed — useful for emitting
// redeclaration diagnostics without doing a separate Lookup.
func (s *Scope) Define(name string, kind DefKind, span core.Span) (Definition, bool) {
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
	"+", "-", "*", "/",
	"==", "~=", "<=", ">=", "<", ">",
	// Boolean.
	"~", "and", "or",
	// Control flow.
	"if", "for", "do", "return", "break", "continue",
	// Declarations / bindings.
	"fun", "method", "struct", "var", "const", "=", "block",
	// Collections.
	"slice", "map", "get", "has", "len", "append", "drop", "range",
	// Meta / code-as-data.
	"pause", "resume", "inspect", "spread",
	// Module imports.
	"import", "goimport",
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
var identRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

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

// quotedIdent returns the identifier wrapped by `'name`, or "" if the
// node isn't a quote of a bare identifier.
func quotedIdent(n core.PNode) (string, core.Span, bool) {
	sig, ok := n.(*core.PSigil)
	if !ok || sig.Sigil != "'" {
		return "", core.Span{}, false
	}
	leaf, ok := sig.Inner.(*core.PLeaf)
	if !ok {
		return "", core.Span{}, false
	}
	if !looksLikeIdentifier(leaf.Value) {
		return "", core.Span{}, false
	}
	return leaf.Value, leaf.Span, true
}

// quotedList returns the children inside `'(...)`, or nil if n isn't a
// quoted parenthesized form.
func quotedList(n core.PNode) ([]core.PNode, bool) {
	sig, ok := n.(*core.PSigil)
	if !ok || sig.Sigil != "'" {
		return nil, false
	}
	br, ok := sig.Inner.(*core.PBranch)
	if !ok || br.Open != "(" {
		return nil, false
	}
	return br.Children, true
}

// stringLiteral returns the string value of a string-literal leaf, with
// the surrounding quotes stripped, or ("", false) if n isn't a string.
func stringLiteral(n core.PNode) (string, bool) {
	leaf, ok := n.(*core.PLeaf)
	if !ok {
		return "", false
	}
	v := leaf.Value
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", false
	}
	return v[1 : len(v)-1], true
}
