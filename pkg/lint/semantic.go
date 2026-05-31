package lint

import (
	"sort"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// SemanticTokenType is the LSP-style classification for one source
// token: what shade an editor's semantic-highlighting layer should
// paint it. Values are stable and shared with cmd/pho-lsp; the LSP
// translates them into the legend it advertises.
type SemanticTokenType int

const (
	SemTokVariable SemanticTokenType = iota
	SemTokParameter
	SemTokFunction
	SemTokMethod
	SemTokKeyword
	SemTokOperator
	SemTokNamespace
	SemTokType
	SemTokProperty
	SemTokMacro
)

// SemanticTokenTypeNames are the names exposed in the LSP legend, in
// SemanticTokenType numeric order.
var SemanticTokenTypeNames = []string{
	"variable", "parameter", "function", "method",
	"keyword", "operator", "namespace", "type",
	"property", "macro",
}

// SemanticToken is one classified source token.
type SemanticToken struct {
	Span core.Span
	Type SemanticTokenType
}

// SemanticTokens classifies every identifier-like leaf in a file
// against the same scope chain the diagnostic walker builds. Returned
// tokens are sorted by source position. Quoted forms are skipped
// (they're data, not code).
func SemanticTokens(path string, src []byte) []SemanticToken {
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)

	c := &semCollector{}
	scope := newScope(PackageScope(path))
	collectFileScope(scope, tree)

	for _, form := range tree {
		c.walk(scope, form, true)
	}

	sort.SliceStable(c.tokens, func(i, j int) bool {
		a, b := c.tokens[i].Span, c.tokens[j].Span
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.StartCol < b.StartCol
	})
	return c.tokens
}

// collectFileScope is a slimmed-down version of walker.collect that
// just populates declarations into the file scope. Reuses the same
// dispatch table so semantic tokens see the same definitions.
func collectFileScope(scope *Scope, tree []core.PNode) {
	w := &walker{} // discard diagnostics
	w.collect(scope, tree)
}

type semCollector struct {
	tokens []SemanticToken
}

func (c *semCollector) emit(s core.Span, t SemanticTokenType) {
	c.tokens = append(c.tokens, SemanticToken{Span: s, Type: t})
}

// walk traverses an expression and classifies each leaf it encounters.
// inCode mirrors the diagnostic walker — quoted forms (other than
// fun/method bodies) are data and don't get their identifiers
// classified.
func (c *semCollector) walk(scope *Scope, n core.PNode, inCode bool) {
	if n == nil {
		return
	}
	switch node := n.(type) {
	case *core.PLeaf:
		if !inCode {
			return
		}
		c.classifyLeaf(scope, node)

	case *core.PSigil:
		// `'expr` quotes content — data, no semantic refs.
		// `&expr` runs in the caller's scope — recurse.
		if node.Sigil == "'" {
			return
		}
		c.walk(scope, node.Inner, inCode)

	case *core.PDot:
		// LHS is a real reference; classify it. RHS is a property
		// name — emit as @property regardless of scope.
		c.walk(scope, node.LHS, inCode)
		if leaf, ok := node.RHS.(*core.PLeaf); ok {
			c.emit(leaf.Span, SemTokProperty)
		} else {
			c.walk(scope, node.RHS, inCode)
		}

	case *core.PMacroCall:
		// (name! args). Tag name as @macro; args are data.
		if leaf, ok := node.Head.(*core.PLeaf); ok {
			c.emit(leaf.Span, SemTokMacro)
		} else {
			c.walk(scope, node.Head, true)
		}

	case *core.PBranch:
		c.walkBranch(scope, node)
	}
}

// walkBranch handles list/array/dict children, plus the special-form
// dispatch (so e.g. fun/method names get tagged as functions, not
// generic refs).
func (c *semCollector) walkBranch(scope *Scope, br *core.PBranch) {
	if br.Open != "(" {
		// Array or dict — every child is an expression.
		for _, ch := range br.Children {
			c.walk(scope, ch, true)
		}
		return
	}

	head := headIdent(br)
	switch head {
	case "fun":
		c.semFun(scope, br)
	case "method":
		c.semMethod(scope, br)
	case "struct":
		c.semStruct(scope, br)
	case "var", "const":
		c.semVarConst(scope, br)
	case "for":
		c.semFor(scope, br)
	case "import", "goimport":
		c.semImport(scope, br)
	case "=":
		c.semAssign(scope, br)
	case "do":
		// Hoist any var/const decls inside the do into the enclosing
		// scope — matches the diagnostic walker so highlighting picks
		// up names declared in earlier do-children when later ones
		// reference them.
		w := &walker{}
		w.collect(scope, br.Children[1:])
		for _, ch := range br.Children {
			c.walk(scope, ch, true)
		}
	default:
		// Generic call: every child is an expression.
		for _, ch := range br.Children {
			c.walk(scope, ch, true)
		}
	}
}

// classifyLeaf emits a SemanticToken for an identifier-shaped leaf
// based on what it resolves to in scope.
func (c *semCollector) classifyLeaf(scope *Scope, leaf *core.PLeaf) {
	if !looksLikeIdentifier(leaf.Value) {
		return
	}
	if leaf.Value == "self" {
		// Soft keyword: paint `self` the same way as the other
		// builtin names (len, drop, slice, …) — SemTokFunction maps
		// to @function.builtin in the LSP legend, matching the
		// tree-sitter scope so the receiver gets the same color
		// regardless of which highlighter the editor uses.
		c.emit(leaf.Span, SemTokFunction)
		return
	}
	def, _, found := scope.Lookup(leaf.Value)
	if !found {
		c.emit(leaf.Span, SemTokVariable)
		return
	}
	c.emit(leaf.Span, kindToToken(def.Kind, leaf.Value))
}

// kindToToken maps a Definition kind to its semantic token type. Some
// builtins are operators (e.g. `+`) rather than keywords (e.g. `fun`),
// so the name is consulted.
func kindToToken(kind DefKind, name string) SemanticTokenType {
	switch kind {
	case DefBuiltin:
		if isKeywordBuiltin(name) {
			return SemTokKeyword
		}
		if isOperatorBuiltin(name) {
			return SemTokOperator
		}
		return SemTokFunction
	case DefImport:
		return SemTokNamespace
	case DefConst:
		return SemTokVariable
	case DefVar:
		return SemTokVariable
	case DefFun:
		return SemTokFunction
	case DefMethod:
		return SemTokMethod
	case DefStruct:
		return SemTokType
	case DefParam:
		return SemTokParameter
	}
	return SemTokVariable
}

// keywordBuiltins are the builtin names that read as syntactic
// keywords in user code: control flow, declaration forms, and module
// imports.
var keywordBuiltins = map[string]bool{
	"fun": true, "method": true, "struct": true,
	"var": true, "const": true, "block": true,
	"if": true, "for": true, "do": true,
	"return": true, "break": true, "continue": true,
	"and": true, "or": true,
	"import": true, "goimport": true,
	"True": true, "False": true, "Nil": true,
}

func isKeywordBuiltin(name string) bool { return keywordBuiltins[name] }

var operatorBuiltins = map[string]bool{
	"+": true, "-": true, "*": true, "/": true,
	"==": true, "~=": true, "<": true, "<=": true, ">": true, ">=": true,
	"~": true, "=": true,
}

func isOperatorBuiltin(name string) bool { return operatorBuiltins[name] }

// ----------------------------------------------------------------------
// Special-form classifiers
// ----------------------------------------------------------------------

// semFun classifies (fun 'name '(args) '(body)) — name as @function,
// each param as @parameter, body walked in a body scope.
func (c *semCollector) semFun(scope *Scope, br *core.PBranch) {
	c.classifyHead(scope, br) // the `fun` keyword

	var argList, body core.PNode
	switch len(br.Children) {
	case 3:
		argList, body = br.Children[1], br.Children[2]
	case 4:
		// Named: emit name as @function.
		if name, span, ok := quotedIdent(br.Children[1]); ok {
			_ = name
			c.emit(span, SemTokFunction)
		}
		argList, body = br.Children[2], br.Children[3]
	default:
		return
	}
	c.walkFunctionLike(scope, argList, body, false)
}

// semMethod classifies (method Owner 'name '(self args) '(body)).
func (c *semCollector) semMethod(scope *Scope, br *core.PBranch) {
	c.classifyHead(scope, br) // `method` keyword
	if len(br.Children) < 5 {
		return
	}
	// Owner is a real ref (struct constructor).
	c.walk(scope, br.Children[1], true)
	if name, span, ok := quotedIdent(br.Children[2]); ok {
		_ = name
		c.emit(span, SemTokMethod)
	}
	c.walkFunctionLike(scope, br.Children[3], br.Children[4], true)
}

// semStruct classifies (struct 'Name '(fields)).
func (c *semCollector) semStruct(scope *Scope, br *core.PBranch) {
	c.classifyHead(scope, br)
	if len(br.Children) < 3 {
		return
	}
	if _, span, ok := quotedIdent(br.Children[1]); ok {
		c.emit(span, SemTokType)
	}
	// Fields stay un-tagged — they're declaration-only and only show up
	// at the dict-init site as keys (where they're properties).
}

// semFor classifies the two `for` shapes:
//
//	(for &cond &body)             -- while-style; both expressions
//	(for 'name collection &body)  -- iterator-style; name is a per-
//	                                 iteration constant binding visible
//	                                 inside the body
//
// The iterator form opens a body scope so highlighting in the body
// resolves the loop variable to its DefConst entry rather than tagging
// it as an unresolved variable.
func (c *semCollector) semFor(scope *Scope, br *core.PBranch) {
	c.classifyHead(scope, br) // `for` keyword

	switch len(br.Children) {
	case 3:
		// (for &cond &body) — both children are blocks; walk normally.
		c.walk(scope, br.Children[1], true)
		c.walk(scope, br.Children[2], true)
	case 4:
		// Loop variable: emit as @variable at the binding site.
		if _, span, ok := quotedIdent(br.Children[1]); ok {
			c.emit(span, SemTokVariable)
		}
		// Collection runs in the caller's scope.
		c.walk(scope, br.Children[2], true)

		// Body runs in a fresh scope with the loop var defined.
		bodyScope := newScope(scope)
		if name, span, ok := quotedIdent(br.Children[1]); ok {
			bodyScope.Defs[name] = Definition{Name: name, Kind: DefConst, Span: span}
		}
		c.walk(bodyScope, br.Children[3], true)
	}
}

// semVarConst classifies (var 'a 1 'b 2 ...) and (const ...).
func (c *semCollector) semVarConst(scope *Scope, br *core.PBranch) {
	c.classifyHead(scope, br)
	for i := 1; i+1 < len(br.Children); i += 2 {
		if _, span, ok := quotedIdent(br.Children[i]); ok {
			c.emit(span, SemTokVariable)
		}
		c.walk(scope, br.Children[i+1], true)
	}
}

// semImport classifies the bound alias (single or aliased-tuple form).
func (c *semCollector) semImport(scope *Scope, br *core.PBranch) {
	c.classifyHead(scope, br)
	for _, arg := range br.Children[1:] {
		if leaf, ok := arg.(*core.PLeaf); ok && len(leaf.Value) >= 2 && leaf.Value[0] == '"' {
			// Bare string — alias is implicit. Nothing to highlight beyond
			// the string itself, which tree-sitter handles.
			continue
		}
		if abr, ok := arg.(*core.PBranch); ok && abr.Open == "[" && len(abr.Children) == 2 {
			if _, span, ok := quotedIdent(abr.Children[1]); ok {
				c.emit(span, SemTokNamespace)
			}
		}
	}
}

// semAssign classifies (= LHS RHS). Quoted-identifier LHS gets the
// variable's classification; dot-chain LHS gets walked normally.
func (c *semCollector) semAssign(scope *Scope, br *core.PBranch) {
	c.classifyHead(scope, br)
	if len(br.Children) != 3 {
		return
	}
	lhs, rhs := br.Children[1], br.Children[2]
	if name, span, ok := quotedIdent(lhs); ok {
		def, _, found := scope.Lookup(name)
		if found {
			c.emit(span, kindToToken(def.Kind, name))
		} else {
			c.emit(span, SemTokVariable)
		}
	} else {
		c.walk(scope, lhs, true)
	}
	c.walk(scope, rhs, true)
}

// classifyHead emits a token for the first child of a list when it's a
// bare identifier (the special-form keyword like `fun`, `var`, etc.).
func (c *semCollector) classifyHead(scope *Scope, br *core.PBranch) {
	if len(br.Children) == 0 {
		return
	}
	leaf, ok := br.Children[0].(*core.PLeaf)
	if !ok {
		return
	}
	c.classifyLeaf(scope, leaf)
}

// walkFunctionLike walks the params + body of a fun or method,
// defining each param in a body scope and classifying the param
// tokens. For methods the first param is the receiver (conventionally
// `self`) — it's bound implicitly at call time but is always written
// explicitly in source, so we just walk the param list normally.
func (c *semCollector) walkFunctionLike(parent *Scope, argList, body core.PNode, isMethod bool) {
	_ = isMethod
	bodyScope := newScope(parent)

	if items, ok := quotedList(argList); ok {
		for _, item := range items {
			c.collectAndEmitParam(bodyScope, item)
		}
	}

	if items, ok := quotedList(body); ok {
		// Forward references inside body resolve via a collect pass.
		w := &walker{}
		w.collect(bodyScope, items)
		for _, c2 := range items {
			c.walk(bodyScope, c2, true)
		}
	}
}

func (c *semCollector) collectAndEmitParam(scope *Scope, item core.PNode) {
	if leaf, ok := item.(*core.PLeaf); ok && looksLikeIdentifier(leaf.Value) {
		scope.Defs[leaf.Value] = Definition{Name: leaf.Value, Kind: DefParam, Span: leaf.Span}
		// `self` is the soft-keyword receiver convention; paint it as
		// @function.builtin everywhere (matching len/drop/etc.) so the
		// highlight stays consistent between the param list and body
		// references.
		if leaf.Value == "self" {
			c.emit(leaf.Span, SemTokFunction)
		} else {
			c.emit(leaf.Span, SemTokParameter)
		}
		return
	}
	if br, ok := item.(*core.PBranch); ok && br.Open == "(" && len(br.Children) == 2 {
		if h, ok := br.Children[0].(*core.PLeaf); ok && h.Value == "spread" {
			if name, ok := br.Children[1].(*core.PLeaf); ok && looksLikeIdentifier(name.Value) {
				scope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
				c.emit(h.Span, SemTokKeyword) // `spread` itself
				c.emit(name.Span, SemTokParameter)
			}
		}
	}
}
