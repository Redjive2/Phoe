package lint

import "pho/pkg/core"

// libraryForms is the allow-list of head identifiers permitted at the
// top level of a .phl file. Mirrors modload's runtime check exactly.
var libraryForms = map[string]bool{
	"import":   true,
	"goimport": true,
	"fun":      true,
	"method":   true,
	"struct":   true,
	"const":    true,
}

// checkNoTopLevelVar walks the top-level forms and flags any (var ...)
// — in .phl library files only. Programs (.pho) allow top-level `var`;
// the caller in lint.go is what gates this check on file mode.
//
// This is a syntactic check (we only look at the head of each top-level
// list); it doesn't follow the form into bodies.
func checkNoTopLevelVar(file string, tree []core.PNode) []Diagnostic {
	var diags []Diagnostic
	for _, form := range tree {
		br, ok := asList(form)
		if !ok {
			continue
		}
		if headIdent(br) != "var" {
			continue
		}
		diags = append(diags, Diagnostic{
			File:     file,
			Span:     br.Children[0].GetSpan(),
			Severity: SeverityError,
			Code:     "no-top-level-var",
			Message:  "'var' is not allowed at the top level of a library file (.phl) — use 'const' instead, or move the binding into a function body",
		})
	}
	return diags
}

// checkPhlSideEffects flags any top-level form in a .phl file that
// isn't a declaration or import. Mirrors modload's library-form rule.
func checkPhlSideEffects(file string, tree []core.PNode) []Diagnostic {
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
