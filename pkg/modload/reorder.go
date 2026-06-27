package modload

import "pho/pkg/core"

// Order-agnostic library loading.
//
// In a library (.phl) file a declaration may appear before another it depends
// on — e.g. a `const` that constructs a struct declared further down. Every
// declaration except `const`/`var` is a pure DEFINITION (it only registers a
// binding or attaches a method/property, with no observable side effect), so
// the loader lifts all definitions above the `const`/`var` declarations before
// evaluating. `const`/`var` are the only side-effecting declarations; they run
// last, in source order (they are NOT sorted among themselves — a const that
// uses a later const is still a forward reference and fails).
//
// Program (.pho) files are left untouched: their top-level forms may be
// arbitrary side-effecting expressions whose order is observable.

// orderedForm pairs a top-level form with the file it came from, so the loader
// keeps per-form context (error positions, library gating) after reordering.
type orderedForm struct {
	form core.Node
	file *core.File
}

// liftDefinitions returns forms with every non-var/const declaration moved
// ahead of the var/const declarations, preserving source order within each
// group. Called only for library loads.
func liftDefinitions(forms []orderedForm) []orderedForm {
	out := make([]orderedForm, 0, len(forms))
	for _, f := range forms { // definitions first, in source order
		if !isVarConst(f.form) {
			out = append(out, f)
		}
	}
	for _, f := range forms { // then const/var, in source order
		if isVarConst(f.form) {
			out = append(out, f)
		}
	}
	return out
}

// isVarConst reports whether a top-level form is a `var` or `const` declaration
// — the only side-effecting declarations, which stay below the definitions.
func isVarConst(form core.Node) bool {
	br, ok := core.AsBranch(form)
	if !ok || len(br) == 0 {
		return false
	}
	head, ok := core.AsLeaf(br[0])
	if !ok {
		return false
	}
	return string(head) == "var" || string(head) == "const" || string(head) == "let"
}
