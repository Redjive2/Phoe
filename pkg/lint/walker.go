package lint

import (
	"fmt"
	"strings"

	"pho/pkg/annot"
	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
	"pho/pkg/syntax"
)

// walker is a stateful AST walker that builds scopes as it descends and
// emits diagnostics for the three scope-aware checkers:
//
//	set-on-constant, unresolved-identifier, redeclaration.
//
// Walk strategy:
//  1. Per scope, do a "collect" pass first that registers every
//     declaration (fun / method / struct / const / var / param /
//     import) in the scope. This lets forward references resolve —
//     a function body can call functions defined later in the file.
//  2. Then a "check" pass walks expressions, treating every
//     identifier leaf as a reference and verifying it resolves.
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

	// inBranch is set while walking code that may or may not run (if
	// arms, for bodies). Reassignments there can't retarget a
	// binding's inferred shape — they invalidate it to Unknown so the
	// member checks stay honest on both sides of the branch.
	inBranch bool

	// methodOwner is the struct name whose method body is being
	// walked ("" outside methods). Lets `self` carry a privileged
	// instance shape.
	methodOwner string

	// fileScope is the scope of the file being analyzed.
	fileScope *Scope

	// bodyScope is the innermost function/method body scope currently
	// being walked (nil at the top level, meaning fileScope). Shape
	// retargeting on reassignment (checkAssign) is sound only for a
	// binding in this exact scope — the code that owns it runs linearly.
	// A binding from an ENCLOSING function (captured by a nested closure)
	// or from file/package level is reassigned at an unknowable moment, so
	// its shape is invalidated instead.
	bodyScope *Scope

	// pkgExports / pkgStructs memoize per-package disk scans for the
	// duration of one analysis pass. Without these, every pkg.Member
	// dot re-reads the package directory. importResolutions memoizes
	// the ancestor-walk stat probes of resolveImportPath the same way.
	pkgExports        map[string]map[string]Definition
	pkgStructs        map[string]map[string]*structInfo
	importResolutions map[string]string

	// Resolution hooks, all optional. Navigation (nav.go) runs the
	// regular walk with these set and gets every resolved reference,
	// member access, and declaration with the same scoping and shape
	// inference the diagnostics use — one source of truth.
	onLeafResolve   func(span span.Span, def Definition)
	onExportResolve func(span span.Span, def Definition)
	onMemberResolve func(span span.Span, si *structInfo, member string, kind DefKind)
	onDefine        func(span span.Span, def Definition)

	// onAnnotation, if set, is called once per annotated top-level form
	// with the evaluated annotation results (one per `--@`, in source
	// order). Lets navigation / hovers surface annotation metadata.
	onAnnotation func(target *ast.PBranch, results []annot.Result)
}

func newWalker(file string) *walker {
	return &walker{file: file, usedImports: map[string]bool{}}
}

// exportsFor is PackageExports with per-pass memoization.
func (w *walker) exportsFor(path string) map[string]Definition {
	if v, ok := w.pkgExports[path]; ok {
		return v
	}
	if w.pkgExports == nil {
		w.pkgExports = map[string]map[string]Definition{}
	}
	v := PackageExports(path)
	w.pkgExports[path] = v
	return v
}

// structsFor is PackageStructs with per-pass memoization.
func (w *walker) structsFor(path string) map[string]*structInfo {
	if v, ok := w.pkgStructs[path]; ok {
		return v
	}
	if w.pkgStructs == nil {
		w.pkgStructs = map[string]map[string]*structInfo{}
	}
	v := PackageStructs(path)
	w.pkgStructs[path] = v
	return v
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
func (w *walker) walkFile(tree []ast.PNode, parent *Scope) {
	fileScope := newScope(parent)
	w.fileScope = fileScope
	w.collect(fileScope, tree)
	// Record shapes for top-level var/const initializers before any
	// checking, so function bodies walked earlier in the file see the
	// shapes of bindings declared later.
	w.assignDeclShapes(fileScope, tree)
	for _, form := range tree {
		w.checkExpr(fileScope, form, false /* not in body code */)
	}
	w.walkAnnotations(tree)
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

// walkAnnotations evaluates the parse-time annotations attached to each
// top-level form (`--@ (sig! ...)` and friends) through pkg/annot's
// isolated, memoized evaluator, and surfaces any diagnostics the annotation
// macros raise — an undefined macro, a bad argument, a macro-side error.
// The process-wide evaluator (annot.Default) carries the macro library,
// loaded once at startup; files without annotations cost nothing.
func (w *walker) walkAnnotations(tree []ast.PNode) {
	ensured := false
	for _, form := range tree {
		br, ok := form.(*ast.PBranch)
		if !ok || len(br.Annotations) == 0 {
			continue
		}
		// First annotation in the file: make sure the macro library is
		// loaded, resolving std/annot relative to this file (so it works
		// from any project layout, not just one guessed at startup).
		if !ensured {
			annot.EnsureDefault(resolveImportPath(w.file, "std/annot"))
			ensured = true
		}
		results := annot.Default().EvaluateBranch(br)
		for _, res := range results {
			for _, d := range res.Diags {
				dg := d.Diagnostic
				// The annotation env has no File of its own; point a
				// call-site diagnostic at the file being linted. An error
				// raised inside a macro body keeps that macro's file.
				if dg.File == "" {
					dg.File = w.file
				}
				w.emit(dg)
			}
		}
		if w.onAnnotation != nil {
			w.onAnnotation(br, results)
		}
	}
}

// ----------------------------------------------------------------------
// Collect pass — gather declarations into the given scope
// ----------------------------------------------------------------------

// collect registers every declaration found at the top level of `forms`
// into `scope`. It does not descend into function bodies or other
// forms — those open their own scope and run their own collect.
func (w *walker) collect(scope *Scope, forms []ast.PNode) {
	for _, form := range forms {
		w.collectOne(scope, form)
	}
}

func (w *walker) collectOne(scope *Scope, form ast.PNode) {
	d, ok := declOf(form)
	if !ok {
		// Non-declaration forms introduce no bindings — except `if`/`unless`,
		// whose arms are bare expressions that run in THIS scope (no frame is
		// pushed), so a var/const declared directly in an arm leaks to the
		// enclosing scope and must be hoisted, just like `do`. Arms wrapped
		// in `do` are hoisted by the do-case at check time (and declOf
		// rejects them here), so we don't double-collect.
		if br, isList := asList(form); isList && (headIdent(br) == "if" || headIdent(br) == "unless") {
			f := parseIfForm(br, headIdent(br), headIdent(br) == "if")
			for _, b := range f.Branches {
				if b.Expr != nil {
					w.collectOne(scope, b.Expr)
				}
			}
			if f.Else != nil {
				w.collectOne(scope, f.Else)
			}
		}
		return
	}
	switch d.Head {
	case "fun":
		// The 2-arg anonymous form has no name to hoist (d.Name == "").
		if d.Name != "" {
			w.define(scope, d.Name, DefFun, d.NameSpan)
		}

	case "macro":
		// Registered under its bare name as a macro, so call sites can tell
		// it must be invoked with `!` and a bare reference to it is rejected.
		if d.Name != "" {
			w.define(scope, d.Name, DefMacro, d.NameSpan)
		}

	case "method":
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
		if d.Owner != "" && d.Name != "" {
			w.define(scope, d.Owner+"."+d.Name, DefMethod, d.NameSpan)
			// Attach to the owner's member table. Lookup first: the
			// struct may have been collected into an outer (package)
			// scope by a sibling file, and splitting its table across
			// scopes would lose members.
			si, ok := scope.LookupStruct(d.Owner)
			if !ok {
				si = scope.structAt(d.Owner)
			}
			si.Methods[d.Name] = d.NameSpan
			if si.MethodFiles == nil {
				si.MethodFiles = map[string]string{}
			}
			si.MethodFiles[d.Name] = w.file
		}

	case "property":
		// A struct-field property `(property Recv.Name …)` is a computed
		// member — register it on the owner's table (like a field) so
		// `inst.Name` resolves. A free-standing `(property Name …)` is a
		// faux variable backed by getter/setter delegates — register it as
		// DefVar so a reference reads (and highlights) as a plain variable.
		switch {
		case d.Owner != "" && d.Name != "":
			si, ok := scope.LookupStruct(d.Owner)
			if !ok {
				si = scope.structAt(d.Owner)
			}
			si.Fields[d.Name] = d.NameSpan
		case d.Name != "":
			w.define(scope, d.Name, DefVar, d.NameSpan)
		}

	case "struct":
		if d.Name != "" {
			w.define(scope, d.Name, DefStruct, d.NameSpan)
			// Record the field table. Adopt a placeholder created by an
			// earlier method collection (possibly in the package scope)
			// rather than shadowing it.
			si, ok := scope.LookupStruct(d.Name)
			if !ok {
				si = scope.structAt(d.Name)
			}
			si.File = w.file
			for _, f := range d.Fields {
				si.Fields[f.Name] = f.Span
				// Fields aren't scope bindings, but navigation needs a hit
				// at the decl site so find-references works from the field
				// name itself. The dotted name mirrors methods' "Owner.name"
				// convention.
				if w.onDefine != nil {
					w.onDefine(f.Span, Definition{
						Name: d.Name + "." + f.Name,
						Kind: DefField,
						Span: f.Span,
						File: w.file,
					})
				}
			}
		}

	case "const":
		for _, b := range d.Binds {
			w.define(scope, b.Name, DefConst, b.Span)
		}

	case "var":
		for _, b := range d.Binds {
			w.define(scope, b.Name, DefVar, b.Span)
		}

	case "import", "goimport":
		w.collectImports(scope, d.Branch)
	}
}

// collectImports handles both single-string and aliased-pair forms:
//
//	(import "std/io")               — alias = basename of path
//	(import ("std/io" myio))        — explicit alias (a bare name)
//	(import "a" "b" ("c" cc))       — multiple
func (w *walker) collectImports(scope *Scope, br *ast.PBranch) {
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
		// Form 2: aliased pair (string alias) — alias is a bare name.
		if abr, ok := arg.(*ast.PBranch); ok && abr.Open == "(" && len(abr.Children) == 2 {
			if path, ok := stringLiteral(abr.Children[0]); ok {
				if alias, span, ok := declIdent(abr.Children[1]); ok {
					w.defineImport(scope, alias, span, path, isGoImport)
					continue
				}
			}
		}
		// Anything else is malformed — `(import foo)` (bare ident),
		// `(import 5)` (number), `(import (x y))` (no string path),
		// etc. Runtime would silently mis-resolve; flag it loudly.
		w.emit(Diagnostic{
			File:     w.file,
			Span:     arg.GetSpan(),
			Severity: SeverityError,
			Code:     "non-string-import-path",
			Message:  "import argument must be a string path (\"std/io\") or an aliased pair (\"std/io\" name)",
		})
	}
}

// defineImport runs the regular `define` path so redeclaration
// diagnostics still fire, then stashes the RESOLVED import path on
// the resulting entry so the dot-member check (and everything else
// that consumes Definition.Path) can find the package's directory
// regardless of the process cwd. `goimport` paths aren't on the Pho
// filesystem, so we leave Path empty for them — the member checker
// treats an empty path as "external, can't validate" and stays
// silent.
func (w *walker) defineImport(scope *Scope, alias string, span span.Span, path string, isGoImport bool) {
	w.define(scope, alias, DefImport, span)
	if isGoImport {
		return
	}
	if d, ok := scope.Defs[alias]; ok {
		d.Path = w.resolveImport(path)
		scope.Defs[alias] = d
	}
}

// resolveImport is resolveImportPath relative to the file being
// walked, memoized for the duration of the pass.
func (w *walker) resolveImport(path string) string {
	key := w.file + "\x00" + path
	if v, ok := w.importResolutions[key]; ok {
		return v
	}
	if w.importResolutions == nil {
		w.importResolutions = map[string]string{}
	}
	v := resolveImportPath(w.file, path)
	w.importResolutions[key] = v
	return v
}

func pathBasename(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// define mirrors the runtime's Declare: var / const / fun / struct /
// import may shadow a binding from an enclosing scope, but cannot
// redeclare a name in the same scope or shadow a builtin. Parameters
// are special — they're installed
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
func (w *walker) define(scope *Scope, name string, kind DefKind, span span.Span) {
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

	def := Definition{Name: name, Kind: kind, Span: span, File: w.file}
	scope.Defs[name] = def
	if w.onDefine != nil {
		w.onDefine(span, def)
	}

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
		// The prior binding is in an enclosing scope (an outer block, the
		// file level, or a closure capture). A var/const/fun here is a
		// fresh binding that shadows it — allowed, matching Declare.
		return
	case kind == DefVar || kind == DefConst:
		// Same-scope rebind: var/const may re-bind a name in place (a fresh
		// binding, reducing var + '=' mutation), matching the runtime's
		// Rebind. fun/struct/import fall through to the redeclaration error.
		return
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

// checkUnquotedName flags a var/const/= name that still carries a leftover
// quote. Post-cutover a binding name is a bare identifier — (var x 5) — so a
// `'x` would declare a binding named literally "x" (quotes and all), which is
// never intended. A bare name is correct and a dot/index = target is left to
// the shape checker. label is the prefix, e.g. "var name" or "assignment
// target".
func (w *walker) checkUnquotedName(target ast.PNode, label string) {
	sig, ok := target.(*ast.PSigil)
	if !ok || sig.Sigil != "'" {
		return // bare name (correct), dot target, or other — not flagged here
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     sig.Span,
		Severity: SeverityError,
		Code:     "quoted-name",
		Message:  label + " is quoted — names are bare now; remove the leading '",
	})
}

// ----------------------------------------------------------------------
// Check pass — walk expressions, verify references, flag set-on-const
// ----------------------------------------------------------------------

// checkExpr walks any expression. inCode is true when we're inside a
// position that the runtime evaluates (function body, top-level form);
// it's false inside data positions (quoted forms other than fun/method
// bodies, macro arguments).
func (w *walker) checkExpr(scope *Scope, n ast.PNode, inCode bool) {
	if n == nil {
		return
	}
	switch node := n.(type) {
	case *ast.PLeaf:
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
		if w.onLeafResolve != nil {
			w.onLeafResolve(node.Span, def)
		}
		if def.Kind == DefImport {
			w.usedImports[node.Value] = true
		}

	case *ast.PSigil:
		// `'expr` quotes its content — data, not a reference.
		if node.Sigil == "'" {
			return
		}
		// `&expr` is a one-argument block whose implicit parameter is `it`
		// (see the `block` builtin). Resolve its body in a child scope that
		// binds `it`, so `&(+ it 1)` / `&do …` don't flag `it` as unresolved.
		// Inserted directly (not via define) so no phantom decl is reported to
		// onDefine, and a fresh child scope means nested `&` blocks each get
		// their own `it`.
		blockScope := newScope(scope)
		blockScope.Defs["it"] = Definition{Name: "it", Kind: DefParam, Span: node.Span, File: w.file}
		w.checkExpr(blockScope, node.Inner, inCode)

	case *ast.PDot:
		// LHS is a reference; a bare RHS is a member name (looked up
		// at runtime against whatever LHS evaluates to), while a
		// bracket RHS (coll.[expr]) carries a real index/key
		// expression. We always check the LHS; when the LHS is an
		// import alias we verify the RHS against the package's
		// exports, and when the LHS's shape is known we mirror the
		// runtime dot dispatch (member.go). Bracket contents are
		// ordinary expressions, so we walk them for scope resolution.
		w.checkExpr(scope, node.LHS, inCode)
		w.checkPackageMember(scope, node)
		w.checkMemberAccess(scope, node)
		if br, ok := bracketRHS(node.RHS); ok {
			w.checkExpr(scope, br, inCode)
		}

	case *ast.PMacroCall:
		// (name! args) — runtime quotes args, so they're data. The macro
		// name itself is a real reference, and the `!` means it must be a
		// macro: calling a non-macro (a function, etc.) with `!` is an error.
		w.checkExpr(scope, node.Head, true)
		if leaf, ok := node.Head.(*ast.PLeaf); ok && looksLikeIdentifier(leaf.Value) {
			if def, _, found := scope.Lookup(leaf.Value); found && def.Kind != DefMacro {
				w.emit(Diagnostic{
					File:     w.file,
					Span:     leaf.Span,
					Severity: SeverityError,
					Code:     "not-a-macro",
					Message:  fmt.Sprintf("'%s' is a %s, not a macro — call it without the '!'", leaf.Value, def.Kind),
				})
			}
		}

	case *ast.PBranch:
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
func (w *walker) checkPackageMember(scope *Scope, dot *ast.PDot) {
	leaf, ok := dot.LHS.(*ast.PLeaf)
	if !ok {
		return
	}
	def, _, found := scope.Lookup(leaf.Value)
	if !found || def.Kind != DefImport || def.Path == "" {
		return
	}
	rhs, ok := dot.RHS.(*ast.PLeaf)
	if !ok {
		return
	}

	exports := w.exportsFor(def.Path)
	if exports == nil {
		// Can't read the package — stay quiet. Surfacing "package
		// not found" on every dot access would drown out the real
		// signal when the LSP's cwd doesn't include the project root.
		return
	}
	if export, ok := exports[rhs.Value]; ok {
		if w.onExportResolve != nil {
			w.onExportResolve(rhs.Span, export)
		}
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

// nonHeadDoIndex returns the index of the first bare `do` element that is
// not the form's head (position >= 1), or -1. It marks a `do` notation
// block — `(identity do …)` — whose tail sequences in the current scope.
// Head-position `do` keeps its own switch case (and is now a runtime
// error, but the linter stays lenient).
func nonHeadDoIndex(br *ast.PBranch) int {
	for i := 1; i < len(br.Children); i++ {
		if lf, ok := br.Children[i].(*ast.PLeaf); ok && lf.Value == "do" {
			return i
		}
	}
	return -1
}

// checkBranch dispatches based on the head of a list. Most special
// forms (fun, method, var, const, =, etc.) need bespoke handling so
// we don't flag declaration-position names as unresolved references.
func (w *walker) checkBranch(scope *Scope, br *ast.PBranch) {
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

	// `do` notation: a bare `do` AFTER the head turns every following
	// sibling into a sequencing block that runs in THIS scope (do
	// introduces no frame). Operands before `do` — typically the
	// `identity` wrapper — are ordinary references; the tail is collected
	// first so forward references resolve, mirroring the head-`do` case.
	if k := nonHeadDoIndex(br); k >= 0 {
		for _, c := range br.Children[:k] {
			w.checkExpr(scope, c, true)
		}
		body := br.Children[k+1:]
		w.collect(scope, body)
		w.assignDeclShapes(scope, body)
		for _, c := range body {
			w.checkExpr(scope, c, true)
		}
		return
	}

	switch head {
	case "fun":
		w.checkFun(scope, br)
	case "macro":
		// A macro body is reference-checked exactly like a fun body —
		// declOf hands back its param list and body the same way.
		w.checkFun(scope, br)
	case "method":
		w.checkMethod(scope, br)
	case "property":
		w.checkProperty(scope, br)
	case "struct":
		// (struct Name f0 f1 …) — name + fields are declarations,
		// nothing to reference-check.
		return
	case "var", "const":
		// (var 'a 1 'b 2 ...) — names are declarations; values are
		// expressions that may reference other names.
		for i := 1; i+1 < len(br.Children); i += 2 {
			w.checkUnquotedName(br.Children[i], headIdent(br)+" name")
			w.checkExpr(scope, br.Children[i+1], true)
		}
		// Re-record shapes at the decl's lexical position: the hoisting
		// pre-pass ran before any reassignments, so this refresh keeps
		// shape tracking lexically accurate.
		w.assignDeclShapes(scope, []ast.PNode{br})
	case "if", "unless":
		// (if cond then expr [elif cond then expr]* [else expr]) and the
		// elif-less `unless`. The first condition always evaluates; every later
		// condition and every arm is conditional, so a shape reassignment there
		// invalidates rather than retargets (see checkAssign). The
		// then/elif/else keyword markers are consumed by parseIfForm, never
		// walked as references.
		f := parseIfForm(br, head, head == "if")
		if len(f.Branches) > 0 && f.Branches[0].Cond != nil {
			w.checkExpr(scope, f.Branches[0].Cond, true)
		}
		prevBranch := w.inBranch
		w.inBranch = true
		for i, b := range f.Branches {
			if i > 0 && b.Cond != nil {
				w.checkExpr(scope, b.Cond, true)
			}
			if b.Expr != nil {
				w.checkExpr(scope, b.Expr, true)
			}
		}
		if f.Else != nil {
			w.checkExpr(scope, f.Else, true)
		}
		w.inBranch = prevBranch
	case "foreach":
		w.checkForeach(scope, br)
	case "while", "until":
		w.checkCondLoop(scope, br)
	case "do":
		// `do` doesn't push a new scope — vars/consts inside it land in
		// the enclosing scope. Run a collect pass over its children
		// first so subsequent (and forward-referenced) statements can
		// resolve them, matching how walkFunctionBody hoists body-level
		// declarations. Shapes are recorded in the same pre-pass order.
		w.collect(scope, br.Children[1:])
		w.assignDeclShapes(scope, br.Children[1:])
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
		// Generic call: every child is an expression. But if the head names
		// a macro, a bare call is wrong — macros are invoked with the `!`
		// sugar (which the runtime lowers through Macrocall).
		if len(br.Children) > 0 {
			if leaf, ok := br.Children[0].(*ast.PLeaf); ok && looksLikeIdentifier(leaf.Value) {
				if def, _, found := scope.Lookup(leaf.Value); found && def.Kind == DefMacro {
					w.emit(Diagnostic{
						File:     w.file,
						Span:     leaf.Span,
						Severity: SeverityError,
						Code:     "macro-needs-bang",
						Message:  fmt.Sprintf("'%s' is a macro — call it with the '!' syntax: (%s! ...)", leaf.Value, leaf.Value),
					})
				}
			}
		}
		// The retired `(T { … })` construction form — a struct constructor
		// applied to a single brace argument — is no longer how a struct is
		// built; point at the `T.{ … }` replacement.
		if name, arg, ok := w.retiredConstruction(scope, br); ok {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     arg.GetSpan(),
				Severity: SeverityError,
				Code:     "retired-construction",
				Message:  fmt.Sprintf("the (%s { … }) construction form was removed; write %s.{ field value … }", name, name),
			})
		}
		for _, c := range br.Children {
			w.checkExpr(scope, c, true)
		}
	}
}

// retiredConstruction matches the removed `(T { … })` struct-construction
// form: a call whose head names a struct constructor (a local `(struct …)`
// or an imported `pkg.Struct`) and whose only argument is a brace group. It
// returns the constructor's display name and the offending brace.
func (w *walker) retiredConstruction(scope *Scope, br *ast.PBranch) (string, *ast.PBranch, bool) {
	if br.Open != "(" || len(br.Children) != 2 {
		return "", nil, false
	}
	arg, ok := br.Children[1].(*ast.PBranch)
	if !ok || arg.Open != "{" {
		return "", nil, false
	}
	switch h := br.Children[0].(type) {
	case *ast.PLeaf:
		if !looksLikeIdentifier(h.Value) {
			return "", nil, false
		}
		if def, _, found := scope.Lookup(h.Value); found && def.Kind == DefStruct {
			return h.Value, arg, true
		}
	case *ast.PDot:
		alias, aok := h.LHS.(*ast.PLeaf)
		member, mok := h.RHS.(*ast.PLeaf)
		if !aok || !mok {
			return "", nil, false
		}
		def, _, found := scope.Lookup(alias.Value)
		if !found || def.Kind != DefImport || def.Path == "" {
			return "", nil, false
		}
		if _, ok := w.structsFor(def.Path)[member.Value]; ok {
			return alias.Value + "." + member.Value, arg, true
		}
	}
	return "", nil, false
}

// checkForeach walks `(foreach name in collection body)`. The collection is
// a reference in the caller's scope; the body runs in a child scope with the
// loop variable bound as a per-iteration constant. The `in` keyword marker
// (child 2) is structural and is deliberately not walked as a reference.
func (w *walker) checkForeach(scope *Scope, br *ast.PBranch) {
	prevLoop := w.inLoop
	w.inLoop = true
	defer func() { w.inLoop = prevLoop }()

	// Loop bodies run zero or many times — reassignments inside can't
	// retarget shapes (see checkAssign).
	prevBranch := w.inBranch
	w.inBranch = true
	defer func() { w.inBranch = prevBranch }()

	if len(br.Children) != 5 {
		return // arity / keyword shape reported by the shape checker
	}
	// Collection (child 3) is evaluated in the caller's scope.
	w.checkExpr(scope, br.Children[3], true)

	bodyScope := newScope(scope)
	if name, span, ok := declIdent(br.Children[1]); ok {
		def := Definition{Name: name, Kind: DefConst, Span: span, File: w.file}
		bodyScope.Defs[name] = def
		if w.onDefine != nil {
			w.onDefine(span, def)
		}
	}
	w.checkExpr(bodyScope, br.Children[4], true)
}

// checkCondLoop walks `(while cond then body)` / `(until cond then body)`.
// Both the condition and the body can break/continue. The `then` keyword
// marker (child 2) is structural and is not walked as a reference.
func (w *walker) checkCondLoop(scope *Scope, br *ast.PBranch) {
	prevLoop := w.inLoop
	w.inLoop = true
	defer func() { w.inLoop = prevLoop }()

	prevBranch := w.inBranch
	w.inBranch = true
	defer func() { w.inBranch = prevBranch }()

	if len(br.Children) != 4 {
		return
	}
	w.checkExpr(scope, br.Children[1], true) // condition
	w.checkExpr(scope, br.Children[3], true) // body
}

// checkFun walks (fun 'name '(args) '(body)) or (fun '(args) '(body)).
func (w *walker) checkFun(scope *Scope, br *ast.PBranch) {
	d, _ := declOf(br)
	if d.ArgList == nil || d.Body == nil {
		return
	}
	w.walkFunctionBody(scope, d.ArgList, d.Body, "" /* not a method */)
}

// checkProperty walks (property <Receiver.>Name get getter [set setter]). For
// a struct-field property the receiver (the dot's LHS) is a reference; the
// field name (the dot's RHS) and a free-standing bare Name are declarations,
// not references. `get`/`set` are positional keywords. The getter (child 3)
// and setter (child 5) are anonymous fun/method forms walked normally.
func (w *walker) checkProperty(scope *Scope, br *ast.PBranch) {
	if len(br.Children) >= 2 {
		if dot, ok := br.Children[1].(*ast.PDot); ok {
			w.checkExpr(scope, dot.LHS, true)
		}
	}
	for i := 3; i < len(br.Children); i += 2 {
		w.checkExpr(scope, br.Children[i], true)
	}
}

// checkMethod walks (method Owner 'name '(args) '(body)).
func (w *walker) checkMethod(scope *Scope, br *ast.PBranch) {
	d, _ := declOf(br)
	if d.ArgList == nil || d.Body == nil {
		return // too short to be a complete method
	}
	// The receiver references the owning struct — check it resolves. For a
	// named method it's the dot's LHS (the name, the dot's RHS, is being
	// DECLARED — not a member reference); for an anonymous method the bare
	// first child IS the receiver.
	if dot, ok := br.Children[1].(*ast.PDot); ok {
		w.checkExpr(scope, dot.LHS, true)
	} else if len(br.Children) >= 2 {
		w.checkExpr(scope, br.Children[1], true)
	}
	w.walkFunctionBody(scope, d.ArgList, d.Body, d.Owner)
}

// walkFunctionBody opens a body scope, defines the parameters in it,
// then walks the body for references.
//
// argList is `'(arg1 arg2 ...)`; body is `'(...)`. For methods, the
// first parameter is the receiver (conventionally `self`) — the
// runtime's BindMethod binds it from the instance stack at call time,
// but it still appears in the source param list so we define it the
// normal way, then stamp it with a privileged instance shape of the
// owning struct so member checks know what `self.x` reaches.
//
// owner is the method's struct name, or "" for a plain fun. It
// toggles the `inMethod` flag for the duration of the body walk so
// the leaf check knows whether `self` is allowed here. The flag stays
// sticky across nested funs (Pho captures via closure, so a fun
// defined inside a method can still see the enclosing `self`); it's
// only reset when we leave the outer method body.
func (w *walker) walkFunctionBody(parent *Scope, argList, body ast.PNode, owner string) {
	if owner != "" {
		prev := w.inMethod
		prevOwner := w.methodOwner
		w.inMethod = true
		w.methodOwner = owner
		defer func() { w.inMethod = prev; w.methodOwner = prevOwner }()
	}

	prevFun := w.inFunction
	w.inFunction = true
	defer func() { w.inFunction = prevFun }()

	// Crossing a function boundary breaks the lexical link to any
	// enclosing loop — see the inLoop comment on the walker struct.
	prevLoop := w.inLoop
	w.inLoop = false
	defer func() { w.inLoop = prevLoop }()

	// A function body runs straight through when called, so its OWN
	// locals can be shape-tracked linearly; the cross-frame rule in
	// checkAssign separately protects outer bindings.
	prevBranch := w.inBranch
	w.inBranch = false
	defer func() { w.inBranch = prevBranch }()

	bodyScope := newScope(parent)

	// This body's own scope is where reassignment may soundly retarget a
	// shape (see checkAssign). Restore on exit so a nested fun/method
	// hands control back to its enclosing body's scope.
	prevBody := w.bodyScope
	w.bodyScope = bodyScope
	defer func() { w.bodyScope = prevBody }()

	// unquoteForm bridges the de-sigiling migration: an old-style `'(args)`
	// list is unwrapped so its params are still defined here (the form itself
	// is separately flagged with bad-form-shape). Without this, params of an
	// un-migrated fun resolve to nothing and every body reference to them
	// errors as unresolved.
	if items, ok := declList(unquoteForm(argList)); ok {
		for _, item := range items {
			w.collectParam(bodyScope, item)
		}
	}

	if owner != "" {
		// The receiver param (conventionally the first, named `self`)
		// is a privileged instance of the owner struct.
		if d, ok := bodyScope.Defs["self"]; ok {
			d.Shape = Shape{Kind: ShapeInstance, Owner: owner, Privileged: true}
			bodyScope.Defs["self"] = d
		}
	}

	// The body is a single expression — at runtime BindFun evaluates it as one
	// form. During the de-sigiling migration it may still be written in the
	// legacy quoted style `'(...)`; unwrap that (the quote is syntax, not data)
	// so the body's references are resolved rather than skipped — otherwise an
	// imported alias used only inside an old-style body is falsely flagged
	// unused. Walk it directly so special forms (do / if / for / =) get their
	// dispatch in checkBranch. A `(do …)` / `(identity do …)` body hoists its
	// own statement scope in the "do" case.
	w.checkExpr(bodyScope, unquoteForm(body), true)
}

// collectParam handles a single entry in a parameter list.
//
//	identifier            — bound as a regular parameter
//	(spread name)         — name bound, captures rest-args
//	(optional name)       — name bound, omittable (defaults to Nil)
func (w *walker) collectParam(scope *Scope, item ast.PNode) {
	if leaf, ok := item.(*ast.PLeaf); ok {
		if looksLikeIdentifier(leaf.Value) {
			w.define(scope, leaf.Value, DefParam, leaf.Span)
		}
		return
	}
	if br, ok := item.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) == 2 {
		if h, ok := br.Children[0].(*ast.PLeaf); ok && (h.Value == "spread" || h.Value == "optional") {
			if name, ok := br.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
				w.define(scope, name.Value, DefParam, name.Span)
			}
		}
	}
}

// checkAssign handles `(= LHS RHS)`. The LHS may be a quoted name
// (variable assignment) or a dot chain (struct-field write); only the
// quoted-name case can fire set-on-constant.
func (w *walker) checkAssign(scope *Scope, br *ast.PBranch) {
	if len(br.Children) != 3 {
		return
	}
	lhs, rhs := br.Children[1], br.Children[2]

	// A leftover quoted target ('PI) is flagged; a bare name is correct.
	w.checkUnquotedName(lhs, "assignment target")

	// Bare-name LHS: `(= PI 4)`.
	if name, span, ok := declIdent(lhs); ok {
		if def, defScope, found := scope.Lookup(name); found {
			if w.onLeafResolve != nil {
				w.onLeafResolve(span, def)
			}
			if def.Kind == DefVar {
				// Track the binding's new shape. Retargeting is only sound
				// when the assignment definitely runs exactly once before
				// later reads: not inside an if-arm or loop (w.inBranch),
				// and in the SAME linear scope that owns the binding — the
				// current body's own scope (or the file scope at top
				// level). A binding from an enclosing function (captured by
				// this closure) or from file/package level is reassigned at
				// an unknowable moment, so its shape is invalidated.
				ownScope := w.bodyScope
				if ownScope == nil {
					ownScope = w.fileScope
				}
				updated := def
				if w.inBranch || defScope != ownScope {
					updated.Shape = Shape{}
				} else {
					updated.Shape = w.inferShape(scope, rhs)
				}
				defScope.Defs[name] = updated
			}
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
	// reference, then validate the written member against the
	// receiver's shape — writes have their own rules (only fields are
	// assignable on instances; fresh static keys on dicts are adds,
	// not mistakes), so this must NOT go through the read-path
	// checkExpr on the full dot chain.
	if dot, ok := lhs.(*ast.PDot); ok {
		w.checkExpr(scope, dot.LHS, true)
		// Writing to a member of an imported module is forbidden — a
		// module's bindings (var/const exports included) are read-only from
		// outside it. Still validate the member exists, then flag the write.
		if leaf, ok := dot.LHS.(*ast.PLeaf); ok {
			if def, _, found := scope.Lookup(leaf.Value); found && def.Kind == DefImport {
				w.checkPackageMember(scope, dot)
				member := ""
				if rhs, ok := dot.RHS.(*ast.PLeaf); ok {
					member = rhs.Value
				}
				w.emit(Diagnostic{
					File:     w.file,
					Span:     dot.RHS.GetSpan(),
					Severity: SeverityError,
					Code:     "readonly-module-member",
					Message:  fmt.Sprintf("cannot assign to '%s': bindings of imported module '%s' are read-only from outside it", member, leaf.Value),
				})
				w.checkExpr(scope, rhs, true)
				return
			}
		}
		w.checkPackageMember(scope, dot)
		w.checkMemberWrite(scope, dot)
		// A bracket index target (= coll.[expr] v) carries a real
		// expression; walk it so the index/key resolves and is checked.
		if br, ok := bracketRHS(dot.RHS); ok {
			w.checkExpr(scope, br, true)
		}
	} else {
		w.checkExpr(scope, lhs, true)
	}
	w.checkExpr(scope, rhs, true)
}

// checkInterpChunks walks each `%...` expression embedded in an
// interpolated string. Lex/parse/split errors land as diagnostics so
// the LSP shows them; expression chunks are re-lexed, re-parsed,
// span-shifted back to the source file's coordinates, and run through
// the regular checkExpr path so unresolved-identifier (and friends)
// fire on names referenced inside `%name` / `%a.b.c` / `%(call args)`.
func (w *walker) checkInterpChunks(scope *Scope, leaf *ast.PLeaf, body string, inCode bool) {
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
		tree = syntax.NormalizeDo(tree)
		// Both lex and parse errors get the same treatment: report at
		// the OUTER leaf's span — re-lexing produces line 1 / col N
		// inside the chunk, which we'd need to offset too, but the
		// chunk's position is close enough for a first surfacing.
		for _, e := range lexErrs {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     leaf.Span,
				Severity: SeverityError,
				Code:     core.ErrParse,
				Message:  "interpolation: " + e.Message,
			})
		}
		for _, e := range parseErrs {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     leaf.Span,
				Severity: SeverityError,
				Code:     core.ErrParse,
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
