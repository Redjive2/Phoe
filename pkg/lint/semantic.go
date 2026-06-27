package lint

import (
	"sort"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
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
	Span span.Span
	Type SemanticTokenType
}

// SemanticTokens classifies every identifier-like leaf in a file
// against the same scope chain the diagnostic walker builds. Returned
// tokens are sorted by source position. Quoted forms are skipped
// (they're data, not code).
func SemanticTokens(path string, src []byte) (toks []SemanticToken) {
	defer func() {
		if r := recover(); r != nil {
			recoverEntry("SemanticTokens", r)
			toks = nil
		}
	}()
	tokens, _ := syntax.LexPos(string(src))
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

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
func collectFileScope(scope *Scope, tree []ast.PNode) {
	w := &walker{} // discard diagnostics
	w.collect(scope, tree)
}

type semCollector struct {
	tokens []SemanticToken
}

func (c *semCollector) emit(s span.Span, t SemanticTokenType) {
	c.tokens = append(c.tokens, SemanticToken{Span: s, Type: t})
}

// walk traverses an expression and classifies each leaf it encounters.
// inCode mirrors the diagnostic walker — quoted forms (other than
// fun/method bodies) are data and don't get their identifiers
// classified.
func (c *semCollector) walk(scope *Scope, n ast.PNode, inCode bool) {
	if n == nil {
		return
	}
	switch node := n.(type) {
	case *ast.PLeaf:
		// Interpolated strings embed real expressions: `"hi %who"`,
		// `"%a.b.c"`, `"%(len xs)"`. Highlight the identifiers inside
		// each `%...` chunk so they get the same colors they would as
		// ordinary code — handled before the inCode gate, mirroring the
		// diagnostic walker's checkInterpChunks (the `%...` parts are
		// always evaluated when the string is, regardless of the
		// surrounding quote).
		if core.IsStrLit(node.Value) {
			body := core.StrLitBody(node.Value)
			if syntax.HasInterpolation(body) {
				c.interpChunks(scope, node, body)
			}
			return
		}
		if !inCode {
			return
		}
		c.classifyLeaf(scope, node)

	case *ast.PSigil:
		// `&expr` is a one-argument block whose implicit parameter is `it`, so
		// classify the body in a child scope binding `it` — that paints it
		// @parameter, matching the reference walker.
		blockScope := newScope(scope)
		blockScope.Defs["it"] = Definition{Name: "it", Kind: DefParam, Span: node.Span}
		c.walk(blockScope, node.Inner, inCode)

	case *ast.PDot:
		// LHS is a real reference; classify it. RHS is a property
		// name — emit as @property regardless of scope.
		c.walk(scope, node.LHS, inCode)
		if leaf, ok := node.RHS.(*ast.PLeaf); ok {
			c.emit(leaf.Span, SemTokProperty)
		} else {
			c.walk(scope, node.RHS, inCode)
		}

	case *ast.PMacroCall:
		// (~name args). Tag name as @macro; args are data.
		if leaf, ok := node.Head.(*ast.PLeaf); ok {
			c.emit(leaf.Span, SemTokMacro)
		} else {
			c.walk(scope, node.Head, true)
		}

	case *ast.PBranch:
		c.walkBranch(scope, node)
	}
}

// interpChunks emits semantic tokens for the identifiers embedded in an
// interpolated string's `%...` chunks. It mirrors the diagnostic
// walker's checkInterpChunks: each expression chunk is re-lexed,
// re-parsed, span-shifted back into the source file's coordinates, and
// run through the normal classifier so `%name` / `%a.b.c` /
// `%(call args)` get the same colors they would as ordinary code. Lex/
// parse/split errors are ignored here — the diagnostic walker already
// reports them; tokens are emitted only for what parses cleanly.
func (c *semCollector) interpChunks(scope *Scope, leaf *ast.PLeaf, body string) {
	chunks, _ := syntax.SplitInterp(body)
	for _, ch := range chunks {
		if !ch.IsExpr {
			continue
		}
		chunkLine, chunkCol := syntax.BodyByteToPos(body, ch.BodyOffset, leaf.Span.StartLine, leaf.Span.StartCol)
		tokens, _ := syntax.LexPos(ch.Text)
		tree, _ := syntax.ParsePos(tokens)
		tree = syntax.NormalizeDo(tree)
		lineDelta := chunkLine - 1
		firstColDelta := chunkCol - 1
		for _, form := range tree {
			syntax.OffsetSpans(form, lineDelta, firstColDelta)
			c.walk(scope, form, true)
		}
	}
}

// walkBranch handles list/array/dict children, plus the special-form
// dispatch (so e.g. fun/method names get tagged as functions, not
// generic refs).
func (c *semCollector) walkBranch(scope *Scope, br *ast.PBranch) {
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
	case "macro":
		c.semMacro(scope, br)
	case "method":
		c.semMethod(scope, br)
	case "property":
		c.semProperty(scope, br)
	case "struct":
		c.semStruct(scope, br)
	case "var", "const":
		c.semVarConst(scope, br)
	case "let":
		c.semLet(scope, br)
	case "foreach":
		c.semForeach(scope, br)
	case "while", "until":
		c.semCondLoop(scope, br)
	case "if", "unless":
		c.semIf(scope, br)
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
func (c *semCollector) classifyLeaf(scope *Scope, leaf *ast.PLeaf) {
	if !looksLikeIdentifier(leaf.Value) {
		return
	}
	if leaf.Value == "self" {
		// Soft keyword: paint `self` the same way as the other
		// builtin names (len, drop, append, …) — SemTokFunction maps
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
	case DefMacro:
		return SemTokMacro
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
	"fun": true, "macro": true, "method": true, "struct": true, "property": true,
	"var": true, "const": true, "let": true, "block": true,
	"if": true, "unless": true, "foreach": true, "while": true, "until": true, "do": true,
	"return": true, "break": true, "continue": true,
	"and": true, "or": true, "not": true,
	"import": true, "goimport": true,
	"True": true, "False": true, "Nil": true,
	"none": true, "true": true, "false": true,
}

func isKeywordBuiltin(name string) bool { return keywordBuiltins[name] }

var operatorBuiltins = map[string]bool{
	"+": true, "-": true, "*": true, "/": true,
	"==": true, "~=": true, "<": true, "<=": true, ">": true, ">=": true,
	"=": true,
}

func isOperatorBuiltin(name string) bool { return operatorBuiltins[name] }

// ----------------------------------------------------------------------
// Special-form classifiers
// ----------------------------------------------------------------------

// semFun classifies (fun 'name '(args) '(body)) — name as @function,
// each param as @parameter, body walked in a body scope.
func (c *semCollector) semFun(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // the `fun` keyword

	d, _ := declOf(br)
	if d.ArgList == nil || d.Body == nil {
		return
	}
	if d.Name != "" { // named form: tag the name as @function
		c.emit(d.NameSpan, SemTokFunction)
	}
	if d.IsSig { // a signature: param/return slots are types, not bindings/code
		c.emitSigTypes(d.ArgList, d.Body)
		return
	}
	c.walkFunctionLike(scope, d.ArgList, d.Body, false)
}

// semMacro classifies (macro ~name (params) body) — the `macro` keyword,
// name as @macro, each param as @parameter, body walked in a body scope.
// declOf locates the name/param list/body (the `~` prefix sigil is the leaf
// at index 1), so this mirrors semFun apart from the name's token type.
func (c *semCollector) semMacro(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // the `macro` keyword
	d, _ := declOf(br)
	if d.ArgList == nil || d.Body == nil {
		return
	}
	if d.Name != "" {
		c.emit(d.NameSpan, SemTokMacro)
	}
	c.walkFunctionLike(scope, d.ArgList, d.Body, false)
}

// semMethod classifies (method Owner 'name '(self args) '(body)).
func (c *semCollector) semMethod(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // `method` keyword

	d, _ := declOf(br)
	if d.ArgList == nil || d.Body == nil {
		return
	}
	// The receiver is a real struct ref: the dot's LHS for a named method, or
	// the bare first child for an anonymous one. A named method's name (the
	// dot's RHS) is the declaration, tagged @method below.
	if dot, ok := br.Children[1].(*ast.PDot); ok {
		c.walk(scope, dot.LHS, true)
	} else if len(br.Children) >= 2 {
		c.walk(scope, br.Children[1], true)
	}
	if d.Name != "" {
		c.emit(d.NameSpan, SemTokMethod)
	}
	if d.IsSig { // a method signature: receiver + param + return slots are types
		c.emitSigTypes(d.ArgList, d.Body)
		return
	}
	c.walkFunctionLike(scope, d.ArgList, d.Body, true)
}

// semProperty classifies (property <Receiver.>Name get getter [set setter]):
// the receiver is a struct ref, the name @property, get/set @keyword, and the
// getter/setter (anonymous fun/method forms) are walked.
func (c *semCollector) semProperty(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // `property` keyword

	d, _ := declOf(br)
	if len(br.Children) >= 2 {
		if dot, ok := br.Children[1].(*ast.PDot); ok {
			// Attached `(property Recv.Name …)`: the receiver is a real
			// reference; the member name reads as a property.
			c.walk(scope, dot.LHS, true)
			if d.Name != "" {
				c.emit(d.NameSpan, SemTokProperty)
			}
		} else if d.Name != "" {
			// Free-standing `(property Name …)` is a faux variable — paint
			// it (and its references, via DefVar) as a variable, not a
			// property.
			c.emit(d.NameSpan, SemTokVariable)
		}
	}
	// `get`/`set` are keywords at children 2/4; getter/setter at 3/5.
	for i := 2; i+1 < len(br.Children); i += 2 {
		if leaf, ok := br.Children[i].(*ast.PLeaf); ok {
			c.emit(leaf.Span, SemTokKeyword)
		}
		c.walk(scope, br.Children[i+1], true)
	}
}

// semStruct classifies (struct Name f0 f1 …).
func (c *semCollector) semStruct(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br)
	if len(br.Children) < 2 {
		return
	}
	// Typed-field form `(struct (Name "F" T …))`: the struct name is the inner
	// branch's head.
	nameNode := br.Children[1]
	if inner, ok := br.Children[1].(*ast.PBranch); ok && inner.Open == "(" && len(inner.Children) >= 1 {
		nameNode = inner.Children[0]
	}
	if _, span, ok := declIdent(nameNode); ok {
		c.emit(span, SemTokType)
	}
	// Fields stay un-tagged — they're declaration-only and only show up
	// at the dict-init site as keys (where they're properties).
}

// semIf classifies an `if`/`unless` form: the head and the then/elif/else
// keyword markers as keywords, and every condition and arm as an ordinary
// expression.
func (c *semCollector) semIf(scope *Scope, br *ast.PBranch) {
	head := headIdent(br)
	c.classifyHead(scope, br) // the `if` / `unless` keyword
	f := parseIfForm(br, head, head == "if")
	for _, kw := range f.Keywords {
		c.emit(kw.Span, SemTokKeyword)
	}
	for _, b := range f.Branches {
		if b.Cond != nil {
			c.walk(scope, b.Cond, true)
		}
		if b.Expr != nil {
			c.walk(scope, b.Expr, true)
		}
	}
	if f.Else != nil {
		c.walk(scope, f.Else, true)
	}
}

// semForeach classifies `(foreach name in collection body)`. It opens a
// body scope so highlighting in the body resolves the loop variable to its
// DefConst entry rather than tagging it as an unresolved variable.
func (c *semCollector) semForeach(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // `foreach` keyword
	if len(br.Children) != 5 {
		return
	}
	if _, span, ok := declIdent(br.Children[1]); ok {
		c.emit(span, SemTokVariable)
	}
	c.emit(br.Children[2].GetSpan(), SemTokKeyword) // `in`
	c.walk(scope, br.Children[3], true)             // collection

	bodyScope := newScope(scope)
	if name, span, ok := declIdent(br.Children[1]); ok {
		bodyScope.Defs[name] = Definition{Name: name, Kind: DefConst, Span: span}
	}
	c.walk(bodyScope, br.Children[4], true) // body
}

// semCondLoop classifies `(while cond then body)` / `(until cond then body)`:
// the condition, the `then` keyword marker, and the body.
func (c *semCollector) semCondLoop(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // `while` / `until` keyword
	if len(br.Children) != 4 {
		return
	}
	c.walk(scope, br.Children[1], true)             // condition
	c.emit(br.Children[2].GetSpan(), SemTokKeyword) // `then`
	c.walk(scope, br.Children[3], true)             // body
}

// semVarConst classifies (var 'a 1 'b 2 ...) and (const ...).
func (c *semCollector) semVarConst(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br)
	for i := 1; i+1 < len(br.Children); i += 2 {
		// The name slot is a bare ident `x` or the typed form `(Type x)`; in
		// the typed form the type leaf is painted @type and the name @variable.
		if inner, ok := br.Children[i].(*ast.PBranch); ok && inner.Open == "(" && len(inner.Children) == 2 {
			c.emitTypeNode(inner.Children[0]) // leaf or compound `(Or …)` type
		}
		if _, span, ok := bindName(br.Children[i]); ok {
			c.emit(span, SemTokVariable)
		}
		c.walk(scope, br.Children[i+1], true)
	}
}

// semLet classifies (let [var] name = value [name = value]*): the `let` head
// and optional `var` modifier as keywords, each binding name as a variable, and
// the value expressions recursively. The `=` markers are punctuation (left to
// tree-sitter).
func (c *semCollector) semLet(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br)
	i := 1
	if i < len(br.Children) {
		if mod, ok := br.Children[i].(*ast.PLeaf); ok && mod.Value == "var" {
			c.emit(mod.Span, SemTokKeyword)
			i++
		}
	}
	for ; i+2 < len(br.Children); i += 3 {
		// The name slot is a bare ident `x` or the typed form `(Type x)`.
		if inner, ok := br.Children[i].(*ast.PBranch); ok && inner.Open == "(" && len(inner.Children) == 2 {
			c.emitTypeNode(inner.Children[0])
		}
		if _, span, ok := bindName(br.Children[i]); ok {
			c.emit(span, SemTokVariable)
		}
		c.walk(scope, br.Children[i+2], true)
	}
}

// semImport classifies the bound alias (single or aliased-tuple form).
func (c *semCollector) semImport(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br)
	for _, arg := range br.Children[1:] {
		if leaf, ok := arg.(*ast.PLeaf); ok && core.IsStrLit(leaf.Value) {
			// Bare string — alias is implicit. Nothing to highlight beyond
			// the string itself, which tree-sitter handles.
			continue
		}
		if abr, ok := arg.(*ast.PBranch); ok && abr.Open == "(" && len(abr.Children) == 2 {
			// Import alias is a bare name (the second element of the pair).
			if _, span, ok := declIdent(abr.Children[1]); ok {
				c.emit(span, SemTokNamespace)
			}
		}
	}
}

// semAssign classifies (= LHS RHS). Quoted-identifier LHS gets the
// variable's classification; dot-chain LHS gets walked normally.
func (c *semCollector) semAssign(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br)
	if len(br.Children) != 3 {
		return
	}
	lhs, rhs := br.Children[1], br.Children[2]
	if name, span, ok := declIdent(lhs); ok {
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
func (c *semCollector) classifyHead(scope *Scope, br *ast.PBranch) {
	if len(br.Children) == 0 {
		return
	}
	leaf, ok := br.Children[0].(*ast.PLeaf)
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
// emitSigTypes paints the parameter-type slots and the return-type slot of an
// inline fun/method SIGNATURE as @type (TypeSignatures.md). Unlike an
// implementation, a signature's param list holds type expressions (the receiver
// type included, for a method) and the "body" is the result type — none of it
// is a binding or code.
func (c *semCollector) emitSigTypes(argList, ret ast.PNode) {
	if items, ok := declList(argList); ok {
		for _, item := range items {
			c.emitTypeNode(item)
		}
	}
	if ret != nil {
		c.emitTypeNode(ret)
	}
}

// emitTypeNode paints a type EXPRESSION as @type: a bare type name or connective
// leaf, recursing into a compound `(Or …)`/`(List …)`/`(Map …)` form so every
// type identifier reads as a type. Numeric/string/atom singleton literals keep
// their own highlighting.
func (c *semCollector) emitTypeNode(node ast.PNode) {
	switch n := node.(type) {
	case *ast.PLeaf:
		if looksLikeIdentifier(n.Value) {
			c.emit(n.Span, SemTokType)
		}
	case *ast.PBranch:
		if n.Open == "(" {
			for _, ch := range n.Children {
				c.emitTypeNode(ch)
			}
		}
	}
}

func (c *semCollector) walkFunctionLike(parent *Scope, argList, body ast.PNode, isMethod bool) {
	_ = isMethod
	bodyScope := newScope(parent)

	if items, ok := declList(argList); ok {
		for _, item := range items {
			c.collectAndEmitParam(bodyScope, item)
		}
	}

	if body != nil {
		// Body is a single bare form. Collect its decls (for forward
		// references) and classify it.
		w := &walker{}
		w.collect(bodyScope, []ast.PNode{body})
		c.walk(bodyScope, body, true)
	}
}

func (c *semCollector) collectAndEmitParam(scope *Scope, item ast.PNode) {
	if leaf, ok := item.(*ast.PLeaf); ok && looksLikeIdentifier(leaf.Value) {
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
	if br, ok := item.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) == 2 {
		if h, ok := br.Children[0].(*ast.PLeaf); ok && (h.Value == "spread" || h.Value == "optional") {
			if name, ok := br.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
				scope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
				c.emit(h.Span, SemTokKeyword) // `spread` / `optional` marker
				c.emit(name.Span, SemTokParameter)
			}
		}
	}
}
