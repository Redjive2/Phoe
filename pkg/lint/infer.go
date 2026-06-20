package lint

import (
	"strings"

	"pho/pkg/ast"
	"pho/pkg/span"
)

// Shape inference: a lightweight static guess at what kind of runtime
// value an expression produces, so the dot-access checker can mirror
// the runtime's dispatch (pkg/builtins/dot.go) at lint time.
//
// The model is deliberately flow-light:
//   - literals and constructor calls produce known shapes
//   - var/const initializers record the shape on the binding
//   - reassignment retargets the shape in lexical order, EXCEPT inside
//     a conditional context (if-arm / for body), where it invalidates
//     to Unknown — we can't know whether the branch runs
//   - everything else (params, call results, quoted data) is Unknown,
//     and Unknown shapes are never checked
//
// "Aggressive but honest": a known shape produces hard errors mirroring
// what the runtime would print; anything uncertain stays silent or
// warns.

// ShapeKind classifies the inferred runtime kind of a value.
type ShapeKind int

const (
	ShapeUnknown ShapeKind = iota
	ShapeInstance
	ShapeDict
	ShapeArray
	ShapeString
	ShapeChar
	ShapeAtom
	ShapeNum
	ShapeBool
	ShapeNil
	ShapeFun
)

func (k ShapeKind) String() string {
	switch k {
	case ShapeInstance:
		return "struct instance"
	case ShapeDict:
		return "dict"
	case ShapeArray:
		return "array"
	case ShapeString:
		return "string"
	case ShapeChar:
		return "char"
	case ShapeAtom:
		return "atom"
	case ShapeNum:
		return "number"
	case ShapeBool:
		return "bool"
	case ShapeNil:
		return "Nil"
	case ShapeFun:
		return "function"
	}
	return "unknown"
}

// Shape is the inferred shape of one value.
//
// Owner/OwnerPkg locate the struct for ShapeInstance: Owner is the
// struct name; OwnerPkg is the import path when the struct lives in an
// imported package ("" = resolve through the local scope chain).
//
// Keys holds the statically known keys of a ShapeDict (key text →
// span of the key in the literal). nil means the keys aren't reliably
// known (a computed key appeared) and key checks stay quiet.
//
// Privileged marks an instance reached via `self` (directly or by
// aliasing) — lowercase members are accessible on it, mirroring the
// runtime's instance privacy flag.
type Shape struct {
	Kind       ShapeKind
	Owner      string
	OwnerPkg   string
	Keys       map[string]span.Span
	Privileged bool
}

// structInfo is the statically collected surface of one struct:
// declared fields and attached methods. File is where the struct was
// declared (used by navigation).
type structInfo struct {
	Name    string
	Fields  map[string]span.Span
	Methods map[string]span.Span
	File    string
	// MethodFiles tracks the declaring file per method — methods can be
	// attached from a different file than the struct declaration.
	MethodFiles map[string]string
}

// resolveStruct finds the field/method tables for an instance shape:
// through the scope chain for local/package structs, through the
// imported package's source for OwnerPkg shapes.
func (w *walker) resolveStruct(scope *Scope, sh Shape) (*structInfo, bool) {
	if sh.Kind != ShapeInstance || sh.Owner == "" {
		return nil, false
	}
	if sh.OwnerPkg != "" {
		si, ok := w.structsFor(sh.OwnerPkg)[sh.Owner]
		return si, ok
	}
	return scope.LookupStruct(sh.Owner)
}

// inferShape computes the shape of an expression. Anything it can't
// be confident about is ShapeUnknown.
func (w *walker) inferShape(scope *Scope, n ast.PNode) Shape {
	switch node := n.(type) {
	case *ast.PLeaf:
		return w.inferLeafShape(scope, node)

	case *ast.PBranch:
		switch node.Open {
		case "[":
			return Shape{Kind: ShapeArray}
		case "{":
			return Shape{Kind: ShapeDict, Keys: dictLiteralKeys(node.Children)}
		case "(":
			return w.inferCallShape(scope, node)
		}

	case *ast.PDot, *ast.PSigil, *ast.PMacroCall:
		// Field reads, quoted data, blocks, and macro results are all
		// runtime-dependent.
		return Shape{}
	}
	return Shape{}
}

func (w *walker) inferLeafShape(scope *Scope, leaf *ast.PLeaf) Shape {
	v := leaf.Value
	if v == "" {
		return Shape{}
	}
	switch {
	case v == "True" || v == "False":
		return Shape{Kind: ShapeBool}
	case v == "Nil":
		return Shape{Kind: ShapeNil}
	case v[0] == '"':
		return Shape{Kind: ShapeString}
	case v[0] == '`':
		return Shape{Kind: ShapeChar}
	case v[0] == ':':
		return Shape{Kind: ShapeAtom}
	case isNumLiteral(v):
		return Shape{Kind: ShapeNum}
	}
	if !looksLikeIdentifier(v) {
		return Shape{}
	}
	def, _, ok := scope.Lookup(v)
	if !ok {
		return Shape{}
	}
	switch def.Kind {
	case DefVar, DefConst, DefParam:
		return def.Shape
	case DefFun:
		return Shape{Kind: ShapeFun}
	}
	return Shape{}
}

// inferCallShape handles `(head args...)` forms: struct constructors
// (local and imported) and a small table of builtins with fixed result
// kinds. The builtin names are reliable references — shadowing a
// builtin is a redeclaration error — so this can't be fooled by a
// user-defined `len`.
func (w *walker) inferCallShape(scope *Scope, br *ast.PBranch) Shape {
	if len(br.Children) == 0 {
		return Shape{}
	}

	// (Name args...) where Name is a struct → instance of it.
	if head, ok := br.Children[0].(*ast.PLeaf); ok {
		if looksLikeIdentifier(head.Value) {
			if def, _, found := scope.Lookup(head.Value); found && def.Kind == DefStruct {
				return Shape{Kind: ShapeInstance, Owner: head.Value}
			}
		}
		switch head.Value {
		// `+` is intentionally absent: it is both numeric addition and
		// string concatenation, so its result shape is num-or-str. Leaving
		// it Unknown avoids false-positive member checks on (+ a b).
		case "-", "*", "/", "mod", "len":
			return Shape{Kind: ShapeNum}
		case "==", "~=", "<", "<=", ">", ">=", "~", "and", "or", "has":
			return Shape{Kind: ShapeBool}
		case "slice", "append", "drop", "range", "keyof":
			return Shape{Kind: ShapeArray}
		case "map":
			return Shape{Kind: ShapeDict, Keys: dictLiteralKeys(br.Children[1:])}
		case "fun":
			return Shape{Kind: ShapeFun}
		}
		return Shape{}
	}

	// (pkg.Struct args...) — constructor reached through an import.
	if dot, ok := br.Children[0].(*ast.PDot); ok {
		alias, ok := dot.LHS.(*ast.PLeaf)
		if !ok {
			return Shape{}
		}
		member, ok := dot.RHS.(*ast.PLeaf)
		if !ok {
			return Shape{}
		}
		def, _, found := scope.Lookup(alias.Value)
		if !found || def.Kind != DefImport || def.Path == "" {
			return Shape{}
		}
		if _, ok := w.structsFor(def.Path)[member.Value]; ok {
			return Shape{Kind: ShapeInstance, Owner: member.Value, OwnerPkg: def.Path}
		}
	}
	return Shape{}
}

// dictLiteralKeys harvests the statically known keys of a dict literal
// (or a (map ...) call's args): entries at even positions, either
// string literals or quoted identifiers. Returns nil — "keys unknown"
// — if any key position holds something computed, since then the
// literal's key set can't be trusted.
func dictLiteralKeys(entries []ast.PNode) map[string]span.Span {
	keys := map[string]span.Span{}
	for i := 0; i < len(entries); i += 2 {
		if name, span, ok := quotedIdent(entries[i]); ok {
			keys[name] = span
			continue
		}
		if str, ok := stringLiteral(entries[i]); ok {
			keys[str] = entries[i].GetSpan()
			continue
		}
		return nil
	}
	return keys
}

// isNumLiteral matches the lexer's number tokens: digits, optionally
// preceded by a glued minus.
func isNumLiteral(v string) bool {
	if strings.HasPrefix(v, "-") {
		v = v[1:]
	}
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

// assignDeclShapes records inferred shapes for the `(var n v)` /
// `(const n v)` pairs in `forms` onto the bindings already collected
// into `scope`. Runs in lexical order so a later initializer sees the
// shapes of earlier bindings.
func (w *walker) assignDeclShapes(scope *Scope, forms []ast.PNode) {
	for _, form := range forms {
		br, ok := asList(form)
		if !ok {
			continue
		}
		head := headIdent(br)
		if head != "var" && head != "const" {
			continue
		}
		for i := 1; i+1 < len(br.Children); i += 2 {
			name, _, ok := declIdent(br.Children[i])
			if !ok {
				continue
			}
			def, exists := scope.Defs[name]
			if !exists || (def.Kind != DefVar && def.Kind != DefConst) {
				continue
			}
			def.Shape = w.inferShape(scope, br.Children[i+1])
			scope.Defs[name] = def
		}
	}
}
