package lint

import "fmt"

// Built-in object-model member surface for tooling — the members the
// auto-loaded built-in module (pkg/builtins/pho/*.phl) attaches to the
// primitive types and to the universal top type Unknown. They live in the
// compiler, not the user's workspace, so completion/hover can't discover them
// by scanning sources; they are mirrored here and kept in sync by a drift test
// (typemembers_test.go). User-declared primitive extensions, by contrast, ARE
// in the workspace and are collected like struct methods (keyed by the type
// name), so they surface through the ordinary structInfo path.

type builtinMember struct {
	Name string
	Kind DefKind // DefField for a property, DefMethod for a method
}

// builtinTypeMembers maps a primitive type's display name to the members the
// built-in module attaches to it (collections.phl).
var builtinTypeMembers = map[string][]builtinMember{
	"List":   {{"size", DefField}, {"keys", DefField}, {"empty?", DefField}},
	"String": {{"size", DefField}, {"keys", DefField}, {"empty?", DefField}},
	"Map":    {{"size", DefField}, {"keys", DefField}, {"empty?", DefField}},
}

// universalMembers are attached to Unknown (the top type), so they resolve on a
// value of ANY type (universal.phl).
var universalMembers = []builtinMember{
	{"is?", DefMethod},
	{"in?", DefMethod},
	{"pipe", DefMethod},
	{"to", DefMethod},
}

// isUniversalMember reports whether name is a universal method (resolves on a
// value of any type, including a struct instance).
func isUniversalMember(name string) bool {
	for _, m := range universalMembers {
		if m.Name == name {
			return true
		}
	}
	return false
}

// shapeTypeName maps an inferred value shape to the built-in type display name
// whose members apply, or "" when the shape has no primitive member surface
// (e.g. a struct instance, or an unknown shape).
func shapeTypeName(k ShapeKind) string {
	switch k {
	case ShapeArray:
		return "List"
	case ShapeString:
		return "String"
	case ShapeDict:
		return "Map"
	case ShapeNum:
		return "Number"
	case ShapeBool:
		return "Boolean"
	case ShapeChar:
		return "Char"
	case ShapeAtom:
		return "Atom"
	case ShapeNil:
		return "Nil"
	case ShapeFun:
		return "Function"
	}
	return ""
}

// selfShapeForOwner returns the shape of a method's receiver `self`, given the
// method's owner type. A user struct → a privileged instance of it (so private
// fields/methods resolve). A built-in CONCRETE type → that type's shape, so
// inside e.g. `(method List.At (self i) self.[i])` the receiver is treated as a
// list — indexable, with its object-model members resolvable — not as a struct
// instance (which is not indexable). A composite/abstract built-in (Collection,
// Unknown) or an unrecognized owner has no single concrete shape, so it stays
// Unknown and nothing is falsely flagged.
func selfShapeForOwner(scope *Scope, owner string) Shape {
	if def, _, found := scope.Lookup(owner); found && def.Kind == DefStruct {
		return Shape{Kind: ShapeInstance, Owner: owner, Privileged: true}
	}
	switch owner {
	case "List":
		return Shape{Kind: ShapeArray}
	case "String":
		return Shape{Kind: ShapeString}
	case "Map":
		return Shape{Kind: ShapeDict}
	case "Number":
		return Shape{Kind: ShapeNum}
	case "Boolean":
		return Shape{Kind: ShapeBool}
	case "Char":
		return Shape{Kind: ShapeChar}
	case "Atom":
		return Shape{Kind: ShapeAtom}
	case "Function":
		return Shape{Kind: ShapeFun}
	}
	return Shape{}
}

// builtinMemberDefs returns the built-in object-model completions for a value
// of the given shape: the type-specific members (for a primitive shape) plus
// the universal (Unknown) members, which apply to every value.
func builtinMemberDefs(k ShapeKind) []Definition {
	var out []Definition
	if tn := shapeTypeName(k); tn != "" {
		for _, m := range builtinTypeMembers[tn] {
			out = append(out, Definition{Name: m.Name, Kind: m.Kind})
		}
	}
	for _, m := range universalMembers {
		out = append(out, Definition{Name: m.Name, Kind: m.Kind})
	}
	return out
}

// scopeImportPaths returns the resolved import paths of every DefImport visible
// from scope (innermost first), deduplicated — the packages whose exported
// extensions are in scope at this point.
func scopeImportPaths(scope *Scope) []string {
	var out []string
	seen := map[string]bool{}
	for cur := scope; cur != nil; cur = cur.Parent {
		for _, d := range cur.Defs {
			if d.Kind == DefImport && d.Path != "" && !seen[d.Path] {
				seen[d.Path] = true
				out = append(out, d.Path)
			}
		}
	}
	return out
}

// scopeImportDefs is like scopeImportPaths but yields the import Definitions
// (alias Name + Path), so a caller that resolves a member through an import can
// mark that alias used.
func scopeImportDefs(scope *Scope) []Definition {
	var out []Definition
	seen := map[string]bool{}
	for cur := scope; cur != nil; cur = cur.Parent {
		for _, d := range cur.Defs {
			if d.Kind == DefImport && d.Path != "" && !seen[d.Name] {
				seen[d.Name] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// markExtensionImportUse marks every in-scope import that exports an extension
// member (method or property) named `member` as used. It backs the
// unused-import determination for a member access whose receiver shape is
// statically unknown: the access can't be type-checked, but importing a module
// purely for such an extension is still a use — mirroring primitiveMemberSources,
// which does the same marking when the receiver type IS known. A lowercase
// member is package-private and never crosses an import boundary, so it marks
// nothing. Conservative by construction: it can only suppress a false
// unused-import, never emit a diagnostic.
func (w *walker) markExtensionImportUse(scope *Scope, member string) {
	if startsLower(member) {
		return
	}
	for _, imp := range scopeImportDefs(scope) {
		for _, si := range w.structsFor(imp.Path) {
			if _, found := si.Methods[member]; found {
				w.usedImports[imp.Name] = true
				break
			}
		}
	}
}

// primitiveMemberSources counts the DISTINCT in-scope definitions of `member`
// on the primitive type `typeName`: the built-in object-model module (a member
// is either a type-specific built-in or a universal Unknown member — one
// source), this package's own extension (collected like a struct method under
// the type name), and each imported package that exports such an extension.
//
// The count drives two diagnostics, mirroring the runtime resolver: 0 sources →
// unknown member (a typo); >1 → an ambiguous clash (e.g. two imports, or a
// local/imported redefinition of a built-in). Exactly 1 is a clean resolution.
func (w *walker) primitiveMemberSources(scope *Scope, typeName, member string) int {
	n := 0
	for _, m := range builtinTypeMembers[typeName] {
		if m.Name == member {
			n++
			break
		}
	}
	if n == 0 { // a member is never both a type-specific and a universal built-in
		for _, m := range universalMembers {
			if m.Name == member {
				n++
				break
			}
		}
	}
	if si, ok := scope.LookupStruct(typeName); ok {
		if _, found := si.Methods[member]; found {
			n++
		}
	}
	// Imported packages expose only their CAPITALIZED extensions; a lowercase
	// member is private to its declaring package, so it's invisible across the
	// import boundary (mirroring the runtime resolver). A lowercase access thus
	// never resolves through an import.
	if !startsLower(member) {
		for _, imp := range scopeImportDefs(scope) {
			if si, ok := w.structsFor(imp.Path)[typeName]; ok {
				if _, found := si.Methods[member]; found {
					n++
					// Resolving a member through an import counts as using it —
					// importing a package for its extension methods is a use.
					w.usedImports[imp.Name] = true
				}
			}
		}
	}
	return n
}

// builtinMemberDocs holds one-line descriptions for the built-in object-model
// members, surfaced on hover.
var builtinMemberDocs = map[string]string{
	"size":   "element/rune count of the collection (replaces `len`)",
	"keys":   "the collection's keys — a list/string's indices, a map's keys (replaces `keyof`)",
	"empty?": "whether the collection has no elements",
	"is?":    "whether the value's type is the given type",
	"in?":    "whether the value is an element of the given collection",
	"pipe":   "thread the value left-to-right through each step function, returning the final result",
	"to":     "convert the value to the given type via that type's `from`",
}

func memberKindWord(k DefKind) string {
	if k == DefMethod {
		return "method"
	}
	return "property"
}

// builtinMemberHover returns hover markdown for a built-in object-model member
// of the given type — type-specific (collection) or universal (Unknown) — and
// ok=false if `member` isn't a built-in member for that type. Built-in members
// have no workspace source span, so this synthetic hover is how the editor
// documents them.
func builtinMemberHover(typeName, member string) (string, bool) {
	for _, m := range builtinTypeMembers[typeName] {
		if m.Name == member {
			return builtinHoverMD(typeName, m), true
		}
	}
	for _, m := range universalMembers {
		if m.Name == member {
			return builtinHoverMD("Unknown", m), true
		}
	}
	return "", false
}

func builtinHoverMD(typeName string, m builtinMember) string {
	md := fmt.Sprintf("```pho\n%s.%s\n```\nbuilt-in %s", typeName, m.Name, memberKindWord(m.Kind))
	if doc := builtinMemberDocs[m.Name]; doc != "" {
		md += " — " + doc
	}
	return md
}

// importedPrimitiveExtensions returns the exported extension members that
// imported packages attach to the primitive type `typeName` — for dot
// completion on a value of that type.
func (w *walker) importedPrimitiveExtensions(scope *Scope, typeName string) []Definition {
	var out []Definition
	for _, path := range scopeImportPaths(scope) {
		si, ok := w.structsFor(path)[typeName]
		if !ok {
			continue
		}
		for name, span := range si.Methods {
			if startsLower(name) {
				continue // private to its declaring package — not exported
			}
			out = append(out, Definition{Name: name, Kind: DefMethod, Span: span, File: si.MethodFiles[name]})
		}
	}
	return out
}
