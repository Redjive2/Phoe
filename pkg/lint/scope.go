package lint

import (
	"regexp"

	"pho/pkg/ast"
	"pho/pkg/core"
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
	// DefType is a named type alias `(type Name T)` — a constant KindType
	// binding, but labelled distinctly so hover/diagnostics say "type".
	DefType
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
	case DefType:
		return "type"
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
	"not", "and", "or",
	// Control flow.
	"if", "unless", "foreach", "while", "until", "do", "return", "break", "continue",
	// Declarations / bindings.
	"fun", "macro", "method", "struct", "property", "static", "trait", "var", "const", "type", "=", "block",
	// Collections. (slice/map are intentionally absent: they are mangled —
	// `[…]`/`{…}` are the only surface forms — so a bare `(slice …)`/`(map …)`
	// is an unresolved call, not a builtin.)
	"get", "has", "append", "drop", "range", "list?",
	// Atoms.
	"atom?", "atom", "atom_name",
	// Meta / code-as-data.
	"inspect", "identity", "spread", "optional",
	// Module imports.
	"import", "goimport",
	// Type system: first-class type values + runtime type operations.
	"Number", "String", "List", "Map", "Boolean", "Char", "Atom", "Function",
	"NilT", "Type", "Unknown", "None", "Collection", "Dynamic",
	// NB: no "Is?" — membership is the universal method (x.Is? T) only,
	// resolved via the type-member surface (typemembers.go), not a builtin.
	"subtype?", "Or", "And", "Not", "Diff", "Fun", "Struct", "Trait",
	// Atoms recognized by the leaf evaluator.
	"True", "False", "Nil",
	// New spellings; capitalized forms accepted during the migration.
	"none", "true", "false",
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
// A single optional leading '#' (the private marker) and trailing '?' (the
// predicate convention) are allowed (Doc/PlanV1/Syntax.md).
var identRe = regexp.MustCompile(`^#?[A-Za-z][A-Za-z0-9_]*\??$`)

// looksLikeIdentifier reports whether a leaf value should be treated as
// a name for resolution purposes. Operators are reachable via Resolve
// at runtime but are seeded into the builtin scope, so they pass.
func looksLikeIdentifier(v string) bool {
	if identRe.MatchString(v) {
		return true
	}
	// Symbol operators — present in the builtin scope, treated as refs.
	// `~` is absent: it is no longer an operator but the macro-call prefix
	// sigil; `~=` (not-equal) stays.
	for _, op := range []string{
		"+", "-", "*", "/", "==", "~=", "<=", ">=", "<", ">", "=",
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
// A non-leaf (a list or a computed expression) returns ok=false; the shape
// checker reports it.
func declIdent(n ast.PNode) (string, span.Span, bool) {
	leaf, ok := n.(*ast.PLeaf)
	if !ok || !looksLikeIdentifier(leaf.Value) {
		return "", span.Span{}, false
	}
	return leaf.Value, leaf.Span, true
}

// declList returns the children of a bare parameter or field list `(a b …)`,
// or nil if n isn't a bare parenthesized form. A non-list (e.g. a bare leaf
// where a list is expected) is rejected so the shape checker can flag it.
func declList(n ast.PNode) ([]ast.PNode, bool) {
	br, ok := n.(*ast.PBranch)
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
	if !core.IsStrLit(v) {
		return "", false
	}
	return core.StrLitBody(v), true
}
