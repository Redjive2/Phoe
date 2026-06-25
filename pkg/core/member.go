package core

// Per-package method/property extension tables and import-scoped member
// resolution — the object model's universal dispatch
// (Doc/PlanV1/ObjectModel.md §4.2–§4.3).
//
// Methods and properties attached to a PRIMITIVE type (or the universal
// "unknown" top type) belong to the package that DECLARES them, keyed by the
// receiver type's key (see TypeKey). A call site resolves such a member through
// its own package, the EXPORTED extensions of the packages its file imports,
// and the always-in-scope built-in module — so a user extension is visible
// only where its declaring module is imported, while built-ins are everywhere.
//
// Struct methods/properties still live on *tstruct (the KindInstance dot path),
// reached globally through any instance; migrating struct extensions into these
// import-scoped tables is a later step. This file governs primitive and
// universal extensions.

// methodExt is one declared method extension.
type methodExt struct {
	Fun        tfun
	Privileged bool
	Exported   bool
}

// propExt is one declared property extension.
type propExt struct {
	Prop       tproperty
	Privileged bool
	Exported   bool
}

// UnknownTypeKey is the extension-table key for universal (top-type) members,
// which apply to every value regardless of its concrete type.
const UnknownTypeKey = "unknown"

// BuiltinExtensions, when set, returns the always-in-scope built-in module
// package whose method/property tables apply to every value without an import
// (the .Size/.Keys/Is?/In? built-ins). Installed by the builtins package; nil
// in bare/test envs that have not loaded the built-in module.
var BuiltinExtensions func() *tpackage

// TypeKey returns the stable extension-table key for a type. Primitive types
// and the top type have fixed keys; composite types and structs return "" —
// structs dispatch through *tstruct, not these tables, in this increment.
func (t *PhoType) TypeKey() string {
	switch t {
	case TypeNumber:
		return "prim:num"
	case TypeString:
		return "prim:str"
	case TypeList:
		return "prim:list"
	case TypeDict:
		return "prim:dict"
	case TypeBoolean:
		return "prim:bool"
	case TypeChar:
		return "prim:chr"
	case TypeAtom:
		return "prim:atom"
	case TypeFunction:
		return "prim:fun"
	case TypeNil:
		return "prim:nil"
	case TypeType:
		return "prim:type"
	case TypeUnknown:
		return UnknownTypeKey
	}
	return ""
}

// TypeKeyOf returns the extension-table key for the runtime type of v.
func TypeKeyOf(v Tval) string {
	return TvTypeOf(v).TypeKey()
}

// AddMethod registers a method extension in the current package under
// (typeKey, name). Returns false if that pair is already declared in THIS
// package — a duplicate, which the caller reports as a hard error (§6).
func (ctx Context) AddMethod(typeKey, name string, fn tfun, privileged, exported bool) bool {
	pkg := ctx.Package
	if pkg == nil {
		return false
	}
	if pkg.Methods == nil {
		pkg.Methods = map[string]map[string]methodExt{}
	}
	tbl := pkg.Methods[typeKey]
	if tbl == nil {
		tbl = map[string]methodExt{}
		pkg.Methods[typeKey] = tbl
	}
	if _, dup := tbl[name]; dup {
		return false
	}
	tbl[name] = methodExt{Fun: fn, Privileged: privileged, Exported: exported}
	return true
}

// AddProperty registers a property extension in the current package under
// (typeKey, name). Returns false on a duplicate, like AddMethod.
func (ctx Context) AddProperty(typeKey, name string, prop tproperty, privileged, exported bool) bool {
	pkg := ctx.Package
	if pkg == nil {
		return false
	}
	if pkg.Properties == nil {
		pkg.Properties = map[string]map[string]propExt{}
	}
	tbl := pkg.Properties[typeKey]
	if tbl == nil {
		tbl = map[string]propExt{}
		pkg.Properties[typeKey] = tbl
	}
	if _, dup := tbl[name]; dup {
		return false
	}
	tbl[name] = propExt{Prop: prop, Privileged: privileged, Exported: exported}
	return true
}

// MemberResult is the outcome of resolving a member by name on a value.
type MemberResult struct {
	Found      bool
	Clash      bool // >1 distinct definition visible — ambiguous (§6)
	IsProperty bool
	Method     tfun
	Property   tproperty
	Privileged bool
}

// ResolveMember resolves member `name` for a value whose type key is typeKey,
// searching with NO precedence (any ambiguity is a clash): the built-in
// module, the current package's own extensions, and the EXPORTED extensions of
// each package the active file imports. Both the value's own type key and the
// universal "unknown" key participate. A package reachable more than once is
// considered only once.
func (ctx Context) ResolveMember(typeKey, name string) MemberResult {
	var (
		res     MemberResult
		matches int
		seen    = map[*tpackage]bool{}
	)

	keys := []string{typeKey, UnknownTypeKey}

	consider := func(pkg *tpackage, allVisible bool) {
		if pkg == nil || seen[pkg] {
			return
		}
		seen[pkg] = true
		for _, key := range keys {
			if key == "" {
				continue
			}
			if m, ok := pkg.Methods[key][name]; ok && (allVisible || m.Exported) {
				res.IsProperty = false
				res.Method = m.Fun
				res.Privileged = m.Privileged
				matches++
			}
			if p, ok := pkg.Properties[key][name]; ok && (allVisible || p.Exported) {
				res.IsProperty = true
				res.Property = p.Prop
				res.Privileged = p.Privileged
				matches++
			}
		}
	}

	// Built-in module: always in scope, all members visible.
	if BuiltinExtensions != nil {
		consider(BuiltinExtensions(), true)
	}
	// Own package: all members visible (including non-exported).
	consider(ctx.Package, true)
	// Imported packages: only their exported extensions.
	if ctx.File != nil {
		for _, imp := range ctx.File.Imports {
			if imp.Kind == KindPackage {
				consider(imp.Val.(*tpackage), false)
			}
		}
	}

	res.Found = matches > 0
	res.Clash = matches > 1
	return res
}
