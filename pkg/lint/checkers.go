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
	// `let` / `let var` are the canonical declaration forms (const/var kept for
	// tolerance); both bind module-level names at the top of a library.
	"let": true,
	// A named type alias `(type Name T)` is a declaration (a constant KindType
	// binding), so it is permitted at the top level of a library.
	"type": true,
	// A top-level `property` declares a faux variable (free-standing) or a
	// computed struct member (attached) — both declarations, not side effects.
	"property": true,
	// `static method`/`static property` declare type-level members.
	"static": true,
	// `(operator Recv.OP …)` declares an operator overload on a type
	// (Features.md §7) — a signature, like `method`.
	"operator": true,
	// `(trait Name …)` declares a named trait type.
	"trait": true,
	// `(template …)` declares type parameters for the following generic
	// struct/method declaration.
	"template": true,
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
		// A 4-child `(= name (params) body)` / `(= Owner.name …)` is a fun/method
		// IMPLEMENTATION — a pure definition permitted at a library's top level (the
		// decl/impl split). A 3-child `(= target value)` reassignment stays a side
		// effect, rejected below. Mirrors modload.isLibraryForm (load.go).
		if head == "=" && len(br.Children) == 4 {
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
