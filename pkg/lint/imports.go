package lint

import (
	"os"
	"path/filepath"
	"unicode"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
	"pho/pkg/syntax"
)

// resolveImportPath resolves an import string the way the runtime
// effectively does. The CLI chdirs to the entry script's directory
// before evaluating (see main.go), so at runtime every import —
// "std/io" included — resolves from the directory the entry script
// lives in. Statically we don't know the entry script, so we use the
// convention that makes that work: try the importing file's own
// directory first (covers entry scripts), then each ancestor, nearest
// first (covers libraries importing "std/…" from the project's script
// root). A hit is a path that stats as a directory; hits come back
// absolute and cleaned so package identity is comparable across
// importing files.
//
// Falls back to the raw string when nothing resolves — that preserves
// the historical cwd-relative behavior for setups that relied on it.
// isMacroLibFile reports whether file lives in the annotation macro-library
// directory (the resolved "std/annot"), where shadowing builtins is permitted
// (the runtime loads that library with AllowShadow).
func isMacroLibFile(file string) bool {
	if file == "" {
		return false
	}
	dir := filepath.Dir(file)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dir == resolveImportPath(file, "std/annot")
}

func resolveImportPath(fromFile, importPath string) string {
	if fromFile == "" || importPath == "" || filepath.IsAbs(importPath) {
		return importPath
	}
	dir := filepath.Dir(fromFile)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		candidate := filepath.Join(dir, importPath)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return importPath
		}
		dir = parent
	}
}

// ResolveImport resolves importPath relative to fromFile by walking
// fromFile's directory and its ancestors (nearest first), returning the
// absolute directory when one stats as a dir, else importPath unchanged.
// Exposed for the LSP, which uses it to locate the annotation-macro library
// (std/annot) relative to the file being edited.
func ResolveImport(fromFile, importPath string) string {
	return resolveImportPath(fromFile, importPath)
}

// PackageExports reads the directory at `path` and returns the set of
// names that an `(import "path")` would expose to other packages.
// Mirrors modload's runtime export rule: a top-level fun, method, or
// struct whose name starts with an uppercase letter. Const decls
// don't qualify — the runtime's export pass refuses to export
// non-callable values, so a hypothetical `cards.PI` would always
// fail at runtime even if `(const 'PI 3.14)` exists.
//
// Path is interpreted exactly as modload does: relative to the
// process cwd. If the directory doesn't exist or contains no
// readable .pho/.phl files, returns nil — the caller treats that as
// "I can't validate this import" and stays silent rather than
// drowning the user in `package not found` noise (the LSP's cwd is
// often not the project root).
//
// No caching: at LSP rates this is roughly "read 2-3 small files
// per import per keystroke", which is cheap and avoids stale data
// when the user edits an imported package's source.
func PackageExports(path string) map[string]Definition {
	if path == "" {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}

	exports := map[string]Definition{}
	any := false

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Only library files contribute to a package's import surface.
		// Sibling .pho programs are entrypoint scripts whose decls
		// the runtime never adds to pkg.Exports, so the linter
		// mirrors that and ignores them here.
		if !isLibrary(e.Name()) {
			continue
		}
		any = true

		full := filepath.Join(path, e.Name())
		src, err := readSource(full)
		if err != nil {
			continue
		}

		tokens, _ := syntax.LexPos(string(src))
		tree, _ := syntax.ParsePos(tokens)
		tree = syntax.NormalizeDo(tree)

		for _, form := range tree {
			collectExportedDecls(exports, form, full)
		}
	}

	if !any {
		return nil
	}
	return exports
}

// PackageStructs reads the directory at `path` like PackageExports,
// but returns the field/method tables of every EXPORTED (capitalized)
// struct the package declares. Used by shape inference to validate
// member access on instances built via `(pkg.Struct ...)`.
//
// Methods are collected regardless of their own capitalization —
// private methods still exist and produce better diagnostics
// ("private" beats "not found"). Returns nil when the package can't
// be read, mirroring PackageExports' stay-silent contract.
func PackageStructs(path string) map[string]*structInfo {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}

	structs := map[string]*structInfo{}
	at := func(name string) *structInfo {
		si, ok := structs[name]
		if !ok {
			si = &structInfo{
				Name:        name,
				Fields:      map[string]span.Span{},
				Methods:     map[string]span.Span{},
				MethodFiles: map[string]string{},
			}
			structs[name] = si
		}
		return si
	}

	any := false
	// Accumulate each struct's typed-field decls; field navigation shapes are
	// resolved in a second pass below, once every exported struct name is known
	// (so same-package and forward references resolve).
	type pendingFields struct {
		si     *structInfo
		fields []fieldDecl
	}
	var pending []pendingFields
	// This package's own imports (alias → resolved path), so a struct field
	// typed as a struct from a FURTHER import (`pkg2.Foo`) can be navigated.
	pkgImports := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !isLibrary(e.Name()) {
			continue
		}
		any = true
		full := filepath.Join(path, e.Name())
		src, err := readSource(full)
		if err != nil {
			continue
		}
		tokens, _ := syntax.LexPos(string(src))
		tree, _ := syntax.ParsePos(tokens)
		tree = syntax.NormalizeDo(tree)

		for _, form := range tree {
			d, ok := declOf(form)
			if !ok {
				continue
			}
			switch d.Head {
			case "import":
				// Record the package's own Pho imports (alias → resolved path),
				// resolved relative to THIS file, mirroring collectImports' two
				// forms. Used to follow a `pkg2.Foo` field type below.
				for _, arg := range d.Branch.Children[1:] {
					if p, ok := stringLiteral(arg); ok {
						if alias := pathBasename(p); alias != "" {
							pkgImports[alias] = resolveImportPath(full, p)
						}
						continue
					}
					if abr, ok := arg.(*ast.PBranch); ok && abr.Open == "(" && len(abr.Children) == 2 {
						if p, ok := stringLiteral(abr.Children[0]); ok {
							if alias, _, ok := declIdent(abr.Children[1]); ok {
								pkgImports[alias] = resolveImportPath(full, p)
							}
						}
					}
				}
			case "struct":
				// Only EXPORTED (capitalized) structs form the package's
				// member surface.
				if d.Name == "" || !unicode.IsUpper(rune(d.Name[0])) {
					continue
				}
				si := at(d.Name)
				si.File = full
				for _, f := range d.Fields {
					si.Fields[f.Name] = f.Span
					// Resolve the field's declared type against builtins (the
					// imported package's own aliases aren't loaded here), so a
					// fully-primitively-typed imported struct gets a precise
					// record and its values check across the import boundary. A
					// struct-typed or aliased field resolves to Dynamic → that
					// struct stays coarse, which is sound.
					if f.Type != nil {
						if si.FieldTypes == nil {
							si.FieldTypes = map[string]*core.PhoType{}
						}
						si.FieldTypes[f.Name] = resolveTypeNode(f.Type, nil)
					}
				}
				pending = append(pending, pendingFields{si, d.Fields})
			case "method":
				// Methods are collected on capitalized owners regardless of
				// their own case — private methods still exist and produce
				// better diagnostics ("private" beats "not found").
				if d.Owner == "" || d.Name == "" || !unicode.IsUpper(rune(d.Owner[0])) {
					continue
				}
				// A union receiver (Collection = String|List|Map) registers on
				// EACH member — mirroring collectOne and the runtime — so
				// `"x".Split` resolves across an import when declared as
				// `Collection.Split`.
				for _, owner := range memberOwners(d.Owner) {
					si := at(owner)
					si.Methods[d.Name] = d.NameSpan
					si.MethodFiles[d.Name] = full
				}
			case "property":
				// An ATTACHED `(property Recv.Name …)` is a computed member of
				// its owner struct — register it like a field so an importer's
				// `inst.Name` resolves. Free-standing properties have no owner
				// and are package exports (PackageExports), not struct members.
				if d.Owner == "" || d.Name == "" || !unicode.IsUpper(rune(d.Owner[0])) {
					continue
				}
				// A property on a primitive/union TYPE (e.g. Collection) is a
				// named member read via primitiveMemberSources (which reads
				// .Methods); a property on a STRUCT is a computed field read via
				// the instance-member check (.Fields). Mirror collectOne.
				_, isType := core.TypeByName(d.Owner)
				for _, owner := range memberOwners(d.Owner) {
					si := at(owner)
					if isType {
						si.Methods[d.Name] = d.NameSpan
					} else {
						si.Fields[d.Name] = d.NameSpan
					}
				}
			}
		}
	}
	// Second pass: record each struct-typed field's navigation shape now that
	// every exported struct name is known. Field types are bare same-package
	// names (`Next Node`) or `(Or … Nil)` nullables; transitive `pkg2.Struct`
	// references across a further import aren't followed yet.
	resolve := func(n ast.PNode) (Shape, bool) {
		switch t := n.(type) {
		case *ast.PLeaf:
			if _, exists := structs[t.Value]; exists {
				return Shape{Kind: ShapeInstance, Owner: t.Value, OwnerPkg: path}, true
			}
		case *ast.PDot:
			// A field typed as a struct from a FURTHER import (`pkg2.Foo`):
			// resolve pkg2 through this package's own imports. The struct's
			// existence in pkg2 is verified at navigation time (resolveStruct
			// returns false if it isn't one), so this stays non-recursive.
			alias, aok := t.LHS.(*ast.PLeaf)
			member, mok := t.RHS.(*ast.PLeaf)
			if aok && mok {
				if p2 := pkgImports[alias.Value]; p2 != "" {
					return Shape{Kind: ShapeInstance, Owner: member.Value, OwnerPkg: p2}, true
				}
			}
		}
		return Shape{}, false
	}
	for _, p := range pending {
		for _, f := range p.fields {
			if f.Type == nil {
				continue
			}
			if sh, ok := fieldStructShape(f.Type, resolve); ok {
				if p.si.FieldStructOwner == nil {
					p.si.FieldStructOwner = map[string]Shape{}
				}
				p.si.FieldStructOwner[f.Name] = sh
			}
		}
	}
	if !any {
		return nil
	}
	return structs
}

// collectExportedDecls inspects one top-level form and adds any
// capitalized fun/struct/var/const declaration to `out` — the names an
// importer can reach as `pkg.Name`. Methods are NOT package exports: the
// runtime stores them on the struct's method table, never as top-level
// names, so they're reached only through an instance (instance.Method,
// validated separately via PackageStructs). Exported var/const are
// read-only from outside the module — the runtime rejects `(= pkg.Name v)`
// (see checkAssign), so they're recorded with their DefVar/DefConst kind
// to drive that diagnostic and hover/completion labelling.
func collectExportedDecls(out map[string]Definition, form ast.PNode, file string) {
	d, ok := declOf(form)
	if !ok {
		return
	}
	exported := func(name string) bool {
		return name != "" && unicode.IsUpper(rune(name[0]))
	}
	switch d.Head {
	case "fun":
		if exported(d.Name) {
			out[d.Name] = Definition{Name: d.Name, Kind: DefFun, Span: d.NameSpan, File: file}
		}
	case "macro":
		// A capitalized top-level macro exports like any other binding —
		// the runtime puts it in the module frame under its name.
		if exported(d.Name) {
			out[d.Name] = Definition{Name: d.Name, Kind: DefMacro, Span: d.NameSpan, File: file}
		}
	case "struct":
		if exported(d.Name) {
			out[d.Name] = Definition{Name: d.Name, Kind: DefStruct, Span: d.NameSpan, File: file}
		}
	case "var", "const":
		kind := DefVar
		if d.Head == "const" {
			kind = DefConst
		}
		for _, b := range d.Binds {
			if exported(b.Name) {
				out[b.Name] = Definition{Name: b.Name, Kind: kind, Span: b.Span, File: file}
			}
		}
	case "property":
		// A capitalized FREE-STANDING property exports like a var — a faux
		// variable read through its getter, read-only from outside the module
		// (the runtime rejects `(= pkg.Name v)` just as for a var). An
		// ATTACHED `(property Recv.Name …)` has an owner and is a struct
		// member, not a top-level export — PackageStructs handles it.
		if d.Owner == "" && exported(d.Name) {
			out[d.Name] = Definition{Name: d.Name, Kind: DefVar, Span: d.NameSpan, File: file}
		}
	}
}
