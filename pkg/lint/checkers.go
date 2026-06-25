package lint

import "pho/pkg/ast"

// libraryForms is the allow-list of head identifiers permitted at the
// top level of a .phl file. Mirrors modload's runtime check exactly.
// `var` is permitted: a top-level var is module-level state, mutable
// within the module but read-only from outside it.
var libraryForms = map[string]bool{
	"import":   true,
	"goimport": true,
	"fun":      true,
	"macro":    true,
	"method":   true,
	"struct":   true,
	"const":    true,
	"var":      true,
	// A named type alias `(type Name T)` is a declaration (a constant KindType
	// binding), so it is permitted at the top level of a library.
	"type": true,
	// A top-level `property` declares a faux variable (free-standing) or a
	// computed struct member (attached) — both declarations, not side effects.
	"property": true,
	// `static method`/`static property` declare type-level members.
	"static": true,
	// `(trait Name …)` declares a named trait type.
	"trait": true,
}

// checkPhlSideEffects flags any top-level form in a .phl file that
// isn't a declaration or import. Mirrors modload's library-form rule.
func checkPhlSideEffects(file string, tree []ast.PNode) []Diagnostic {
	var diags []Diagnostic
	for _, form := range tree {
		br, ok := asList(form)
		if !ok {
			// Bare atoms at top level are side effects too — flag the
			// whole node.
			diags = append(diags, Diagnostic{
				File:     file,
				Span:     form.GetSpan(),
				Severity: SeverityError,
				Code:     "phl-side-effect",
				Message:  "library files (.phl) may only contain declarations and imports at the top level",
			})
			continue
		}
		head := headIdent(br)
		if libraryForms[head] {
			continue
		}

		// An empty `()` form has no head child to point at; fall back
		// to the whole-form span so the diagnostic still has a range.
		span := br.Span
		if len(br.Children) > 0 {
			span = br.Children[0].GetSpan()
		}
		diags = append(diags, Diagnostic{
			File:     file,
			Span:     span,
			Severity: SeverityError,
			Code:     "phl-side-effect",
			Message:  "library files (.phl) may only contain declarations and imports at the top level",
		})
	}
	return diags
}
