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

	// Sigs records the qualified names ("add", "Owner.name") whose type
	// SIGNATURE was collected at this scope's level. Signatures bind no value,
	// so they aren't Defs; the clause checker consults this (through the chain)
	// to tell "no signature anywhere" from "signature in a sibling file".
	Sigs map[string]bool
}

// markSig records a signature name at this scope's level.
func (s *Scope) markSig(name string) {
	if s.Sigs == nil {
		s.Sigs = map[string]bool{}
	}
	s.Sigs[name] = true
}

// HasSig walks the scope chain for a recorded signature name.
func (s *Scope) HasSig(name string) bool {
	for cur := s; cur != nil; cur = cur.Parent {
		if cur.Sigs[name] {
			return true
		}
	}
	return false
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
// in builtins.NewEnv plus the Pho-side value literals (none/true/false) that the
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
	// The match expression (Features.md §3); `case`/`->`/`where` are
	// structural markers inside it, not names.
	"select",
	// Declarations / bindings.
	"fun", "macro", "method", "operator", "struct", "property", "static", "trait", "template", "let", "var", "const", "type", "=", "block",
	// The lambda family (Features.md §11) — every effect-suffix combination.
	"lambda", "lambda?", "lambda!", "lambda=", "lambda?!", "lambda?=", "lambda!=", "lambda?!=",
	// Collections. (slice/map are intentionally absent: they are mangled —
	// `[…]`/`{…}` are the only surface forms — so a bare `(slice …)`/`(map …)`
	// is an unresolved call, not a builtin.)
	"get", "has", "append", "drop", "range", "list?",
	// Atoms.
	"atom?", "atom", "atom-name",
	// Meta / code-as-data.
	"inspect", "identity", "spread", "optional",
	// Module imports.
	"import", "goimport",
	// Type system: first-class type values + runtime type operations.
	"Number", "String", "List", "Map", "Boolean", "Char", "Atom", "Function",
	"Never", "Type", "Unknown", "None", "Collection", "Dynamic",
	// NB: no "Is?" — membership is the universal method (x.Is? T) only,
	// resolved via the type-member surface (typemembers.go), not a builtin.
	"subtype?", "Or", "And", "Not", "Diff", "Fun", "Struct", "Trait",
	// Value literals recognized by the leaf evaluator (the capitalized
	// Nil/True/False are no longer values — a bare one is undefined).
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
// A single optional leading '#' (the private marker) and the trailing effect
// suffixes '?' (predicate), '!' (environmental effect) and '=' (self/value
// mutation) are allowed, in the fixed order `name?!=` (Doc/PlanV1/Syntax.md,
// Doc/PlanV1/Effects.md).
var identRe = regexp.MustCompile(`^#?[A-Za-z][A-Za-z0-9]*(-[A-Za-z0-9]+)*\??!?=?$`)

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

// letTargetIsDestructure reports whether a `let` target destructures its value
// (a `[…]` list or a `Type.{ … }` struct pattern) — its binders are PARTS of
// the value — rather than binding the whole value under one name (a bare name,
// `(name)`, `(var name)`, or `(Type name)`).
func letTargetIsDestructure(target ast.PNode) bool {
	br, ok := target.(*ast.PBranch)
	if !ok {
		return false
	}
	if br.Open == "[" {
		return true
	}
	if br.Open != "(" || len(br.Children) == 0 {
		return false
	}
	head, ok := br.Children[0].(*ast.PLeaf)
	// A struct destructure has a Capitalized head and 3+ children (Type key
	// pat …); a `(var name)` / `(Type name)` binder does not.
	return ok && len(br.Children) >= 3 && head.Value != "" && head.Value[0] >= 'A' && head.Value[0] <= 'Z'
}

// letBind is one name a `let` target binds: its name, span, and whether it was
// written `(var …)` (reassignable).
type letBind struct {
	name    string
	span    span.Span
	mutable bool
}

// letTargetBinders extracts the names a `let` target binds, mirroring the
// runtime pattern engine (pkg/builtins). `top` marks the target itself — a bare
// leaf there binds regardless of case (`(let Foo = …)`); nested inside a `[…]`
// list or a struct destructure, only a lowercase leaf binds (a Capitalized leaf
// is a type-value literal — matched, not bound). mutable propagates from an
// enclosing `(var …)`. Handles: bare name, `(name)` capture, `(var name)`,
// `(Type name)` / `(var Type name)` (type erased), `[p …]`, and a struct
// destructure `(Type key pat …)` whose `(field)` keys also capture.
func letTargetBinders(target ast.PNode, top, mutable bool) []letBind {
	switch n := target.(type) {
	case *ast.PLeaf:
		v := n.Value
		if !looksLikeIdentifier(v) {
			return nil // a literal (number/string/atom) — matched, not bound
		}
		if !top && (v == "true" || v == "false" || v == "none" || (v[0] >= 'A' && v[0] <= 'Z')) {
			return nil // nested literal / type-value match
		}
		return []letBind{{v, n.Span, mutable}}

	case *ast.PBranch:
		if n.Open == "[" { // list destructure
			var out []letBind
			for _, ch := range n.Children {
				out = append(out, letTargetBinders(ch, false, mutable)...)
			}
			return out
		}
		if n.Open != "(" || len(n.Children) == 0 {
			return nil
		}
		head, headIsLeaf := n.Children[0].(*ast.PLeaf)

		// Struct destructure (Type key pat …): a Capitalized head, 3+ children,
		// alternating key/pattern. Each `(field)` capture key binds too.
		if headIsLeaf && len(n.Children) >= 3 && (len(n.Children)-1)%2 == 0 &&
			head.Value != "" && head.Value[0] >= 'A' && head.Value[0] <= 'Z' {
			var out []letBind
			for i := 1; i+1 < len(n.Children); i += 2 {
				if _, isStr := stringLiteral(n.Children[i]); !isStr {
					out = append(out, letTargetBinders(n.Children[i], false, mutable)...) // (field) capture
				}
				out = append(out, letTargetBinders(n.Children[i+1], false, mutable)...) // the field pattern
			}
			return out
		}
		// (var …) — mutable wrapper around a name or (Type name): the name is last.
		if headIsLeaf && head.Value == "var" && len(n.Children) >= 2 {
			return letTargetBinders(n.Children[len(n.Children)-1], true, true)
		}
		// (Type name) typed binder — the name is last, the type erased.
		if len(n.Children) == 2 {
			return letTargetBinders(n.Children[1], true, mutable)
		}
		// (name) — the capture operator.
		if len(n.Children) == 1 {
			return letTargetBinders(n.Children[0], true, mutable)
		}
	}
	return nil
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
