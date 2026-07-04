package lint

import (
	"sort"
	"strings"

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

	case *ast.PSlash:
		// `pkg/sub/member` package navigation. Every segment EXCEPT the last is
		// a namespace (package/subpackage); the FINAL RHS is the export being
		// referenced — a function or a type — which must NOT read as a namespace
		// (the theme may italicize those). Paint the whole LHS chain @namespace,
		// then the final export by its kind. Mirrors the tree-sitter highlight.
		c.emitSlashNamespaces(node.LHS)
		if leaf, ok := node.RHS.(*ast.PLeaf); ok {
			c.emit(leaf.Span, slashExportToken(leaf.Value))
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
	case "method", "operator":
		// `operator` is an operator-overload signature — painted like a method.
		c.semMethod(scope, br)
	case "property":
		c.semProperty(scope, br)
	case "static":
		c.semStatic(scope, br)
	case "struct":
		c.semStruct(scope, br)
	case "var", "const":
		c.semVarConst(scope, br)
	case "let":
		c.semLet(scope, br)
	case "select":
		c.semSelect(scope, br)
	case "foreach":
		c.semForeach(scope, br)
	case "while", "until":
		c.semCondLoop(scope, br)
	case "if", "unless":
		c.semIf(scope, br)
	case "import", "goimport":
		c.semImport(scope, br)
	case "=":
		// A 4-child `(= name (params) body)` / `(= Owner.name …)` is a fun/method
		// IMPLEMENTATION (declOf normalizes it to Head fun/method); paint it like
		// fun/method. A 2-arg `=` is reassignment.
		if d, ok := declOf(br); ok && d.Head == "fun" {
			c.semFun(scope, br)
		} else if ok && d.Head == "method" {
			c.semMethod(scope, br)
		} else {
			c.semAssign(scope, br)
		}
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

// emitSlashNamespaces paints every identifier in a package path's LHS as a
// namespace. The LHS of a slash chain is either the leftmost package leaf or a
// nested slash chain whose own RHS is a further subpackage — all namespaces.
// (The final export is handled by the caller, not here.)
func (c *semCollector) emitSlashNamespaces(n ast.PNode) {
	switch node := n.(type) {
	case *ast.PLeaf:
		c.emit(node.Span, SemTokNamespace)
	case *ast.PSlash:
		c.emitSlashNamespaces(node.LHS)
		if leaf, ok := node.RHS.(*ast.PLeaf); ok {
			c.emit(leaf.Span, SemTokNamespace)
		}
	}
}

// slashExportToken classifies the FINAL segment of a package path. A
// Title-Kebab-Case name is a type; anything else is a function (exported
// values, functions, and effectful callables all read as callable) — never a
// namespace, so it isn't italicized like the intermediate package segments.
func slashExportToken(name string) SemanticTokenType {
	s := strings.TrimPrefix(name, "#")
	if s != "" && s[0] >= 'A' && s[0] <= 'Z' {
		return SemTokType
	}
	return SemTokFunction
}

// classifyLeaf emits a SemanticToken for an identifier-shaped leaf
// based on what it resolves to in scope.
func (c *semCollector) classifyLeaf(scope *Scope, leaf *ast.PLeaf) {
	if !looksLikeIdentifier(leaf.Value) {
		return
	}
	if leaf.Value == "self" || leaf.Value == "Self" {
		// `self` is the receiver PARAMETER and `Self` its TYPE — paint both
		// @parameter so they read the same everywhere (param list, body
		// reference, signature receiver), rather than a @function/@type color
		// that made the receiver stand out from its value.
		c.emit(leaf.Span, SemTokParameter)
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
	"static": true, "trait": true, "template": true, "type": true,
	"var": true, "const": true, "let": true, "block": true,
	"if": true, "unless": true, "foreach": true, "while": true, "until": true, "do": true,
	"select": true,
	"return": true, "break": true, "continue": true,
	"and": true, "or": true, "not": true,
	"import": true, "goimport": true,
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

// semProperty classifies (property <Receiver.>Name (get (params) body)
// [(set (params) body)]): the receiver is a struct ref, the name @property, each
// accessor's get/set head @keyword, and its params/body walked as code.
func (c *semCollector) semProperty(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // `property` keyword

	d, _ := declOf(br)
	if len(br.Children) >= 2 {
		nameSlot := br.Children[1]
		// Typed property `(property (Type name) …)`: paint the type @type, then
		// treat the inner target as the name slot.
		if inner, ok := asList(nameSlot); ok && len(inner.Children) == 2 {
			c.emitTypeNode(inner.Children[0])
			nameSlot = inner.Children[1]
		}
		if dot, ok := nameSlot.(*ast.PDot); ok {
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
	// (property Target (get (params) body) [(set (params) body)]) — paint each
	// accessor's `get`/`set` head @keyword, its params @parameter, body as code.
	// A non-accessor getter slot is the rejected old flat form (flagged by the
	// shape check); leave it unclassified.
	if len(br.Children) < 3 || !isAccessorSublist(br.Children[2], "get") {
		return
	}
	for _, ch := range br.Children[2:] {
		acc, ok := asList(ch)
		if !ok || len(acc.Children) != 3 {
			continue
		}
		if h, ok := acc.Children[0].(*ast.PLeaf); ok {
			c.emit(h.Span, SemTokKeyword)
		}
		c.walkFunctionLike(scope, acc.Children[1], acc.Children[2], d.Owner != "")
	}
}

// semStatic classifies a `(static method Recv.M …)` / `(static property Recv.P
// …)` declaration — the type-level analogue of semMethod/semProperty. Without
// it these fall to the generic-call default, leaving the member name and (for a
// signature) the type slots unclassified. The `static` + inner keyword are
// painted, the receiver (dot LHS) is a struct ref, the member name (dot RHS) is
// @method/@property, and a static METHOD signature's slots are painted @type
// (like semMethod's sig arm — mirroring the decls.go IsSig rule).
func (c *semCollector) semStatic(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // `static` keyword
	if len(br.Children) >= 2 {
		if kw, ok := br.Children[1].(*ast.PLeaf); ok {
			c.classifyLeaf(scope, kw) // inner `method` / `property` keyword
		}
	}
	d, _ := declOf(br)
	if len(br.Children) >= 3 {
		if dot, ok := br.Children[2].(*ast.PDot); ok {
			c.walk(scope, dot.LHS, true) // receiver is a real struct ref
			if d.Name != "" {
				tok := SemTokMethod
				if d.Sub == "property" {
					tok = SemTokProperty
				}
				c.emit(d.NameSpan, tok)
			}
		}
	}
	switch d.Sub {
	case "method":
		if d.ArgList == nil || d.Body == nil {
			return
		}
		if d.IsSig { // a static method signature: slots are types, not code
			c.emitSigTypes(d.ArgList, d.Body)
			return
		}
		c.walkFunctionLike(scope, d.ArgList, d.Body, true)
	case "property":
		// (get (params) body) / (set (params) body) accessors: paint each head
		// @keyword and walk its params/body as code (self is the receiver type).
		for _, ch := range br.Children[3:] {
			acc, ok := asList(ch)
			if !ok || len(acc.Children) != 3 {
				continue
			}
			if h, ok := acc.Children[0].(*ast.PLeaf); ok {
				c.emit(h.Span, SemTokKeyword)
			}
			c.walkFunctionLike(scope, acc.Children[1], acc.Children[2], true)
		}
	}
}

// semStruct classifies (struct Name f0 f1 …).
func (c *semCollector) semStruct(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br)
	if len(br.Children) < 2 {
		return
	}
	// Typed-field form `(struct (Name T "F" U "G" …))`: the struct name is the
	// inner branch's head, then `Type name` pairs (type live, name a quoted
	// string). Paint each type slot @type; the names are string literals.
	nameNode := br.Children[1]
	if inner, ok := br.Children[1].(*ast.PBranch); ok && inner.Open == "(" && len(inner.Children) >= 1 {
		nameNode = inner.Children[0]
		for i := 1; i+1 < len(inner.Children); i += 2 {
			c.emitTypeNode(inner.Children[i])
		}
	}
	if _, span, ok := declIdent(nameNode); ok {
		c.emit(span, SemTokType)
	}
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
// tree-sitter). An IMPLEMENTATION CLAUSE `(let [Owner.]name (patterns)
// [where guard] = body)` routes to semClause instead.
func (c *semCollector) semLet(scope *Scope, br *ast.PBranch) {
	if d, ok := declOf(br); ok && d.IsClause {
		c.semClause(scope, br, d)
		return
	}
	c.classifyHead(scope, br)
	i := 1
	if i < len(br.Children) {
		if mod, ok := br.Children[i].(*ast.PLeaf); ok && mod.Value == "var" {
			c.emit(mod.Span, SemTokKeyword)
			i++
		}
	}
	for i < len(br.Children) {
		targetNode, valueNode, next, ok := letBinding(br.Children, i)
		if !ok {
			break
		}
		// Paint the type inside a grouped `(Type name)` target @type.
		if inner, ok := targetNode.(*ast.PBranch); ok && inner.Open == "(" && len(inner.Children) == 2 {
			c.emitTypeNode(inner.Children[0])
		}
		// Paint every binder the target introduces (a destructuring pattern may
		// bind several) @variable.
		for _, b := range letTargetBinders(targetNode, true, false) {
			c.emit(b.span, SemTokVariable)
		}
		c.walk(scope, valueNode, true)
		i = next
	}
}

// semClause classifies an implementation CLAUSE `(let [Owner.]name (patterns)
// [where guard] = body)` (Features.md §1/§2): the `let` head and `where`/`=`
// markers as keywords/operators, the name @function (or the receiver walked +
// name @method), each pattern's BINDERS as @parameter (literal patterns keep
// their literal colors; type patterns paint @type), and the guard + body walked
// in the binder scope.
func (c *semCollector) semClause(scope *Scope, br *ast.PBranch, d topLevelDecl) {
	c.classifyHead(scope, br) // the `let` keyword
	if dot, ok := br.Children[1].(*ast.PDot); ok {
		c.walk(scope, dot.LHS, true) // the receiver is a real struct ref
	}
	if d.Name != "" {
		tok := SemTokFunction
		if d.Head == "method" {
			tok = SemTokMethod
		}
		c.emit(d.NameSpan, tok)
	}

	bodyScope := newScope(scope)
	if items, ok := declList(d.ArgList); ok {
		for _, item := range items {
			c.semPattern(bodyScope, item, true)
		}
	}
	// The `where` / `=` structural markers read as keywords.
	for _, ch := range br.Children[3:] {
		if lf, ok := ch.(*ast.PLeaf); ok && (lf.Value == "where" || lf.Value == "=") {
			c.emit(lf.Span, SemTokKeyword)
		}
	}
	if d.Guard != nil {
		c.walk(bodyScope, d.Guard, true)
	}
	if d.Body != nil {
		w := &walker{}
		w.collect(bodyScope, []ast.PNode{d.Body})
		c.walk(bodyScope, d.Body, true)
	}
}

// semPattern classifies one clause/select PATTERN (Features.md §2): a lowercase
// leaf is a binder (@parameter, defined in scope); `none`/`true`/`false` read
// as keywords; a Capitalized leaf is a type value (@type); numeric/string/atom
// literals keep their literal colors (no token). A `(var/spread x)` wrapper
// paints its head @keyword and binds x; `(Type name)` paints the type and binds
// name; `[p…]` list patterns and `Type.{ field = pat }` struct destructures
// (pre-desugared to `(Type 'field' pat …)`) recurse.
func (c *semCollector) semPattern(scope *Scope, item ast.PNode, top bool) {
	switch node := item.(type) {
	case *ast.PLeaf:
		v := node.Value
		switch {
		case v == "none" || v == "true" || v == "false":
			c.emit(node.Span, SemTokKeyword)
		case !looksLikeIdentifier(v):
			return // a literal — tree-sitter colors it
		case v[0] >= 'A' && v[0] <= 'Z':
			c.emit(node.Span, SemTokType) // a type value matched by identity
		default:
			scope.Defs[v] = Definition{Name: v, Kind: DefParam, Span: node.Span}
			c.emit(node.Span, SemTokParameter)
		}
	case *ast.PBranch:
		if node.Open == "[" {
			for _, ch := range node.Children {
				c.semPattern(scope, ch, false)
			}
			return
		}
		if node.Open != "(" || len(node.Children) == 0 {
			return
		}
		head, ok := node.Children[0].(*ast.PLeaf)
		if !ok {
			return
		}
		if top && (head.Value == "var" || head.Value == "spread") && len(node.Children) == 2 {
			c.emit(head.Span, SemTokKeyword)
			if name, ok := node.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
				scope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
				c.emit(name.Span, SemTokParameter)
			}
			return
		}
		// (Type name) — type test + bind; (Type 'field' pat …) — struct destructure.
		c.emitTypeNode(head)
		if len(node.Children) == 2 {
			if name, ok := node.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) && name.Value[0] >= 'a' && name.Value[0] <= 'z' {
				scope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
				c.emit(name.Span, SemTokParameter)
			}
			return
		}
		for i := 2; i < len(node.Children); i += 2 {
			c.semPattern(scope, node.Children[i], false)
		}
	}
}

// semSelect classifies a `(select value case pattern -> result …)` match
// expression (Features.md §3): the `select` head and `case` markers as
// keywords, `->` as an operator, each case's pattern via semPattern, and each
// result walked in its case's binder scope.
func (c *semCollector) semSelect(scope *Scope, br *ast.PBranch) {
	c.classifyHead(scope, br) // the `select` keyword
	if len(br.Children) >= 2 {
		c.walk(scope, br.Children[1], true) // the scrutinee is code
	}
	for i := 2; i+3 < len(br.Children); i += 4 {
		kw, kwOK := br.Children[i].(*ast.PLeaf)
		arrow, arOK := br.Children[i+2].(*ast.PLeaf)
		if !kwOK || kw.Value != "case" || !arOK || arrow.Value != "->" {
			return // malformed — the shape walker reports it
		}
		c.emit(kw.Span, SemTokKeyword)
		c.emit(arrow.Span, SemTokOperator)
		caseScope := newScope(scope)
		c.semPattern(caseScope, br.Children[i+1], true)
		c.walk(caseScope, br.Children[i+3], true)
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
			c.emitSigParam(item)
		}
	}
	if ret != nil {
		c.emitTypeNode(ret)
	}
}

// emitSigParam paints one signature param slot: a `(var/spread/optional/const
// T)` modifier's head reads as a @keyword and its inner slot as a type; the
// defaulted `(optional T else D)` also paints `else` @keyword (the DEFAULT is a
// literal/name — left to its own coloring). A bare slot is a plain type.
func (c *semCollector) emitSigParam(item ast.PNode) {
	if br, ok := asList(item); ok && len(br.Children) >= 2 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			switch head.Value {
			case "var", "spread", "optional", "const", "disc":
				c.emit(head.Span, SemTokKeyword)
				c.emitTypeNode(br.Children[1])
				if len(br.Children) == 4 {
					if kw, ok := br.Children[2].(*ast.PLeaf); ok && kw.Value == "else" {
						c.emit(kw.Span, SemTokKeyword)
					}
				}
				return
			}
		}
	}
	c.emitTypeNode(item)
}

// emitTypeNode paints a type EXPRESSION as @type: a bare type name or connective
// leaf, recursing into a compound `(Or …)`/`(List …)`/`(Map …)` form so every
// type identifier reads as a type. Numeric/string/atom singleton literals keep
// their own highlighting.
func (c *semCollector) emitTypeNode(node ast.PNode) {
	switch n := node.(type) {
	case *ast.PLeaf:
		if n.Value == "Self" {
			// `Self` (the receiver TYPE) colors like the `self` value — a
			// @parameter, not a plain @type — so a signature's receiver reads
			// the same as the impl's `self`.
			c.emit(n.Span, SemTokParameter)
		} else if looksLikeIdentifier(n.Value) {
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
		// `self` is a parameter (the receiver) like any other — paint it
		// @parameter, not @function. It sits in the parameter list and reads
		// as an argument, so a function color made the receiver stand out
		// wrongly (the first arg always rendered as a function/call).
		c.emit(leaf.Span, SemTokParameter)
		return
	}
	br, ok := item.(*ast.PBranch)
	if !ok || br.Open != "(" || len(br.Children) == 0 {
		return
	}
	h, ok := br.Children[0].(*ast.PLeaf)
	if !ok {
		return
	}
	// (or name default) — defaulted optional: `or` reads as a keyword, `name` is
	// the parameter, and DEFAULT is walked as ordinary code.
	if h.Value == "or" && len(br.Children) == 3 {
		if name, ok := br.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
			scope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
			c.emit(h.Span, SemTokKeyword)
			c.emit(name.Span, SemTokParameter)
		}
		c.walk(scope, br.Children[2], true)
		return
	}
	if len(br.Children) == 2 && (h.Value == "spread" || h.Value == "optional") {
		if name, ok := br.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
			scope.Defs[name.Value] = Definition{Name: name.Value, Kind: DefParam, Span: name.Span}
			c.emit(h.Span, SemTokKeyword) // `spread` / `optional` marker
			c.emit(name.Span, SemTokParameter)
		}
	}
}
