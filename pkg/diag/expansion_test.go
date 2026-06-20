package diag

import (
	"strings"
	"testing"
)

// TestRenderExpansion pins the macro-expansion block: the primary excerpt
// is the call site, followed by an "expanded from macro" block showing
// the generated code with its own caret.
func TestRenderExpansion(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{
			File:     "m.pho",
			Span:     sp(2, 1, 2, 13),
			Severity: SeverityError,
			Code:     ErrUnresolved,
			Message:  "operation 'fakeFn' is not defined",
		},
		Source: "(top)\n(evilMacro!)\n",
		Expansion: &Expansion{
			Macro:  "evilMacro",
			Source: "(fakeFn arg)",
			Span:   sp(1, 1, 1, 13),
		},
	}
	got := Render(e, StylePlain)
	want := strings.Join([]string{
		"error[unresolved]: operation 'fakeFn' is not defined",
		" --> m.pho:2:1",
		"  |",
		"2 | (evilMacro!)",
		"  | ^^^^^^^^^^^^",
		"  |",
		"  = expanded from macro 'evilMacro':",
		"  |",
		"1 | (fakeFn arg)",
		"  | ^^^^^^^^^^^^",
		"",
		"",
	}, "\n")
	if got != want {
		t.Errorf("Render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderExpansionUnnamed: a direct `resume` (no macro name) labels the
// block generically rather than "macro ”".
func TestRenderExpansionUnnamed(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{File: "m.pho", Severity: SeverityError, Code: ErrUnresolved, Message: "x"},
		Expansion:  &Expansion{Macro: "", Source: "(gen)", Span: sp(1, 1, 1, 6)},
	}
	got := Render(e, StylePlain)
	if !strings.Contains(got, "= expanded from generated code:") {
		t.Errorf("unnamed expansion should use generic label; got:\n%s", got)
	}
	if strings.Contains(got, "macro ''") {
		t.Errorf("should not render an empty macro name; got:\n%s", got)
	}
}

// TestRenderExpansionColorEqualsPlain: the expansion block, like the rest
// of the renderer, must be byte-identical in color and plain modes once
// SGR sequences are stripped.
func TestRenderExpansionColorEqualsPlain(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{File: "m.pho", Span: sp(1, 1, 1, 6), Severity: SeverityError, Code: ErrUnresolved, Message: "x"},
		Source:     "(call)\n",
		Expansion:  &Expansion{Macro: "m", Source: "(gen arg)", Span: sp(1, 1, 1, 10)},
	}
	if got := stripSGR(Render(e, StyleANSI)); got != Render(e, StylePlain) {
		t.Errorf("stripped ANSI != plain\n--- stripped ---\n%q\n--- plain ---\n%q", got, Render(e, StylePlain))
	}
}
