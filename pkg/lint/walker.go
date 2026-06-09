package lint

import (
	"fmt"
	"strings"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// walker is a stateful AST walker that builds scopes as it descends and
// emits diagnostics for the three scope-aware checkers:
//   set-on-constant, unresolved-identifier, redeclaration.
//
// Walk strategy:
//   1. Per scope, do a "collect" pass first that registers every
//      declaration (fun / method / struct / const / var / param /
//      import) in the scope. This lets forward references resolve —
//      a function body can call functions defined later in the file.
//   2. Then a "check" pass walks expressions, treating every
//      identifier leaf as a reference and verifying it resolves.
//
// `(=` `var`/`const`/`fun`/`method`/`struct`/`import`/`goimport` are
// treated specially — their LHS or quoted-name positions are
// declarations, not references.
type walker struct {
	file        string
	diagnostics []Diagnostic

	// inMethod is set while we're walking a method body (and any
	// nested funs inside it — Pho captures `self` via the closure
	// so a fun defined inside a method can still see it). Used to
	// gate the invalid-self-usage check.
	inMethod bool

	// inFunction is set while we're inside any function or method
	// body, including nested ones. Gates the return-outside-function
	// check. Sticky across nested funs: a `return` inside a fun
	// nested in another fun exits the inner one.
	inFunction bool

	// inLoop is set while we're inside a `for`'s argument forms
	// (body, condition, collection). It is CLEARED when crossing a
	// function boundary, matching the convention every imperative
	// language follows: break/continue are lexically scoped to the
	// nearest enclosing loop, and a fun is a new lexical layer.
	// (Runtime would actually let it work because BindFun's recover
	// only catches ReturnSignal — break would tunnel through — but
	// relying on that is bad style and surprises future readers.)
	inLoop bool

	// usedImports records which DefImport aliases were resolved at
	// least once during the check pass. Names with zero usages get
	// an unused-import warning when the walk finishes.
	usedImports map[string]bool
}

func newWalker(file string) *walker {
	return &walker{file: file, usedImports: map[string]bool{}}
}

func (w *walker) emit(d Diagnostic) {
	w.diagnostics = append(w.diagnostics, d)
}

// ----------------------------------------------------------------------
// File-level entry
// ----------------------------------------------------------------------

// walkFile lints `tree` against the given parent scope. The parent
// is typically the package scope returned by PackageScope; for an
// isolated file with no siblings, callers can pass newBuiltinScope().
func (w *walker) walkFile(tree []core.PNode, parent *Scope) {
	fileScope := newScope(parent)
	w.collect(fileScope, tree)
	for _, form := range tree {
		w.checkExpr(fileScope, form, false /* not in body code */)
	}
	// Post-walk: every DefImport in the file scope that wasn't
	// resolved at least once during checking gets flagged. Imports
	// from sibling files (in the package scope) aren't considered —
	// they're owned by their declaring file.
	for name, def := range fileScope.Defs {
		if def.Kind != DefImport {
			continue
		}
		if w.usedImports[name] {
			continue
		}
		w.emit(Diagnostic{
			File:     w.file,
			Span:     def.Span,
			Severity: SeverityWarning,
			Code:     "unused-import",
			Message:  fmt.Sprintf("imported alias '%s' is declared but never used", name),
		})
	}
}

// ----------------------------------------------------------------------
// Collect pass — gather declarations into the given scope
// ----------------------------------------------------------------------

// collect registers every declaration found at the top level of `forms`
// into `scope`. It does not descend into function bodies or other
// forms — those open their own scope and run their own collect.
func (w *walker) collect(scope *Scope, forms []core.PNode) {
	for _, form := range forms {
		w.collectOne(scope, form)
	}
}

func (w *walker) collectOne(scope *Scope, form core.PNode) {
	br, ok := asList(form)
	if !ok {
		return
	}
	head := headIdent(br)
	switch head {
	case "fun":
		// (fun 'name '(args) '(body)) — define name; 2-arg form has no
		// name to hoist.
		if len(br.Children) >= 3 {
			if name, span, ok := quotedIdent(br.Children[1]); ok {
				w.define(scope, name, DefFun, span)
			}
		}

	case "method":
		// (method Owner 'name '(args) '(body))
		//
		// A method is NOT a top-level binding. The runtime stores it in
		// the owner struct's method table (builtins.method does
		// `struct.Methods[name] = ...` and never calls ctx.Declare), and
		// it's only ever reached via `instance.name` — never by bare
		// name. So a method must not be reported as shadowing a fun,
		// const, struct, or a method of the same name on a DIFFERENT
		// owner: those live in separate namespaces. We register it under
		// a receiver-qualified key ("Owner.name") so the one
		// redeclaration still worth flagging — the same method defined
		// twice on the same owner — keeps firing, while "Owner.name" can
		// never match a bare identifier (identifiers can't contain '.'),
		// so nothing else trips. If the owner isn't a plain identifier we
		// can't form a stable key, so we skip it rather than risk a false
		// positive.
		if len(br.Children) >= 4 {
			if name, span, ok := quotedIdent(br.Children[2]); ok {
				if recv, ok := br.Children[1].(*core.PLeaf); ok && looksLikeIdentifier(recv.Value) {
					w.define(scope, recv.Value+"."+name, DefMethod, span)
				}
			}
		}

	case "struct":
		// (struct 'name '(fields))
		if len(br.Children) >= 2 {
			if name, span, ok := quotedIdent(br.Children[1]); ok {
				w.define(scope, name, DefStruct, span)
			}
		}

	case "const":
		// (const 'a 1 'b 2 ...) — pairs.
		for i := 1; i+1 < len(br.Children); i += 2 {
			if name, span, ok := quotedIdent(br.Children[i]); ok {
				w.define(scope, name, DefConst, span)
			}
		}

	case "var":
		for i := 1; i+1 < len(br.Children); i += 2 {
			if name, span, ok := quotedIdent(br.Children[i]); ok {
				w.define(scope, name, DefVar, span)
			}
		}

	case "import", "goimport":
		w.collectImports(scope, br)
	}
}

// collectImports handles both single-string and aliased-tuple forms:
//
//	(import "std/io")               — alias = basename of path
//	(import ["std/io" 'myio])       — explicit alias
//	(import "a" "b" ["c" 'cc])      — multiple
func (w *walker) collectImports(scope *Scope, br *core.PBranch) {
	// `goimport` aliases never have a Pho-side package directory to
	// inspect, so we leave their Path empty and the dot-member check
	// silently skips them.
	isGoImport := headIdent(br) == "goimport"

	for _, arg := range br.Children[1:] {
		// Form 1: bare string. Alias is the basename (last segment).
		if path, ok := stringLiteral(arg); ok {
			alias := pathBasename(path)
			if alias != "" {
				w.defineImport(scope, alias, arg.GetSpan(), path, isGoImport)
			}
			continue
		}
		// Form 2: array of [string, 'alias].
		if abr, ok := arg.(*core.PBranch); ok && abr.Open == "[" && len(abr.Children) == 2 {
			if path, ok := stringLiteral(abr.Children[0]); ok {
				if alias, span, ok := quotedIdent(abr.Children[1]); ok {
					w.defineImport(scope, alias, span, path, isGoImport)
					continue
				}
			}
		}
		// Anything else is malformed — `(import foo)` (bare ident),
		// `(import 5)` (number), `(import [x y])` (no string path),
		// etc. Runtime would silently mis-resolve; flag it loudly.
		w.emit(Diagnostic{
			File:     w.file,
			Span:     arg.GetSpan(),
			Severity: SeverityError,
			Code:     "non-string-import-path",
			Message:  "import argument must be a string path (\"std/io\") or an aliased pair ([\"std/io\" 'name])",
		})
	}
}

// defineImport runs the regular `define` path so redeclaration
// diagnostics still fire, then stashes the import path on the
// resulting entry so the dot-member check can find the package's
// directory. `goimport` paths aren't on the Pho filesystem, so we
// leave Path empty for them — the member checker treats an empty
// path as "external, can't validate" and stays silent.
func (w *walker) defineImport(scope *Scope, alias string, span core.Span, path string, isGoImport bool) {
	w.define(scope, alias, DefImport, span)
	if isGoImport {
		return
	}
	if d, ok := scope.Defs[alias]; ok {
		d.Path = path
		scope.Defs[alias] = d
	}
}

func pathBasename(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// define mirrors the runtime's Declare: var / const / fun / struct /
// import can't shadow ANY visible binding (current scope, enclosing
// scopes, or builtins). Parameters are special — they're installed
// directly into the body frame at call time without going through
// Declare, so they're allowed to shadow. Methods are special too: the
// runtime never Declares them (they live on the owner struct), so
// collectOne passes a receiver-qualified key ("Owner.name") here —
// that way a method only ever collides with another method of the same
// name on the same owner, never with a fun/const/struct/import or a
// method on a different owner.
//
// Always installs the new binding regardless, so subsequent lookups in
// this scope resolve to the just-declared name.
func (w *walker) define(scope *Scope, name string, kind DefKind, span core.Span) {
	var prior Definition
	var foundIn *Scope
	var existed bool

	if kind == DefParam {
		if d, ok := scope.Defs[name]; ok {
			prior, foundIn, existed = d, scope, true
		}
	} else {
		prior, foundIn, existed = scope.Lookup(name)
	}

	scope.Defs[name] = Definition{Name: name, Kind: kind, Span: span}

	if !existed {
		return
	}

	switch {
	case prior.Kind == DefBuiltin:
		w.emit(Diagnostic{
			File:     w.file,
			Span:     span,
			Severity: SeverityError,
			Code:     "redeclaration",
			Message:  fmt.Sprintf("'%s' shadows the builtin of the same name", name),
		})
	case foundIn != nil && foundIn.IsPackage:
		// Same-package name reuse across files. The runtime will
		// reject this at load time if it's a real conflict; silently
		// allowing it here keeps the linter from spamming "shadows..."
		// diagnostics on every legitimate cross-file reference.
		return
	case foundIn != scope:
		w.emit(Diagnostic{
			File:     w.file,
			Span:     span,
			Severity: SeverityError,
			Code:     "redeclaration",
			Message:  fmt.Sprintf("'%s' shadows a %s in an enclosing scope", name, prior.Kind),
		})
	default:
		w.emit(Diagnostic{
			File:     w.file,
			Span:     span,
			Severity: SeverityError,
			Code:     "redeclaration",
			Message:  fmt.Sprintf("'%s' is already declared as a %s in this scope", name, prior.Kind),
		})
	}
}

// ----------------------------------------------------------------------
// Check pass — walk expressions, verify references, flag set-on-const
// ----------------------------------------------------------------------

// checkExpr walks any expression. inCode is true when we're inside a
// position that the runtime evaluates (function body, top-level form);
// it's false inside data positions (quoted forms other than fun/method
// bodies, macro arguments).
func (w *walker) checkExpr(scope *Scope, n core.PNode, inCode bool) {
	if n == nil {
		return
	}
	switch node := n.(type) {
	case *core.PLeaf:
		// String literals normally don't get reference-checked, but
		// `"%name"` interpolation embeds real expressions whose
		// identifiers we want to resolve. Walk into each interp
		// expression chunk so unresolved-identifier / set-on-const /
		// invalid-self-usage all fire there too.
		if len(node.Value) >= 2 && node.Value[0] == '"' && node.Value[len(node.Value)-1] == '"' {
			body := node.Value[1 : len(node.Value)-1]
			if syntax.HasInterpolation(body) {
				w.checkInterpChunks(scope, node, body, inCode)
			}
			return
		}
		if !looksLikeIdentifier(node.Value) {
			return
		}
		// `self` resolves silently via the soft-keyword entry in
		// builtinNames, but using it outside a method body is always
		// a bug — the runtime has no `self` binding at top-level or
		// inside a non-method fun. Flag it explicitly.
		if node.Value == "self" && !w.inMethod {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     node.Span,
				Severity: SeverityError,
				Code:     "invalid-self-usage",
				Message:  "'self' is only valid inside a method body",
			})
		}
		def, _, ok := scope.Lookup(node.Value)
		if !ok {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     node.Span,
				Severity: SeverityError,
				Code:     "unresolved-identifier",
				Message:  fmt.Sprintf("'%s' is not defined", node.Value),
			})
			return
		}
		if def.Kind == DefImport {
			w.usedImports[node.Value] = true
		}

	case *core.PSigil:
		// `'expr` quotes its content — data, not a reference.
		// `&expr` is an inline block that runs in the caller's scope —
		// recurse normally.
		if node.Sigil == "'" {
			return
		}
		w.checkExpr(scope, node.Inner, inCode)

	case *core.PDot:
		// LHS is a reference; RHS is a member name (looked up at
		// runtime against whatever LHS evaluates to). We always
		// check the LHS, and additionally — when the LHS is a bare
		// alias resolving to a Pho-side import — verify the RHS is
		// a known export of that package.
		w.checkExpr(scope, node.LHS, inCode)
		w.checkPackageMember(scope, node)

	case *core.PMacroCall:
		// (name! args) — runtime quotes args, so they're data. The
		// macro name itself is a real reference; arg subtrees aren't
		// reference-checked.
		w.checkExpr(scope, node.Head, true)

	case *core.PBranch:
		w.checkBranch(scope, node)
	}
}

// checkPackageMember validates that `pkg.Member` in the source refers
// to a name actually exported by the imported package. Skips silently
// when the LHS isn't a bare alias, when the alias doesn't resolve to
// a Pho-side import (e.g. it's a local variable or a goimport with
// no Path), when the RHS isn't a static identifier, or when the
// imported package's directory can't be read — in any of those cases
// we can't make a confident static call so we defer to the runtime.
func (w *walker) checkPackageMember(scope *Scope, dot *core.PDot) {
	leaf, ok := dot.LHS.(*core.PLeaf)
	if !ok {
		return
	}
	def, _, found := scope.Lookup(leaf.Value)
	if !found || def.Kind != DefImport || def.Path == "" {
		return
	}
	rhs, ok := dot.RHS.(*core.PLeaf)
	if !ok {
		return
	}

	exports := PackageExports(def.Path)
	if exports == nil {
		// Can't read the package — stay quiet. Surfacing "package
		// not found" on every dot access would drown out the real
		// signal when the LSP's cwd doesn't include the project root.
		return
	}
	if _, ok := exports[rhs.Value]; ok {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     rhs.Span,
		Severity: SeverityError,
		Code:     "unknown-export",
		Message:  fmt.Sprintf("'%s' is not exported by package '%s'", rhs.Value, leaf.Value),
	})
}

// checkBranch dispatches based on the head of a list. Most special
// forms (fun, method, var, const, =, etc.) need bespoke handling so
// we don't flag declaration-position names as unresolved references.
func (w *walker) checkBranch(scope *Scope, br *core.PBranch) {
	if br.Open != "(" {
		// Array or dict literal — every child is an expression.
		for _, c := range br.Children {
			w.checkExpr(scope, c, true)
		}
		return
	}

	// Validate special-form shape first (arity + sigil placement).
	// Diagnostics from this don't halt the walk: the regular case
	// handlers below still run on whatever's there, so reference
	// checking and downstream diagnostics keep firing.
	w.checkSpecialFormShape(br)

	head := headIdent(br)
	switch head {
	case "fun":
		w.checkFun(scope, br)
	case "method":
		w.checkMethod(scope, br)
	case "struct":
		// (struct 'name '(fields)) — name + fields are declarations,
		// nothing to reference-check.
		return
	case "var", "const":
		// (var 'a 1 'b 2 ...) — names are declarations; values are
		// expressions that may reference other names.
		for i := 1; i+1 < len(br.Children); i += 2 {
			w.checkExpr(scope, br.Children[i+1], true)
		}
	case "for":
		w.checkFor(scope, br)
	case "do":
		// `do` doesn't push a new scope — vars/consts inside it land in
		// the enclosing scope. Run a collect pass over its children
		// first so subsequent (and forward-referenced) statements can
		// resolve them, matching how walkFunctionBody hoists body-level
		// declarations.
		w.collect(scope, br.Children[1:])
		for _, c := range br.Children[1:] {
			w.checkExpr(scope, c, true)
		}
	case "=":
		w.checkAssign(scope, br)
	case "import", "goimport":
		// All children handled in the collect pass. Nothing to check
		// for references — paths are strings.
		return
	case "return":
		if !w.inFunction {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     br.Children[0].GetSpan(),
				Severity: SeverityError,
				Code:     "return-outside-function",
				Message:  "'return' is only valid inside a function or method body",
			})
		}
		// The optional value expression is a regular reference site.
		if len(br.Children) >= 2 {
			w.checkExpr(scope, br.Children[1], true)
		}
	case "break":
		if !w.inLoop {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     br.Children[0].GetSpan(),
				Severity: SeverityError,
				Code:     "break-outside-loop",
				Message:  "'break' is only valid inside a 'for' loop",
			})
		}
	case "continue":
		if !w.inLoop {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     br.Children[0].GetSpan(),
				Severity: SeverityError,
				Code:     "continue-outside-loop",
				Message:  "'continue' is only valid inside a 'for' loop",
			})
		}
	default:
		// Generic call: every child is an expression.
		for _, c := range br.Children {
			w.checkExpr(scope, c, true)
		}
	}
}

// checkFor walks both `for` shapes:
//
//	(for &cond &body)             -- while-style; both children are
//	                                 plain expressions, no new bindings
//	(for 'name collection &body)  -- iterator-style; name is a per-
//	                                 iteration const visible only in
//	                                 the body
//
// For the iterator form we open a body scope and write the loop
// variable directly (bypassing w.define) so the natural shadowing of
// an outer `name` doesn't fire a redeclaration diagnostic — `for`
// loop variables are conventionally allowed to shadow.
func (w *walker) checkFor(scope *Scope, br *core.PBranch) {
	// Anything lexically inside a `for` — body, condition, even the
	// collection expression — can break/continue. The runtime would
	// catch any of them: the for's recover wraps every Evaluate of
	// its child forms.
	prevLoop := w.inLoop
	w.inLoop = true
	defer func() { w.inLoop = prevLoop }()

	switch len(br.Children) {
	case 3:
		w.checkExpr(scope, br.Children[1], true)
		w.checkExpr(scope, br.Children[2], true)
	case 4:
		// Collection is evaluated in the caller's scope.
		w.checkExpr(scope, br.Children[2], true)

		bodyScope := newScope(scope)
		if name, span, ok := quotedIdent(br.Children[1]); ok {
			bodyScope.Defs[name] = Definition{Name: name, Kind: DefConst, Span: span}
		}
		w.checkExpr(bodyScope, br.Children[3], true)
	}
}

// checkFun walks (fun 'name '(args) '(body)) or (fun '(args) '(body)).
func (w *walker) checkFun(scope *Scope, br *core.PBranch) {
	var argList, body core.PNode
	switch len(br.Children) {
	case 3:
		argList, body = br.Children[1], br.Children[2]
	case 4:
		argList, body = br.Children[2], br.Children[3]
	default:
		return
	}
	w.walkFunctionBody(scope, argList, body, false /* not a method */)
}

// checkMethod walks (method Owner 'name '(args) '(body)).
func (w *walker) checkMethod(scope *Scope, br *core.PBranch) {
	if len(br.Children) < 5 {
		return
	}
	// Owner is a reference; check it.
	w.checkExpr(scope, br.Children[1], true)
	w.walkFunctionBody(scope, br.Children[3], br.Children[4], true /* method */)
}

// walkFunctionBody opens a body scope, defines the parameters in it,
// then walks the body for references.
//
// argList is `'(arg1 arg2 ...)`; body is `'(...)`. For methods, the
// first parameter is the receiver (conventionally `self`) — the
// runtime's BindMethod binds it from the instance stack at call time,
// but it still appears in the source param list so we define it the
// normal way.
//
// isMethod toggles the `inMethod` flag for the duration of the body
// walk so the leaf check knows whether `self` is allowed here. The
// flag stays sticky across nested funs (Pho captures via closure,
// so a fun defined inside a method can still see the enclosing
// `self`); it's only reset when we leave the outer method body.
func (w *walker) walkFunctionBody(parent *Scope, argList, body core.PNode, isMethod bool) {
	if isMethod {
		prev := w.inMethod
		w.inMethod = true
		defer func() { w.inMethod = prev }()
	}

	prevFun := w.inFunction
	w.inFunction = true
	defer func() { w.inFunction = prevFun }()

	// Crossing a function boundary breaks the lexical link to any
	// enclosing loop — see the inLoop comment on the walker struct.
	prevLoop := w.inLoop
	w.inLoop = false
	defer func() { w.inLoop = prevLoop }()

	bodyScope := newScope(parent)

	if items, ok := quotedList(argList); ok {
		for _, item := range items {
			w.collectParam(bodyScope, item)
		}
	}

	// Body is `'(...)` — at runtime BindFun calls Evaluate on this
	// inner expression as a single form. Mirror that here so special
	// forms (do / if / for / =) get their dispatch in checkBranch
	// instead of being unrolled into independent leaves. The "do"
	// case in checkBranch handles its own scope hoisting, so
	// multi-statement bodies wrapped in `do` keep working.
	if sig, ok := body.(*core.PSigil); ok && sig.Sigil == "'" {
		w.checkExpr(bodyScope, sig.Inner, true)
	}
}

// collectParam handles a single entry in a parameter list.
//
//	identifier            — bound as a regular parameter
//	(spread name)         — name bound, captures rest-args
func (w *walker) collectParam(scope *Scope, item core.PNode) {
	if leaf, ok := item.(*core.PLeaf); ok {
		if looksLikeIdentifier(leaf.Value) {
			w.define(scope, leaf.Value, DefParam, leaf.Span)
		}
		return
	}
	if br, ok := item.(*core.PBranch); ok && br.Open == "(" && len(br.Children) == 2 {
		if h, ok := br.Children[0].(*core.PLeaf); ok && h.Value == "spread" {
			if name, ok := br.Children[1].(*core.PLeaf); ok && looksLikeIdentifier(name.Value) {
				w.define(scope, name.Value, DefParam, name.Span)
			}
		}
	}
}

// checkAssign handles `(= LHS RHS)`. The LHS may be a quoted name
// (variable assignment) or a dot chain (struct-field write); only the
// quoted-name case can fire set-on-constant.
func (w *walker) checkAssign(scope *Scope, br *core.PBranch) {
	if len(br.Children) != 3 {
		return
	}
	lhs, rhs := br.Children[1], br.Children[2]

	// Quoted-name LHS: `(= 'PI 4)`.
	if name, span, ok := quotedIdent(lhs); ok {
		if def, _, found := scope.Lookup(name); found {
			if def.Kind == DefConst {
				w.emit(Diagnostic{
					File:     w.file,
					Span:     span,
					Severity: SeverityError,
					Code:     "set-on-constant",
					Message:  fmt.Sprintf("cannot reassign constant '%s'", name),
				})
			}
			if def.Kind == DefBuiltin {
				w.emit(Diagnostic{
					File:     w.file,
					Span:     span,
					Severity: SeverityError,
					Code:     "set-on-constant",
					Message:  fmt.Sprintf("cannot reassign builtin '%s'", name),
				})
			}
			if def.Kind == DefImport {
				w.emit(Diagnostic{
					File:     w.file,
					Span:     span,
					Severity: SeverityError,
					Code:     "set-on-constant",
					Message:  fmt.Sprintf("cannot reassign import alias '%s'", name),
				})
			}
		} else {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     span,
				Severity: SeverityError,
				Code:     "unresolved-identifier",
				Message:  fmt.Sprintf("'%s' is not defined", name),
			})
		}
		w.checkExpr(scope, rhs, true)
		return
	}

	// Dot-chain LHS: `(= obj.field val)`. Check the receiver as a
	// reference; the field name is opaque.
	w.checkExpr(scope, lhs, true)
	w.checkExpr(scope, rhs, true)
}

// checkInterpChunks walks each `%...` expression embedded in an
// interpolated string. Lex/parse/split errors land as diagnostics so
// the LSP shows them; expression chunks are re-lexed, re-parsed,
// span-shifted back to the source file's coordinates, and run through
// the regular checkExpr path so unresolved-identifier (and friends)
// fire on names referenced inside `%name` / `%a.b.c` / `%(call args)`.
func (w *walker) checkInterpChunks(scope *Scope, leaf *core.PLeaf, body string, inCode bool) {
	chunks, errs := syntax.SplitInterp(body)
	// Split errors point at the leaf as a whole — we don't have a
	// precise span for the bad `%` inside the body without re-walking,
	// and the message is descriptive enough on its own.
	for _, err := range errs {
		w.emit(Diagnostic{
			File:     w.file,
			Span:     leaf.Span,
			Severity: SeverityError,
			Code:     "bad-interpolation",
			Message:  err.Error(),
		})
	}
	for _, ch := range chunks {
		if !ch.IsExpr {
			continue
		}
		chunkLine, chunkCol := syntax.BodyByteToPos(body, ch.BodyOffset, leaf.Span.StartLine, leaf.Span.StartCol)
		tokens, lexErrs := syntax.LexPos(ch.Text)
		tree, parseErrs := syntax.ParsePos(tokens)
		// Both lex and parse errors get the same treatment: report at
		// the OUTER leaf's span — re-lexing produces line 1 / col N
		// inside the chunk, which we'd need to offset too, but the
		// chunk's position is close enough for a first surfacing.
		for _, e := range lexErrs {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     leaf.Span,
				Severity: SeverityError,
				Code:     "parse-error",
				Message:  "interpolation: " + e.Message,
			})
		}
		for _, e := range parseErrs {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     leaf.Span,
				Severity: SeverityError,
				Code:     "parse-error",
				Message:  "interpolation: " + e.Message,
			})
		}
		// Shift spans on a successful parse so identifier diagnostics
		// land in the right column. lineDelta = chunkLine - 1 because
		// inner spans start at line 1; firstColDelta = chunkCol - 1
		// applies only on inner line 1 (subsequent lines reset to col
		// 1 in the inner tree, and that's correct in the source too).
		lineDelta := chunkLine - 1
		firstColDelta := chunkCol - 1
		for _, form := range tree {
			syntax.OffsetSpans(form, lineDelta, firstColDelta)
			w.checkExpr(scope, form, inCode)
		}
	}
}
