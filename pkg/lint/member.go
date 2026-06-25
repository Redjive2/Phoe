package lint

import (
	"fmt"

	"pho/pkg/ast"
	"pho/pkg/span"
)

// Member-access checking: when the shape of a dot expression's LHS is
// statically known, mirror the runtime dot accessor's dispatch
// (pkg/builtins/dot.go) and flag accesses that would fail at runtime.
//
// The surface syntax splits cleanly by RHS shape:
//
//	coll.[expr]   dynamic indexing/slicing — valid on dict, array, str.
//	              The bracket's inner expression is a normal expression
//	              (checked by the walker's ordinary traversal).
//	coll.name     field/member access — valid on instance, package, and
//	              the number fractional-decimal hack. `name` is a literal,
//	              never evaluated.
//
// Each rule mirrors an actual runtime error path:
//
//	instance.nope        → "could not resolve method or field ..."
//	instance.priv        → "cannot index struct instance with private key ..."
//	dict.name            → bare field syntax on a collection: must write
//	                       dict.["name"] / dict.[expr]
//	array.name           → likewise: must write array.[i]
//	num.ident            → "cannot apply '.' to a number and ..."
//	bool/Nil/char/fun.x  → "cannot index a value of type ..."
//
// Unknown shapes are never checked: no false positives on dynamic
// code, by construction.

// checkMemberAccess validates a dot READ against the LHS's inferred
// shape.
func (w *walker) checkMemberAccess(scope *Scope, dot *ast.PDot) {
	sh := w.inferShape(scope, dot.LHS)

	// Object-model member access on a primitive: a bare-identifier RHS.
	if rhs, ok := dot.RHS.(*ast.PLeaf); ok && looksLikeIdentifier(rhs.Value) {
		if tn := shapeTypeName(sh.Kind); tn != "" {
			// A USER-declared extension (collected like a struct method under the
			// type name) resolves to its source site, so hover/go-to-definition
			// work like any struct method.
			localExt := false
			if si, ok := scope.LookupStruct(tn); ok {
				if _, isMethod := si.Methods[rhs.Value]; isMethod {
					localExt = true
					if w.onMemberResolve != nil {
						w.onMemberResolve(rhs.Span, si, rhs.Value, DefMethod)
					}
				}
			}
			switch sources := w.primitiveMemberSources(scope, tn, rhs.Value); {
			case sources == 0:
				// Neither a built-in, a universal member, nor an extension in
				// scope — the object-model analogue of "unknown field/method".
				w.emitMember(rhs.Span, "unknown-member",
					fmt.Sprintf("'%s' is not a member of %s", rhs.Value, tn))
			case sources > 1:
				// Resolves to more than one definition in scope (e.g. two
				// imports, or a local/imported redefinition of a built-in) —
				// ambiguous, mirroring the runtime resolver's clash error.
				w.emitMember(rhs.Span, "member-clash",
					fmt.Sprintf("'%s' on %s is defined by more than one module in scope", rhs.Value, tn))
			case !localExt && w.onBuiltinMember != nil:
				// A single built-in / universal member: it has no workspace span,
				// so give it a synthetic hover. (Imported extensions are also
				// single sources but get no built-in hover — left to completion.)
				if md, ok := builtinMemberHover(tn, rhs.Value); ok {
					w.onBuiltinMember(rhs.Span, md)
				}
			}
		} else {
			// Unknown receiver shape (an untyped parameter, a slice expression,
			// etc.): the member can't be type-checked here (no false positives on
			// dynamic shapes — see the file header), but the access may still
			// resolve at runtime through an imported extension. Mark any import
			// that exports this member used, so a module imported SOLELY for its
			// extension methods on an untyped receiver isn't wrongly flagged
			// unused-import — mirroring primitiveMemberSources, which marks the
			// import used when the receiver type IS statically known.
			w.markExtensionImportUse(scope, rhs.Value)
		}
	}

	switch sh.Kind {
	case ShapeInstance:
		w.checkInstanceMember(scope, dot, sh, false)
	case ShapeDict:
		w.checkDictKey(scope, dot, sh, false)
	case ShapeArray, ShapeString:
		w.checkIndexedAccess(scope, dot, sh)
	case ShapeNum:
		// A bracket RHS would index the number — invalid. A bare-identifier RHS
		// (1.Double) is a method/property access resolved at runtime via the
		// object model, and a numeric RHS (1.5) is the fractional-decimal hack;
		// neither is flagged.
		if _, ok := bracketRHS(dot.RHS); ok {
			w.emitMember(dot.RHS.GetSpan(), "invalid-member-access",
				"cannot index a number")
		}
	case ShapeBool, ShapeNil, ShapeChar, ShapeFun:
		// Indexing these is invalid; a bare-identifier RHS is a method/property
		// access (e.g. x.Is?), resolved at runtime.
		if _, ok := bracketRHS(dot.RHS); ok {
			w.emitMember(dot.RHS.GetSpan(), "invalid-member-access",
				fmt.Sprintf("cannot index a %s value", sh.Kind))
		}
	}
}

// checkMemberWrite validates `(= receiver.member value)` against the
// receiver's inferred shape. Writes differ from reads in two ways:
// only fields are assignable on instances (never methods), and writing
// a fresh static key into a dict is legitimate — it ADDS the key.
func (w *walker) checkMemberWrite(scope *Scope, dot *ast.PDot) {
	sh := w.inferShape(scope, dot.LHS)
	switch sh.Kind {
	case ShapeInstance:
		w.checkInstanceMember(scope, dot, sh, true)
	case ShapeDict:
		w.checkDictKey(scope, dot, sh, true)
	case ShapeArray:
		w.checkIndexedAccess(scope, dot, sh)
	case ShapeString:
		// Strings are immutable: the runtime's `=` has no string case, so
		// any indexed/sliced write into one fails. (Reads index fine — see
		// checkMemberAccess — but writes never do.)
		w.emitMember(dot.RHS.GetSpan(), "invalid-member-access",
			"cannot assign into a string — strings are immutable")
	case ShapeNum, ShapeBool, ShapeNil, ShapeChar, ShapeFun:
		w.emitMember(dot.RHS.GetSpan(), "invalid-member-access",
			fmt.Sprintf("cannot assign into a %s value", sh.Kind))
	}
}

func (w *walker) checkInstanceMember(scope *Scope, dot *ast.PDot, sh Shape, write bool) {
	si, ok := w.resolveStruct(scope, sh)
	if !ok {
		return
	}

	rhs, ok := dot.RHS.(*ast.PLeaf)
	if !ok || !looksLikeIdentifier(rhs.Value) {
		w.emitMember(dot.RHS.GetSpan(), "invalid-member-access",
			fmt.Sprintf("struct instances can only be accessed with a member identifier (struct '%s')", si.Name))
		return
	}
	name := rhs.Value

	_, isField := si.Fields[name]
	_, isMethod := si.Methods[name]

	// Resolve the member for navigation/hover/references BEFORE the privacy
	// and existence checks: an editor still wants go-to-def to work on a
	// reference that happens to violate privacy, and the diagnostics below
	// are independent of resolution.
	if w.onMemberResolve != nil && (isField || isMethod) {
		kind := DefField
		if isMethod && !isField {
			kind = DefMethod
		}
		w.onMemberResolve(rhs.Span, si, name, kind)
	}

	// Privacy mirrors the runtime's check order: lowercase members are only
	// reachable while one of the instance's own methods runs, which
	// statically means "the receiver traces back to self".
	if name[0] == '#' && !sh.Privileged {
		w.emitMember(rhs.Span, "private-member-access",
			fmt.Sprintf("'%s' is private to struct '%s' — lowercase or '#'-prefixed members are only accessible through 'self' inside its methods", name, si.Name))
		return
	}

	if write {
		if isMethod && !isField {
			w.emitMember(rhs.Span, "unknown-member",
				fmt.Sprintf("cannot assign to '%s' — it is a method of struct '%s', not a field", name, si.Name))
			return
		}
		if !isField {
			w.emitMember(rhs.Span, "unknown-member",
				fmt.Sprintf("struct '%s' has no field '%s'", si.Name, name))
		}
		return
	}

	if !isField && !isMethod {
		// Universal methods (x.Is?, x.In?, …) resolve on EVERY value, including a
		// struct instance — mirroring the runtime's universal Unknown members
		// (dot.go falls back to them when an instance lacks the member).
		if isUniversalMember(name) {
			return
		}
		w.emitMember(rhs.Span, "unknown-member",
			fmt.Sprintf("'%s' is not a field or method of struct '%s'", name, si.Name))
	}
}

// checkDictKey handles dot access on a known dict. Dynamic key lookup must
// use the bracket form dict.[key]; a bare RHS (dict.name) is the field
// syntax reserved for structs/packages and fails at runtime. The bracket's
// inner key expression is scope-checked by the walker's normal traversal,
// so here we only flag the bare-syntax mistake and track statically known
// string-literal keys.
func (w *walker) checkDictKey(scope *Scope, dot *ast.PDot, sh Shape, write bool) {
	br, ok := bracketRHS(dot.RHS)
	if !ok {
		// A bare IDENTIFIER RHS is a method/property access (object model),
		// resolved at runtime — not a key lookup, not flagged. A non-identifier
		// bare RHS (d."x") is neither: steer it to bracket form.
		if rhs, ok := dot.RHS.(*ast.PLeaf); ok && looksLikeIdentifier(rhs.Value) {
			return
		}
		w.emitMember(dot.RHS.GetSpan(), "invalid-member-access",
			fmt.Sprintf("dict key lookup uses brackets: write 'coll.[%s]', not 'coll.%s'", memberText(dot.RHS), memberText(dot.RHS)))
		return
	}

	// Static key: a single string-literal / quoted-identifier element.
	// Computed keys (an expression) disable tracking.
	key, span, ok := staticKeyInBracket(br)
	if !ok || sh.Keys == nil {
		return
	}
	if _, present := sh.Keys[key]; present {
		return
	}
	if write {
		// Adding a fresh key is legitimate; remember it so later reads
		// of the same binding see it. Keys maps are shared by
		// reference, so this updates the binding's shape in place.
		sh.Keys[key] = span
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     span,
		Severity: SeverityWarning,
		Code:     "unknown-key",
		Message:  fmt.Sprintf("this dict has no key \"%s\" at this point (keys added dynamically aren't tracked)", key),
	})
}

// checkIndexedAccess covers arrays and strings: indexing and slicing must
// use the bracket form coll.[i] / coll.[a : b]. A bare RHS (coll.i) is the
// field syntax reserved for structs/packages and fails at runtime. Bracket
// contents are scope-checked by the walker's normal traversal.
func (w *walker) checkIndexedAccess(scope *Scope, dot *ast.PDot, sh Shape) {
	if _, ok := bracketRHS(dot.RHS); ok {
		return // a bracket RHS is a valid index/slice (contents checked elsewhere)
	}
	// A bare IDENTIFIER RHS is a method/property access (object model),
	// resolved at runtime — not flagged. A non-identifier bare RHS (e.g.
	// arr.0) is neither a member nor a valid index: steer it to bracket form.
	if rhs, ok := dot.RHS.(*ast.PLeaf); ok && looksLikeIdentifier(rhs.Value) {
		return
	}
	w.emitMember(dot.RHS.GetSpan(), "invalid-member-access",
		fmt.Sprintf("%s indexing uses brackets: write 'coll.[%s]', not 'coll.%s'", sh.Kind, memberText(dot.RHS), memberText(dot.RHS)))
}

// bracketRHS returns the bracket branch of a dynamic-index dot
// (`coll.[…]`). Lowering turns `[ … ]` into an array-literal PBranch, so a
// bracket RHS is exactly a PBranch opened with "[".
func bracketRHS(n ast.PNode) (*ast.PBranch, bool) {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "[" {
		return nil, false
	}
	return br, true
}

// memberText renders a dot RHS for diagnostics. Bare member mistakes are
// almost always a single leaf (`coll.i`, `coll.0`, `coll."key"`); anything
// else collapses to an ellipsis.
func memberText(n ast.PNode) string {
	if lf, ok := n.(*ast.PLeaf); ok {
		return lf.Value
	}
	return "…"
}

// staticKeyInBracket extracts the key text of a statically known dict key
// inside a bracket: dict.["name"] or dict.['name]. A multi-element or
// computed bracket has no static key.
func staticKeyInBracket(br *ast.PBranch) (string, span.Span, bool) {
	if len(br.Children) != 1 {
		return "", span.Span{}, false
	}
	return staticKey(br.Children[0])
}

// staticKey extracts the key text of a statically known dict key:
// a string literal ("name").
func staticKey(n ast.PNode) (string, span.Span, bool) {
	if str, ok := stringLiteral(n); ok {
		return str, n.GetSpan(), true
	}
	return "", span.Span{}, false
}

func (w *walker) emitMember(span span.Span, code, msg string) {
	w.emit(Diagnostic{
		File:     w.file,
		Span:     span,
		Severity: SeverityError,
		Code:     code,
		Message:  msg,
	})
}
