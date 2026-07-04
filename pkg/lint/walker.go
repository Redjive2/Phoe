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
// treated specially — their LHS or name positions are
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

	// sigSites collects the inline type SIGNATURE forms seen during
	// collection (fun/method sigs). After collect, checkMissingImpls verifies
	// each has a matching implementation. (Phase 2 of the type-signature plan.)
	sigSites []sigSite

	// inBranch is set while walking code that may or may not run (if
	// arms, for bodies). Reassignments there can't retarget a
	// binding's inferred shape — they invalidate it to Unknown so the
	// member checks stay honest on both sides of the branch.
	inBranch bool

	// clauseParams marks that the param list being collected belongs to a
	// `(let name (patterns) …)` implementation CLAUSE: entries are PATTERNS
	// (literals, type tests, destructures) whose BINDERS define params —
	// literals are matched, not declared (Features.md §2).
	clauseParams bool

	// methodOwner is the struct name whose method body is being
	// walked ("" outside methods). Lets `self` carry a privileged
	// instance shape.
	methodOwner string

	// fileScope is the scope of the file being analyzed.
	fileScope *Scope

	// inMacroLib is set when the file is the annotation macro library (the
	// std/annot directory). Its helper funcs (e.g. `type`, backing `~type`)
	// intentionally shadow builtins — the runtime loads it with AllowShadow —
	// so the linter suppresses the shadows-the-builtin diagnostic there.
	inMacroLib bool

	// declared holds each `--@ (~type T)`-annotated binding's DECLARED type
	// (un-narrowed), set by the gradual checker. Assignment checking reads it so
	// `(= x v)` is validated against x's declared type, not a narrowed flow type
	// (avoiding a false positive when a narrowed var is reassigned).
	declared flowEnv

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
	// onBuiltinMember fires for a member access resolving to a built-in
	// object-model member (a primitive's Size/Keys/Empty? or a universal
	// Is?/In?). These live in the compiler with no source span, so the hook
	// carries ready-rendered hover markdown rather than a definition site.
	onBuiltinMember func(span span.Span, hoverMD string)
	onDefine        func(span span.Span, def Definition)
	// onShapeAssigned fires when assignDeclShapes records an inferred shape for a
	// var/const/let binding, carrying the binding NAME span. onDefine fires during
	// collect (before shapes are inferred), so a declaration hit captures a
	// shapeless Definition; this hook lets navigation backfill the inferred shape
	// so hovering a binding AT its declaration shows its type, like a reference.
	onShapeAssigned func(sp span.Span, sh Shape)

	// onAnnotation, if set, is called once per annotated top-level form
	// with the evaluated annotation results (one per `--@`, in source
	// order). Lets navigation / hovers surface annotation metadata.
	onAnnotation func(target *ast.PBranch, results []annot.Result)

	// bodyScopes maps a function/method body node to the scope the reference
	// walk built for it (params + locals, with shapes). The gradual checker
	// reuses these to type-check INSIDE bodies with correct shape inference —
	// without rebuilding scopes, and so a local correctly shadows a top-level
	// binding (using the file scope would mis-resolve a shadowed name).
	bodyScopes map[ast.PNode]*Scope

	// checkScope is the scope the gradual checker currently infers shapes
	// against — the file scope at top level, a function body's scope while
	// checking inside it. Swapped (save/restore) as the checker descends.
	checkScope *Scope

	// tvarBoundStruct maps a generic type variable (interned) to the NAME of the
	// struct that bounds it, when the bound is a struct. The variable's bound
	// PhoType is a structural record (fields only); the name reaches the nominal
	// struct's method table so bound METHOD access `(b.method …)` resolves.
	tvarBoundStruct map[*core.PhoType]string
}

func newWalker(file string) *walker {
	return &walker{file: file, usedImports: map[string]bool{}, bodyScopes: map[ast.PNode]*Scope{}}
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
	w.inMacroLib = isMacroLibFile(w.file)
	w.collect(fileScope, tree)
	// Every inline type signature must have a matching implementation.
	w.checkMissingImpls(fileScope)
	// Clause-set layout + exhaustiveness + const-dispatch call sites
	// (Features.md §1/§2/§9). After collect so cross-file sigs are visible.
	w.checkClauses(fileScope, tree)
	// Record struct-typed fields' owners so shape inference can navigate through
	// them (recursive/nested member access). Must precede the reference walk.
	w.harvestFieldShapes(fileScope, tree)
	// Harvest each method's `--@ (~methodsig …)` onto its owner's member
	// surface, now that collect has created the structInfos. The gradual
	// checker reads these to type method calls (Coordination §3).
	w.harvestMethodSigs(fileScope, tree)
	// Record shapes for top-level var/const initializers before any
	// checking, so function bodies walked earlier in the file see the
	// shapes of bindings declared later.
	w.assignDeclShapes(fileScope, tree)
	for _, form := range tree {
		w.checkExpr(fileScope, form, false /* not in body code */)
	}
	w.walkAnnotations(tree)
	w.checkTypes(tree)
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

// checkMissingImpls flags an inline type SIGNATURE collected in this file that
// has no matching implementation — `(fun add (Number Number) Number)` with no
// `(fun add (a b) …)`. Matching is by qualified name through the full scope
// chain, so an implementation in a sibling file of the same package satisfies
// it. Phase 2 of the inline type-signature plan (TypeSignatures.md §4).
func (w *walker) checkMissingImpls(scope *Scope) {
	for _, s := range w.sigSites {
		if def, _, ok := scope.Lookup(s.Name); ok {
			if def.Kind == s.Kind {
				continue
			}
			// A 0-argument function's implementation is a value binding
			// `(let name = body)`: the collector records it as a const/var, but it
			// implements the function signature (the runtime binds it as a 0-arg
			// function via isZeroArgClause).
			if s.Kind == DefFun && (def.Kind == DefConst || def.Kind == DefVar) {
				continue
			}
		}
		kind := "function"
		if s.Kind == DefMethod {
			kind = "method"
		}
		w.emit(Diagnostic{
			File:     w.file,
			Span:     s.Span,
			Severity: SeverityError,
			Code:     "missing-implementation",
			Message:  fmt.Sprintf("%s signature '%s' has no implementation", kind, s.Name),
		})
	}
}

// walkAnnotations evaluates the parse-time annotations attached to each
// top-level form (`--@ (~sig ...)` and friends) through pkg/annot's
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
		switch {
		case d.Name == "":
			// anonymous — nothing to collect
		case d.IsSig:
			// A type SIGNATURE binds nothing; record it so checkMissingImpls
			// can require a matching implementation (Phase 2), and mark the
			// scope so the clause checker sees cross-file signatures.
			w.sigSites = append(w.sigSites, sigSite{d.Name, d.NameSpan, DefFun})
			scope.markSig(d.Name)
		case d.IsClause:
			// Implementation CLAUSES of one function coexist (Features.md §1):
			// define the name once; later clauses attach to the same binding.
			if _, _, exists := scope.Lookup(d.Name); !exists {
				w.define(scope, d.Name, DefFun, d.NameSpan)
			}
		default:
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
		if d.Owner != "" && d.Name != "" && d.IsSig {
			// A method type SIGNATURE binds nothing — record it so
			// checkMissingImpls can require a matching implementation (Phase 2).
			// (Effect checks read the sig's receiver mutability straight off the
			// clause set's adjacentSig — see checkClauseSetEffects.)
			w.sigSites = append(w.sigSites, sigSite{d.Owner + "." + d.Name, d.NameSpan, DefMethod})
			scope.markSig(d.Owner + "." + d.Name)
		}
		if d.Owner != "" && d.Name != "" && !d.IsSig {
			full := d.Owner + "." + d.Name
			// Implementation CLAUSES of one method coexist (Features.md §1):
			// define the qualified name once; later clauses attach to the same
			// entry. A (retired) combined method takes the ordinary path, where
			// a second definition is a redeclaration.
			if d.IsClause {
				if _, _, exists := scope.Lookup(full); !exists {
					w.define(scope, full, DefMethod, d.NameSpan)
				}
			} else {
				w.define(scope, full, DefMethod, d.NameSpan)
			}
			// Attach to the owner's member table. Lookup first: the
			// struct may have been collected into an outer (package)
			// scope by a sibling file, and splitting its table across
			// scopes would lose members. A union receiver (Collection =
			// String|List|Map) registers on EACH member's surface,
			// mirroring the runtime's union-receiver expansion, so an
			// access on a concrete list/string/map resolves.
			for _, owner := range memberOwners(d.Owner) {
				si, ok := scope.LookupStruct(owner)
				if !ok {
					si = scope.structAt(owner)
				}
				si.Methods[d.Name] = d.NameSpan
				if si.MethodFiles == nil {
					si.MethodFiles = map[string]string{}
				}
				si.MethodFiles[d.Name] = w.file
			}
		}

	case "property":
		// A struct-field property `(property Recv.Name …)` is a computed
		// member — register it on the owner's table (like a field) so
		// `inst.Name` resolves. A free-standing `(property Name …)` is a
		// faux variable backed by getter/setter delegates — register it as
		// DefVar so a reference reads (and highlights) as a plain variable.
		switch {
		case d.Owner != "" && d.Name != "":
			// A property on a built-in type (a primitive, or a union like
			// Collection expanded across its members) is a named member —
			// register it on the member surface the primitive-member check
			// reads (Methods). A struct property is a field, reached via the
			// instance-member check.
			_, isType := core.TypeByName(d.Owner)
			for _, owner := range memberOwners(d.Owner) {
				si, ok := scope.LookupStruct(owner)
				if !ok {
					si = scope.structAt(owner)
				}
				if isType {
					si.Methods[d.Name] = d.NameSpan
				} else {
					si.Fields[d.Name] = d.NameSpan
				}
			}
		case d.Name != "":
			w.define(scope, d.Name, DefVar, d.NameSpan)
		}

	case "static":
		// A type-level member registers on the owner's STATIC surface (reached
		// via the type value `Point.At`), not the instance member table. The
		// receiver-qualified key flags the one redeclaration worth catching — the
		// same static member declared twice on the same owner.
		if d.Owner != "" && d.Name != "" {
			w.define(scope, d.Owner+".static."+d.Name, DefMethod, d.NameSpan)
			// A static-method SIGNATURE is the declared interface a
			// `(let Owner.name …)` clause attaches to — mark it so the clause
			// checker finds it under the same qualified key as instance sigs.
			if d.Sub == "method" && d.IsSig {
				scope.markSig(d.Owner + "." + d.Name)
			}
			for _, owner := range memberOwners(d.Owner) {
				si, ok := scope.LookupStruct(owner)
				if !ok {
					si = scope.structAt(owner)
				}
				if si.StaticMembers == nil {
					si.StaticMembers = map[string]span.Span{}
				}
				si.StaticMembers[d.Name] = d.NameSpan
			}
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

	case "type":
		// (type Name T) — a named type alias binds Name as a constant KindType.
		if d.Name != "" {
			w.define(scope, d.Name, DefType, d.NameSpan)
		}

	case "trait":
		// (trait Name …) — a named trait binds Name as a constant KindType, like
		// the `(type Name (Trait …))` it shorthands.
		if d.Name != "" {
			w.define(scope, d.Name, DefType, d.NameSpan)
		}

	case "template":
		// Type parameters become gradual type names so references to them in the
		// following struct/method (field types, receiver, sig slots) resolve.
		// Defined idempotently: the same parameter name recurs across templates
		// and must not read as a redeclaration. Scoping is loose for Phase 1
		// (file-wide); since the names are gradual, the imprecision is harmless.
		for _, p := range d.TemplateParams {
			if _, _, found := scope.Lookup(p.Name); !found {
				w.define(scope, p.Name, DefType, p.Span)
			}
		}

	case "const":
		for _, b := range d.Binds {
			// A plain `let` binds const, but a per-binder `(var x)` in a
			// destructuring target is reassignable.
			kind := DefConst
			if b.Mutable {
				kind = DefVar
			}
			w.define(scope, b.Name, kind, b.Span)
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
	// `goimport` paths aren't on the Pho filesystem — leave Path empty. A Pho
	// `import` resolves its path up front so the entry (and the onDefine hit
	// navigation/hover reads) carry it from the start.
	resolved := ""
	if !isGoImport {
		resolved = w.resolveImport(path)
	}
	w.defineFull(scope, alias, DefImport, span, resolved)
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
	w.defineFull(scope, name, kind, span, "")
}

// defineFull is define with an explicit Definition.Path — set on the entry
// BEFORE the onDefine hook fires, so navigation and hover see it. Imports
// resolve their path through here rather than patching it on afterward (which
// left the onDefine hit, and thus the import's hover, with an empty path).
func (w *walker) defineFull(scope *Scope, name string, kind DefKind, span span.Span, path string) {
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

	def := Definition{Name: name, Kind: kind, Span: span, File: w.file, Path: path}
	scope.Defs[name] = def
	if w.onDefine != nil {
		w.onDefine(span, def)
	}

	if !existed {
		return
	}

	switch {
	case prior.Kind == DefBuiltin:
		if w.inMacroLib {
			// The macro library intentionally shadows builtins (e.g. its `type`
			// fun backs the `~type` annotation); the runtime permits this.
			break
		}
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
		if core.IsStrLit(node.Value) {
			body := core.StrLitBody(node.Value)
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
				Message:  fmt.Sprintf("'%s' is not defined", node.Value) + arrayDictHint(node.Value),
			})
			return
		}
		if w.onLeafResolve != nil {
			w.onLeafResolve(node.Span, def)
		}
		if def.Kind == DefImport {
			w.usedImports[node.Value] = true
		}
		// §10: a reference that resolves to a type (a struct or a named type
		// alias/trait) must spell that type in Title-Kebab-Case — this catches
		// type references in every walked position (`.is?`/`.as`, `subtype?`,
		// construction, type-alias RHS, pattern type tests). Signature slots are
		// erased and unwalked, so they get a separate pass (checkSigTypeNames).
		if def.Kind == DefStruct || def.Kind == DefType {
			w.checkTypeName(node.Value, node.Span)
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

	case *ast.PSlash:
		// `pkg/member` (or nested `pkg/sub/member`) — package/subpackage
		// navigation. Walking the LHS resolves the alias (marking the import
		// used); checkPackageSlash resolves a one-level `alias/name` export
		// for hover/nav and stays tolerant of subpackages.
		w.checkExpr(scope, node.LHS, inCode)
		w.checkPackageSlash(scope, node)

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
					Message:  fmt.Sprintf("'%s' is a %s, not a macro — call it without the '~' prefix", leaf.Value, def.Kind),
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

// checkPackageSlash resolves a one-level `alias/name` package navigation for
// hover / go-to-definition, without false-flagging: `name` may be an export OR
// a subpackage. When the LHS is a bare import alias and `name` is a known
// export, it fires onExportResolve; anything else (a non-export name — possibly
// a subpackage — or a nested `a/b/c` LHS whose deeper package the linter does
// not statically resolve) is deferred to the runtime rather than risk a false
// diagnostic.
func (w *walker) checkPackageSlash(scope *Scope, sl *ast.PSlash) {
	leaf, ok := sl.LHS.(*ast.PLeaf)
	if !ok {
		return
	}
	def, _, found := scope.Lookup(leaf.Value)
	if !found || def.Kind != DefImport || def.Path == "" {
		return
	}
	rhs, ok := sl.RHS.(*ast.PLeaf)
	if !ok {
		return
	}
	exports := w.exportsFor(def.Path)
	if exports == nil {
		// Can't read the package — stay quiet (mirrors checkPackageMember).
		return
	}
	if export, ok := exports[rhs.Value]; ok {
		if w.onExportResolve != nil {
			w.onExportResolve(rhs.Span, export)
		}
		return
	}
	// Not an export. It may be a SUBPACKAGE directory (`pkg/sub/…`), navigated at
	// runtime and not statically resolved here — leave those alone. A name that is
	// neither an export nor a subpackage is a real error.
	if isSubpackage(def.Path, rhs.Value) {
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
		w.checkUnreachable(body)
		return
	}

	switch head {
	case "fun":
		w.checkComboImpl(br)
		w.checkFun(scope, br)
	case "macro":
		// A macro body is reference-checked exactly like a fun body —
		// declOf hands back its param list and body the same way.
		w.checkFun(scope, br)
	case "method", "operator":
		// `operator` is an operator-overload signature — structurally a method
		// sig (declOf normalizes its Head to "method"), so it is walked the same.
		w.checkComboImpl(br)
		w.checkMethod(scope, br)
	case "lambda", "lambda?", "lambda!", "lambda=", "lambda?!", "lambda?=", "lambda!=", "lambda?!=":
		w.checkLambda(scope, br)
		return
	case "property":
		w.checkProperty(scope, br)
	case "static":
		w.checkStatic(scope, br)
	case "struct":
		// (struct Name f0 f1 …) — name + fields are declarations,
		// nothing to reference-check beyond the §10 type-name rule.
		if d, ok := declOf(br); ok {
			w.checkTypeName(d.Name, d.NameSpan)
		}
		return
	case "template":
		// (template P (Bound Q) …) — a type-parameter declaration for the
		// following form. Its children are type parameters and bounds
		// (declarations / type positions), not value references; the parameter
		// names are types and obey the §10 kebab rule.
		if d, ok := declOf(br); ok {
			for _, p := range d.TemplateParams {
				w.checkTypeName(p.Name, p.Span)
			}
		}
		return
	case "type":
		// (type Name T) — Name is a declaration; T is a type expression that
		// may reference builtin type names, connectives, and other aliases.
		if d, ok := declOf(br); ok {
			w.checkTypeName(d.Name, d.NameSpan)
		}
		if len(br.Children) >= 3 {
			w.checkExpr(scope, br.Children[2], true)
		}
	case "Trait", "trait":
		if d, ok := declOf(br); ok {
			w.checkTypeName(d.Name, d.NameSpan)
		}
		w.checkTrait(scope, br)
	case "select":
		// (select scrutinee case PAT -> RESULT …) — the match expression
		// (Features.md §3). The scrutinee is code; each case's pattern BINDERS
		// scope to that case's result; `case`/`->` are structural markers.
		if len(br.Children) >= 2 {
			w.checkExpr(scope, br.Children[1], true)
		}
		for i := 2; i+3 < len(br.Children); i += 4 {
			kw, kwOK := br.Children[i].(*ast.PLeaf)
			arrow, arOK := br.Children[i+2].(*ast.PLeaf)
			if !kwOK || kw.Value != "case" || !arOK || arrow.Value != "->" {
				w.emit(Diagnostic{
					File: w.file, Span: br.Children[i].GetSpan(), Severity: SeverityError, Code: "bad-select",
					Message: "'select' is written (select value case pattern -> result …)",
				})
				return
			}
			caseScope := newScope(scope)
			w.collectPatternBinders(caseScope, br.Children[i+1], true)
			w.checkExpr(caseScope, br.Children[i+3], true)
		}
		return

	case "var", "const", "let":
		// A `(let name (patterns) [where g] = body)` IMPLEMENTATION CLAUSE
		// (declOf normalizes it to Head fun/method) routes to the fun/method
		// walkers — patterns bind, the guard and body walk in the clause scope.
		if d, ok := declOf(br); ok && d.IsClause {
			if d.Head == "method" {
				w.checkMethod(scope, br)
			} else {
				w.checkFun(scope, br)
			}
			return
		}
		// names are declarations; values are expressions that may reference
		// other names. declOf normalizes the `let` triple form (name = value)
		// to the same binds as const/var pairs.
		decl, _ := declOf(br)
		for _, b := range decl.Binds {
			// A destructuring target attaches its RHS to the first binder only;
			// the extra binders carry no value.
			if b.Value != nil {
				w.checkExpr(scope, b.Value, true)
			}
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
		w.checkUnreachable(br.Children[1:])
	case "=":
		// `=` is reassignment only: `(= target value)`. The old define form
		// `(= name (params) body)` is not recognized (the shape checker flags its
		// arity); an implementation is a `let` clause `(let name (params) = body)`.
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
				Message:  "'break' is only valid inside a loop (foreach / while / until)",
			})
		}
	case "continue":
		if !w.inLoop {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     br.Children[0].GetSpan(),
				Severity: SeverityError,
				Code:     "continue-outside-loop",
				Message:  "'continue' is only valid inside a loop (foreach / while / until)",
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
						Code:     "macro-needs-prefix",
						Message:  fmt.Sprintf("'%s' is a macro — call it with the '~' prefix: (~%s ...)", leaf.Value, leaf.Value),
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
		// `T.{ field value … }` construction: every literal field name must be a
		// declared field of T (the runtime rejects an unknown key).
		w.checkConstruction(scope, br)
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

// headStruct resolves a call head to the struct it constructs — a local
// `(struct …)` name or an imported `pkg.Struct` — returning the struct's
// field/method tables and a display name. Used to validate construction.
func (w *walker) headStruct(scope *Scope, head ast.PNode) (*structInfo, string, bool) {
	switch h := head.(type) {
	case *ast.PLeaf:
		if !looksLikeIdentifier(h.Value) {
			return nil, "", false
		}
		if def, _, found := scope.Lookup(h.Value); found && def.Kind == DefStruct {
			if si, ok := scope.LookupStruct(h.Value); ok {
				return si, h.Value, true
			}
		}
	case *ast.PDot:
		alias, aok := h.LHS.(*ast.PLeaf)
		member, mok := h.RHS.(*ast.PLeaf)
		if !aok || !mok {
			return nil, "", false
		}
		def, _, found := scope.Lookup(alias.Value)
		if !found || def.Kind != DefImport || def.Path == "" {
			return nil, "", false
		}
		if si, ok := w.structsFor(def.Path)[member.Value]; ok {
			return si, alias.Value + "." + member.Value, true
		}
	}
	return nil, "", false
}

// checkConstruction flags an unknown field name in a `T.{ field value … }`
// construction. The sugar lowers (at parse time) to a plain call
// `(T 'field' value …)` with the field names as string literals, so this runs
// in the generic-call path: when the head names a struct, every literal
// field-name argument (the odd-position children) must be a declared field of
// that struct — the runtime rejects an unknown key (see builtins/decl.go).
func (w *walker) checkConstruction(scope *Scope, br *ast.PBranch) {
	if br.Open != "(" || len(br.Children) < 2 {
		return
	}
	si, name, ok := w.headStruct(scope, br.Children[0])
	if !ok {
		return
	}
	seen := map[string]bool{}
	for i := 1; i < len(br.Children); i += 2 {
		field, isStr := stringLiteral(br.Children[i])
		if !isStr {
			continue // dynamic/non-literal key (unusual) — leave to the runtime
		}
		switch {
		case seen[field]:
			w.emit(Diagnostic{
				File:     w.file,
				Span:     br.Children[i].GetSpan(),
				Severity: SeverityWarning,
				Code:     "duplicate-field",
				Message:  fmt.Sprintf("field '%s' is set more than once; the last value wins", field),
			})
		case !fieldDeclared(si, field):
			w.emit(Diagnostic{
				File:     w.file,
				Span:     br.Children[i].GetSpan(),
				Severity: SeverityError,
				Code:     "unknown-field",
				Message:  fmt.Sprintf("'%s' has no field '%s'", name, field),
			})
		}
		seen[field] = true
	}
}

// fieldDeclared reports whether a struct declares the named field (comma-ok so a
// field whose recorded span is the zero value still counts as present).
func fieldDeclared(si *structInfo, field string) bool {
	_, ok := si.Fields[field]
	return ok
}

// checkUnreachable flags statements that follow an UNCONDITIONAL control-flow
// exit — a bare `(return …)` / `(break)` / `(continue)` at the top of a
// do-sequence — since they can never run. Reported once (Warning), at the
// first dead statement. A `return` nested inside an `(if …)` arm is conditional
// and does not trigger this.
func (w *walker) checkUnreachable(body []ast.PNode) {
	for i := 0; i < len(body)-1; i++ {
		if kw, ok := unconditionalExit(body[i]); ok {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     body[i+1].GetSpan(),
				Severity: SeverityWarning,
				Code:     "unreachable-code",
				Message:  fmt.Sprintf("unreachable code: the preceding '%s' always exits", kw),
			})
			return
		}
	}
}

// unconditionalExit reports whether n is a direct (return …)/(break)/(continue)
// call, returning the head keyword.
func unconditionalExit(n ast.PNode) (string, bool) {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "(" || len(br.Children) == 0 {
		return "", false
	}
	h, ok := br.Children[0].(*ast.PLeaf)
	if ok && (h.Value == "return" || h.Value == "break" || h.Value == "continue") {
		return h.Value, true
	}
	return "", false
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

// checkComboImpl rejects the OLD combined signature+implementation form
// `(fun name (params) body)` / `(method Owner.name (params) body)`. Under the
// clause system, `fun`/`method` declare only SIGNATURES (all-type params + a
// type return); the implementation is one or more `(let name (patterns) = body)`
// clauses (Features.md §1). So a NAMED `fun`/`method` with a value (non-type)
// parameter list is the pre-split combined form and is no longer valid — the
// writer must split it.
//
// Only literal `fun`/`method` forms reach here (the clause form dispatches
// separately). A signature (IsSig) and the anonymous `fun`/method delegate
// forms (no Name) are left alone.
func (w *walker) checkComboImpl(br *ast.PBranch) {
	d, ok := declOf(br)
	if !ok || d.IsSig || d.Name == "" || d.ArgList == nil || d.Body == nil {
		return
	}
	impl := d.Name
	if d.Owner != "" {
		impl = d.Owner + "." + d.Name
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     headSpan(br),
		Severity: SeverityError,
		Code:     "combined-impl",
		Message: fmt.Sprintf("'%s' declares a signature only; write the implementation as '(let %s (params) = body)'",
			d.Head, impl),
	})
}

// checkFun walks (fun name (args) body) or (fun (args) body).
func (w *walker) checkFun(scope *Scope, br *ast.PBranch) {
	d, _ := declOf(br)
	// A type SIGNATURE is erased in Phase 1: its "param list" is a list of
	// types and its "body" is the return type, so it must NOT be walked as an
	// implementation (that would collect the types as params and reference-
	// check them as code). See decls.go IsSig.
	if d.IsSig {
		w.checkVarParams(d.ArgList, true) // (var Self) receiver only
		w.checkSigTypeNames(d)            // §10: type slots + result are Title-Kebab
		return
	}
	if d.ArgList == nil || d.Body == nil {
		return
	}
	prevClause := w.clauseParams
	w.clauseParams = d.IsClause
	w.walkFunctionBody(scope, d.ArgList, d.Body, "" /* not a method */)
	w.clauseParams = prevClause
	w.walkClauseGuard(d)
	w.checkClauseGuardEffect(scope, d)
	// The `!`/`=` name contract is checked once per callable, over the UNION of
	// its clauses and anchored on its signature — see checkClauseSetEffects,
	// driven from checkClauses. (A per-clause check here would falsely flag a
	// pure clause of an otherwise-effectful function.)
}

// walkClauseGuard reference-checks a clause's `where` guard in the clause's
// own body scope (params + patterns bound) — stashed by walkFunctionBody.
func (w *walker) walkClauseGuard(d topLevelDecl) {
	if d.Guard == nil {
		return
	}
	if bs, ok := w.bodyScopes[d.Body]; ok && bs != nil {
		w.checkExpr(bs, d.Guard, true)
	}
}

// checkProperty walks (property <Receiver.>Name (get (params) body)
// [(set (params) body)]). For a struct-field property the receiver (the dot's
// LHS) is a reference; the field name (the dot's RHS) and a free-standing bare
// Name are declarations, not references. Each accessor body is walked with its
// params (+ `self` for a struct property) bound.
func (w *walker) checkProperty(scope *Scope, br *ast.PBranch) {
	// The receiver (dot LHS) is a real reference.
	if len(br.Children) >= 2 {
		if dot, ok := br.Children[1].(*ast.PDot); ok {
			w.checkExpr(scope, dot.LHS, true)
		}
	}

	// (property Target (get (params) body) [(set (params) body)]) — walk each
	// accessor body in its own scope binding its params (+ `self` for a struct
	// property). A getter is auto-invoked on read, so it must be pure. A getter
	// slot that is NOT a `(get …)` accessor is the rejected old flat form, already
	// flagged by checkSpecialFormShape; nothing more to walk.
	if len(br.Children) < 3 || !isAccessorSublist(br.Children[2], "get") {
		return
	}
	d, _ := declOf(br)
	for _, ch := range br.Children[2:] {
		acc, ok := asList(ch)
		if !ok || len(acc.Children) != 3 {
			continue
		}
		params, body := acc.Children[1], acc.Children[2]
		w.walkFunctionBody(scope, params, body, d.Owner)
		if h, ok := acc.Children[0].(*ast.PLeaf); ok && h.Value == "get" {
			w.checkPureBody(scope, params, body, "a property getter", br.Children[1].GetSpan())
		}
	}
}

// isAccessorSublist reports whether n is a `(get …)` / `(set …)` property-delegate
// sub-form (a list headed by the keyword) — the property accessor syntax.
func isAccessorSublist(n ast.PNode, kw string) bool {
	br, ok := asList(n)
	if !ok || len(br.Children) < 1 {
		return false
	}
	h, ok := br.Children[0].(*ast.PLeaf)
	return ok && h.Value == kw
}

// checkPureBody is checkPureContext for an accessor whose params/body are already
// extracted (the new `(get …)` form, which declOf doesn't wrap into a decl).
func (w *walker) checkPureBody(scope *Scope, argList, body ast.PNode, what string, at span.Span) {
	if !EffectCheck || body == nil {
		return
	}
	r := scanEffects(body, true, varParamNames(argList), freeVarClassifier(scope, argList, body))
	if !r.set.pure() {
		w.emit(Diagnostic{
			File:     w.file,
			Span:     at,
			Severity: SeverityError,
			Code:     "effect-in-pure-context",
			Message:  fmt.Sprintf("%s must be pure, but has an effect (%s)", what, r.set),
		})
	}
}

// checkMethod walks (method Owner.name (args) body).
func (w *walker) checkMethod(scope *Scope, br *ast.PBranch) {
	d, _ := declOf(br)
	// A method type SIGNATURE is erased in Phase 1 — its param list is types
	// and its "body" is the return type, so it must not be walked as an
	// implementation. See decls.go IsSig.
	if d.IsSig {
		w.checkVarParams(d.ArgList, true) // (var Self) receiver only
		w.checkVarSelfNeedsEquals(d)      // (var Self) ⟹ name ends in '='
		w.checkSigTypeNames(d)            // §10: type slots + result are Title-Kebab
		// The receiver type named in the dotted head (String.to) is a type
		// reference too; the erased-sig path never walks it, so check it here.
		if dot, ok := br.Children[1].(*ast.PDot); ok {
			if lhs, ok := dot.LHS.(*ast.PLeaf); ok {
				w.checkTypeName(lhs.Value, lhs.Span)
			}
		}
		return
	}
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
	prevClause := w.clauseParams
	w.clauseParams = d.IsClause
	w.walkFunctionBody(scope, d.ArgList, d.Body, d.Owner)
	w.clauseParams = prevClause
	w.walkClauseGuard(d)
	w.checkClauseGuardEffect(scope, d)
	// The `!`/`=` name contract (including receiver mutability from the sig's
	// `(var Self)`) is checked once per callable, over the UNION of its clauses
	// and anchored on its signature — see checkClauseSetEffects, driven from
	// checkClauses.
}

// checkStatic walks a `(static method Recv.Name (args) body)` or `(static
// property Recv.Name (get (self) body) [(set (self v) body)])` declaration. The
// receiver names the owning struct (a reference, checked). A static method's
// body sees the explicit params plus `Self` (the receiver TYPE); a static
// property's accessors are `(get …)`/`(set …)` sub-forms whose `self` is the
// receiver type. The old flat `get getter [set setter]` form is rejected.
func (w *walker) checkStatic(scope *Scope, br *ast.PBranch) {
	d, ok := declOf(br)
	if !ok {
		return
	}
	if len(br.Children) >= 3 {
		if dot, ok := br.Children[2].(*ast.PDot); ok {
			w.checkExpr(scope, dot.LHS, true)
		}
	}
	// A static method SIGNATURE `(static method Recv.M (T…) R)` binds nothing and
	// has no body to check — its param/return slots are TYPES, not code. Walking
	// it as an implementation would misread each type as a parameter (firing
	// capitalized-param on `String`/`Atom`). The plain-method walk skips a sig the
	// same way (see decls.go IsSig).
	if d.IsSig {
		return
	}
	switch d.Sub {
	case "method":
		if d.ArgList != nil && d.Body != nil {
			w.walkStaticBody(scope, d.ArgList, d.Body, d.Owner)
		}
	case "property":
		if len(br.Children) < 4 || !isAccessorSublist(br.Children[3], "get") {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     headSpan(br),
				Severity: SeverityError,
				Code:     "bad-form-shape",
				Message:  "'static property' takes (Recv.Name (get (params) body) [(set (params) body)]); the getter must be a (get …) accessor",
			})
			return
		}
		for _, c := range br.Children[3:] {
			acc, ok := asList(c)
			if !ok || len(acc.Children) != 3 {
				continue
			}
			// `self` in the accessor body is the receiver TYPE; pass the owner so
			// it is allowed and resolves.
			w.walkFunctionBody(scope, acc.Children[1], acc.Children[2], d.Owner)
		}
	}
}

// walkStaticBody walks a static method body with the explicit params plus
// `Self` (the receiver type) in scope. It reuses walkFunctionBody by prepending
// a synthetic `Self` parameter, so `Self` and `Self.{ … }` resolve; no owner is
// passed because a static method has no `self` instance.
func (w *walker) walkStaticBody(scope *Scope, argList, body ast.PNode, owner string) {
	items := []ast.PNode{&ast.PLeaf{Value: "self", Span: argList.GetSpan()}}
	if br, ok := argList.(*ast.PBranch); ok {
		items = append(items, br.Children...)
	}
	augmented := &ast.PBranch{Open: "(", Close: ")", Children: items, Span: argList.GetSpan()}
	// Passing the owner sets inMethod so `self` (the receiver type) is allowed
	// in the body; a static method has no instance, but the privileged shape is
	// harmless for reference checks.
	w.walkFunctionBody(scope, augmented, body, owner)
}

// checkTrait walks a `(Trait (extends…) member…)` form. The extends-list
// entries are trait references (resolved); the member `(method Self.Name …)` /
// `(property Self.Name get…)` forms are SIGNATURES whose `Self` receiver,
// parameter names, and get/set keywords are declarations — not references — so
// they are not reference-checked. A default body, when present, IS walked (with
// `Self` as the receiver owner, so `self` is allowed and resolves loosely).
func (w *walker) checkTrait(scope *Scope, br *ast.PBranch) {
	extends, members := traitFormParts(br)
	if extends != nil {
		for _, ref := range extends.Children {
			w.checkExpr(scope, ref, true)
		}
	}
	for _, sub := range members {
		sb, ok := sub.(*ast.PBranch)
		if !ok || sb.Open != "(" {
			continue
		}
		switch headIdent(sb) {
		case "method":
			if len(sb.Children) >= 4 { // (method Self.Name (args) body) — a default
				w.walkFunctionBody(scope, sb.Children[2], sb.Children[3], "self")
			}
		case "property":
			// (property Self.Name get [impl] [set [impl]]) — each impl is a
			// (method <recv> (args) body) default.
			for _, c := range sb.Children[2:] {
				if cb, ok := c.(*ast.PBranch); ok && cb.Open == "(" && headIdent(cb) == "method" && len(cb.Children) >= 4 {
					w.walkFunctionBody(scope, cb.Children[2], cb.Children[3], "self")
				}
			}
			// `static` members are signature-only requirements whose `Self` receiver
			// is a placeholder — not reference-checked, like the instance signatures.
		}
	}
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

	// Stash the body scope so the gradual checker (a later pass) can type-check
	// inside this body with the same params/locals and shapes the reference walk
	// resolves against — keyed by the body node it'll look up.
	if body != nil {
		w.bodyScopes[body] = bodyScope
	}

	// This body's own scope is where reassignment may soundly retarget a
	// shape (see checkAssign). Restore on exit so a nested fun/method
	// hands control back to its enclosing body's scope.
	prevBody := w.bodyScope
	w.bodyScope = bodyScope
	defer func() { w.bodyScope = prevBody }()

	var paramItems []ast.PNode
	if items, ok := declList(argList); ok {
		paramItems = items
		for _, item := range items {
			w.collectParam(bodyScope, item)
		}
	}

	// A `(var …)` param may only be the receiver `(var self)` in an impl — a
	// value parameter cannot be declared mutable.
	w.checkVarParams(argList, false)

	if owner != "" {
		// Shape the receiver `self` from the owner type. A struct owner makes
		// it a privileged instance; a built-in CONCRETE type (List/String/Map/
		// Number/…) makes it that type's value — so `self.[i]` is a valid
		// index and `self.Size` resolves, instead of being checked as
		// struct-field access (a struct is not indexable). A composite/abstract
		// owner (Collection, Unknown) stays Unknown. See selfShapeForOwner.
		if d, ok := bodyScope.Defs["self"]; ok {
			d.Shape = selfShapeForOwner(parent, owner)
			bodyScope.Defs["self"] = d
		}
	}

	// Walk any (or name default) DEFAULT expressions now that every parameter
	// (and a shaped self) is in scope — matching the runtime's body-scope
	// evaluation of defaults.
	for _, item := range paramItems {
		w.walkParamDefault(bodyScope, item)
	}

	// The body is a single expression — at runtime BindFun evaluates it as one
	// form. Walk it directly so special forms (do / if / for / =) get their
	// dispatch in checkBranch. A `(do …)` / `(identity do …)` body hoists its
	// own statement scope in the "do" case.
	w.checkExpr(bodyScope, body, true)
}

// collectParam handles a single entry in a parameter list.
//
//	identifier            — bound as a regular parameter
//	(spread name)         — name bound, captures rest-args
//	(optional name)       — name bound, omittable (defaults to Nil)
func (w *walker) collectParam(scope *Scope, item ast.PNode) {
	if w.clauseParams {
		w.collectPatternBinders(scope, item, true)
		return
	}
	if leaf, ok := item.(*ast.PLeaf); ok {
		if looksLikeIdentifier(leaf.Value) {
			w.flagCapitalizedParam(leaf)
			w.define(scope, leaf.Value, DefParam, leaf.Span)
		}
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

	// (or name default) — a DEFAULTED optional: binds `name`; when the argument
	// is `none` it takes `default` instead. The DEFAULT expression is walked
	// separately (walkParamDefault), once every param is in scope.
	if h.Value == "or" {
		if len(br.Children) != 3 {
			w.emit(Diagnostic{
				File: w.file, Span: br.Span, Severity: SeverityError, Code: "bad-default-param",
				Message: "a defaulted parameter is written '(or name default)'",
			})
			return
		}
		name, ok := br.Children[1].(*ast.PLeaf)
		if !ok || !looksLikeIdentifier(name.Value) {
			w.emit(Diagnostic{
				File: w.file, Span: br.Children[1].GetSpan(), Severity: SeverityError, Code: "bad-default-param",
				Message: "a defaulted parameter needs a plain name: '(or name default)'",
			})
			return
		}
		w.flagCapitalizedParam(name)
		w.define(scope, name.Value, DefParam, name.Span)
		return
	}

	if len(br.Children) != 2 {
		return
	}
	// RETIRED: `(disc X)` — dispatch moved to `(const T)` signature slots +
	// literal patterns in the clauses (Features.md). Point the writer there.
	if h.Value == "disc" {
		w.emit(Diagnostic{
			File: w.file, Span: br.Span, Severity: SeverityError, Code: "retired-disc",
			Message: "(disc X) is retired — declare the parameter (const T) in the signature and dispatch with literal patterns in the clauses",
		})
		return
	}
	if h.Value == "spread" || h.Value == "optional" || h.Value == "var" {
		if name, ok := br.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
			// A `(var name)` receiver/param is conventionally lowercase
			// `self`; don't run the capitalized-param check for it (the
			// sig form uses `(var Self)`, which is handled on the sig path).
			if h.Value != "var" {
				w.flagCapitalizedParam(name)
			}
			w.define(scope, name.Value, DefParam, name.Span)
		}
	}
}

// collectPatternBinders defines every BINDER a clause pattern introduces
// (Features.md §2): a lowercase leaf binds; literals (numbers, strings,
// atoms, bools, none, Capitalized type values) match and bind nothing; a
// `(var x)`/`(spread x)` wrapper binds x; a `(Type name)` type test binds
// name; `[p…]` list patterns and `Type.{ field = pat }` struct patterns
// (pre-desugared to `(Type 'field' pat …)`) recurse. top marks a top-level
// param slot (where var/spread wrappers are legal).
func (w *walker) collectPatternBinders(scope *Scope, item ast.PNode, top bool) {
	switch node := item.(type) {
	case *ast.PLeaf:
		v := node.Value
		if !looksLikeIdentifier(v) || v == "true" || v == "false" || v == "none" {
			return // a literal — matched, not bound
		}
		c := v[0]
		if c >= 'A' && c <= 'Z' {
			return // a type value used disc-style — matched by identity
		}
		w.define(scope, v, DefParam, node.Span)

	case *ast.PBranch:
		if node.Open == "[" {
			for _, ch := range node.Children {
				w.collectPatternBinders(scope, ch, false)
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
		// (var name) / (var Type name) — a mutable binder at ANY depth (the
		// self-mutation receiver at top, or a nested `(var x)` in a list/struct
		// pattern). The name is the last child; any type is erased.
		if head.Value == "var" && len(node.Children) >= 2 {
			if name, ok := node.Children[len(node.Children)-1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
				w.define(scope, name.Value, DefParam, name.Span)
			}
			return
		}
		// (spread name) — a top-level rest-param binder only.
		if top && head.Value == "spread" && len(node.Children) == 2 {
			if name, ok := node.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
				w.define(scope, name.Value, DefParam, name.Span)
			}
			return
		}
		// (name) — the capture operator: a parenthesized binder (mainly a struct
		// field key `(field)`, but accepted anywhere for uniformity).
		if len(node.Children) == 1 && looksLikeIdentifier(head.Value) && head.Value[0] >= 'a' && head.Value[0] <= 'z' {
			w.define(scope, head.Value, DefParam, head.Span)
			return
		}
		// The RETIRED param wrappers get pointed redirects (the runtime rejects
		// them too): `(disc X)` moved to `(const T)` in the signature; a default
		// `(or name D)` / an `(optional name)` slot moved to the signature's
		// `(optional Type [else default])`. Any other lowercase-headed branch
		// isn't a pattern (a pattern head must be a Capitalized type).
		if head.Value != "" && head.Value[0] >= 'a' && head.Value[0] <= 'z' {
			switch head.Value {
			case "disc":
				w.emit(Diagnostic{
					File: w.file, Span: node.Span, Severity: SeverityError, Code: "retired-disc",
					Message: "(disc X) is retired — declare the parameter (const T) in the signature and dispatch with literal patterns in the clauses",
				})
			case "or", "optional":
				w.emit(Diagnostic{
					File: w.file, Span: node.Span, Severity: SeverityError, Code: "bad-default-param",
					Message: "optional parameters and defaults are declared in the SIGNATURE — (optional Type [else default]) — the clause binds a plain name",
				})
				// Still bind the name so the body doesn't cascade unresolved errors.
				if len(node.Children) >= 2 {
					if name, ok := node.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) {
						w.define(scope, name.Value, DefParam, name.Span)
					}
				}
			default:
				w.emit(Diagnostic{
					File: w.file, Span: node.Span, Severity: SeverityError, Code: "bad-pattern",
					Message: "cannot read this as a pattern — expected a name, literal, [list], (Type name), or Type.{ field = pattern }",
				})
			}
			return
		}
		// (Type name) — type test; (Type 'field' pat …) — struct destructure.
		if len(node.Children) == 2 {
			if name, ok := node.Children[1].(*ast.PLeaf); ok && looksLikeIdentifier(name.Value) && name.Value[0] >= 'a' && name.Value[0] <= 'z' {
				w.define(scope, name.Value, DefParam, name.Span)
			}
			return
		}
		// (Type 'field' pat …) struct destructure: a key at an odd index is a
		// `'field'` string-lit selector (binds nothing) or a `(field)` capture
		// (binds the whole field value); the pattern at the next index recurses.
		for i := 1; i+1 < len(node.Children); i += 2 {
			if _, isStr := stringLiteral(node.Children[i]); !isStr {
				w.collectPatternBinders(scope, node.Children[i], false) // (field) capture
			}
			w.collectPatternBinders(scope, node.Children[i+1], false)
		}
	}
}

// walkParamDefault walks the DEFAULT expression of an `(or name default)`
// parameter. Called after every parameter is collected into the body scope, so
// the default's references resolve against its siblings and the closure —
// mirroring the runtime's body-scope evaluation (core.applyDefaults).
func (w *walker) walkParamDefault(scope *Scope, item ast.PNode) {
	br, ok := item.(*ast.PBranch)
	if !ok || br.Open != "(" || len(br.Children) != 3 {
		return
	}
	if h, ok := br.Children[0].(*ast.PLeaf); !ok || h.Value != "or" {
		return
	}
	w.checkExpr(scope, br.Children[2], true)
}

// flagCapitalizedParam enforces the §3 casing split: a Capitalized identifier
// used as an IMPLEMENTATION parameter name reads as a TYPE, so the form looks
// like a signature whose params accidentally have a body — flag it. (Sigs
// never reach here; checkFun/checkMethod skip them. The value literals
// Nil/True/False are excluded.) Phase 2 of the inline type-signature plan.
func (w *walker) flagCapitalizedParam(leaf *ast.PLeaf) {
	v := leaf.Value
	// Excluded: the value literals Nil/True/False, and `Self` — the
	// conventional capitalized receiver name in (static) method getters like
	// `(method Counter (Self) …)`, where Self is the receiver instance.
	if v == "" || v[0] < 'A' || v[0] > 'Z' || v == "Nil" || v == "True" || v == "False" || v == "Self" {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     leaf.Span,
		Severity: SeverityError,
		Code:     "capitalized-param",
		Message:  fmt.Sprintf("parameter '%s' is capitalized — a Capitalized name reads as a type; lowercase it, or (if you meant a type signature) drop the body", v),
	})
}

// checkTypeName enforces Features.md §10: a type's declared name must be
// Title-Kebab-Case (My-Type, Integer, Vec-3d), never PascalCase (MyType),
// SCREAMING, or lowercase-as-a-type. The reader-level rejection
// (syntax.StrictNames) stays off until the goimport re-casing lands (§12), so
// the linter is the enforcement surface: a bad name is flagged but still parses.
func (w *walker) checkTypeName(name string, sp span.Span) {
	if name == "" || syntax.ClassifyIdent(name) == syntax.IdentType {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     sp,
		Severity: SeverityError,
		Code:     "bad-type-name",
		Message:  fmt.Sprintf("type name '%s' must be Title-Kebab-Case (e.g. My-Type) — PascalCase, acronyms, and lowercase type names are rejected", name),
	})
}

// checkTypeExpr flags every type NAME in a type-position expression (a
// signature slot or a result type) that is not Title-Kebab-Case (§10). Wrapper
// forms carry their type inside: (var Self), (spread T) and (const T) recurse
// into their operands; (optional T else DEFAULT) checks only T, since DEFAULT
// is an ordinary value. A capitalized head is a type constructor/connective
// (List, Map, Or, And), so it and its arguments are all types; a
// lowercase-headed `(` form is a keyword we don't model as a type and is left
// alone, so its keyword is never mistaken for a bad type name. A `[…]` form is
// a list/map type literal whose elements are types (its `->`/`:` separators
// aren't identifiers, so the leaf guard skips them).
func (w *walker) checkTypeExpr(n ast.PNode) {
	switch t := n.(type) {
	case *ast.PLeaf:
		if looksLikeIdentifier(t.Value) {
			w.checkTypeName(t.Value, t.Span)
		}
	case *ast.PBranch:
		switch t.Open {
		case "[":
			for _, c := range t.Children {
				w.checkTypeExpr(c)
			}
		case "(":
			switch head := headIdent(t); head {
			case "var", "spread", "const":
				for _, c := range t.Children[1:] {
					w.checkTypeExpr(c)
				}
			case "optional":
				if len(t.Children) >= 2 {
					w.checkTypeExpr(t.Children[1])
				}
			default:
				if head != "" && head[0] >= 'A' && head[0] <= 'Z' {
					for _, c := range t.Children {
						w.checkTypeExpr(c)
					}
				}
			}
		}
	}
}

// checkSigTypeNames enforces §10 across a type SIGNATURE's slots and result —
// the type references the erased-signature path (checkFun/checkMethod IsSig)
// never walks as expressions.
func (w *walker) checkSigTypeNames(d topLevelDecl) {
	if bl, ok := asList(d.ArgList); ok {
		for _, slot := range bl.Children {
			w.checkTypeExpr(slot)
		}
	}
	w.checkTypeExpr(d.Body)
}

// checkAssign handles `(= LHS RHS)`. The LHS may be a bare name (variable
// assignment) or a dot chain (struct-field write); only the bare-name case
// can fire set-on-constant.
func (w *walker) checkAssign(scope *Scope, br *ast.PBranch) {
	if len(br.Children) != 3 {
		return
	}
	lhs, rhs := br.Children[1], br.Children[2]

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

// arrayDictHint suggests the literal syntax when an unresolved identifier is
// the name of the mangled array/dict constructor — `slice` / `map`. Those are
// no longer callable builtins (the `[…]` and `{…}` literals are the only
// surface forms), so a bare `(slice …)` / `(map …)` resolves to nothing; the
// hint redirects the user to the literal instead of a bare "not defined".
// Returns "" for any other name.
func arrayDictHint(name string) string {
	switch name {
	case "slice":
		return " (arrays are written with brackets: [a b c])"
	case "map":
		return " (dicts are written with brackets: [k -> v])"
	}
	return ""
}
