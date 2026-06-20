package diag

import (
	"strings"
	"testing"

	"pho/pkg/span"
)

func sp(sl, sc, el, ec int) span.Span {
	return span.Span{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
}

// TestRenderFullBlock pins the plain-style layout: header, location,
// gutter excerpt, caret alignment, notes/help, trace, and the single
// terminating blank line.
func TestRenderFullBlock(t *testing.T) {
	src := "(fun 'double '(n)\n    '(* n 2))\n"
	// The span underlines the mid-line form '(* n 2)' — deliberately NOT
	// running to end-of-line, so an inclusive-EndCol regression (which the
	// endByte clamp would mask on an EOL span) shifts the caret count and
	// fails this golden.
	e := RuntimeError{
		Diagnostic: Diagnostic{
			File:     "script/rps.pho",
			Span:     sp(2, 6, 2, 13),
			Severity: SeverityError,
			Code:     ErrType,
			Message:  "'*' expected a 'num' argument, got 'str'",
			Notes:    []string{"'double' is defined at script/rps.pho:1:7"},
			Help:     []string{"convert it before multiplying: (num count)"},
		},
		Source: src,
		Trace: []Frame{
			{Name: "double", File: "script/rps.pho", Span: sp(2, 6, 2, 13)},
			{Name: "<top level>", File: "script/rps.pho", Span: sp(4, 1, 4, 15)},
		},
	}

	got := Render(e, StylePlain)
	want := strings.Join([]string{
		"error[type-mismatch]: '*' expected a 'num' argument, got 'str'",
		" --> script/rps.pho:2:6",
		"  |",
		"2 |     '(* n 2))",
		"  |      ^^^^^^^",
		"  |",
		"  = note: 'double' is defined at script/rps.pho:1:7",
		"  = help: convert it before multiplying: (num count)",
		"trace (most recent call first):",
		"   0: double       script/rps.pho:2:6",
		"   1: <top level>  script/rps.pho:4:1",
		"",
		"",
	}, "\n")
	if got != want {
		t.Errorf("Render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderNoSpan pins the degraded form: location is file-only, no
// excerpt block, no trace at depth <= 1.
func TestRenderNoSpan(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{
			File:     "main.pho",
			Severity: SeverityError,
			Code:     ErrUnresolved,
			Message:  "operation 'frobnicate' is not defined",
		},
	}

	got := Render(e, StylePlain)
	want := "error[unresolved]: operation 'frobnicate' is not defined\n" +
		" --> main.pho\n\n"
	if got != want {
		t.Errorf("Render mismatch\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestRenderWarningHeader pins the severity word for warnings.
func TestRenderWarningHeader(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{
			Severity: SeverityWarning,
			Code:     ErrUnresolved,
			Message:  "identifier 'x' is not defined",
		},
	}
	got := Render(e, StylePlain)
	if !strings.HasPrefix(got, "warning[unresolved]: ") {
		t.Errorf("warning header = %q", got)
	}
}

// TestRenderTabAlignment: tabs expand to four cells in both the excerpt
// and the caret padding, so the carets line up with the display text.
func TestRenderTabAlignment(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{
			File:     "t.pho",
			Span:     sp(1, 2, 1, 8), // the (boom) form, after the tab
			Severity: SeverityError,
			Code:     ErrUnresolved,
			Message:  "operation 'boom' is not defined",
		},
		Source: "\t(boom)\n",
	}
	got := Render(e, StylePlain)
	if !strings.Contains(got, "1 |     (boom)\n") {
		t.Errorf("tab not expanded in excerpt:\n%s", got)
	}
	if !strings.Contains(got, "  |     ^^^^^^\n") {
		t.Errorf("caret misaligned under tab-expanded text:\n%s", got)
	}
}

// TestRenderSpanOutOfRange: a span pointing past the retained source
// must skip the excerpt instead of excerpting wrong code or panicking.
func TestRenderSpanOutOfRange(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{
			File:     "t.pho",
			Span:     sp(99, 1, 99, 4),
			Severity: SeverityError,
			Code:     ErrUnresolved,
			Message:  "x",
		},
		Source: "(one line)\n",
	}
	got := Render(e, StylePlain)
	if strings.Contains(got, "|") {
		t.Errorf("excerpt rendered for out-of-range span:\n%s", got)
	}
	if !strings.Contains(got, "t.pho:99:1") {
		t.Errorf("location line missing:\n%s", got)
	}
}

// TestColoredAndPlainDifferOnlyInEscapes: stripping SGR sequences from
// the ANSI render must yield exactly the plain render.
func TestColoredAndPlainDifferOnlyInEscapes(t *testing.T) {
	e := RuntimeError{
		Diagnostic: Diagnostic{
			File:     "x.pho",
			Span:     sp(1, 1, 1, 7),
			Severity: SeverityError,
			Code:     ErrUnresolved,
			Message:  "operation 'boom' is not defined",
			Notes:    []string{"n"},
		},
		Source: "(boom)\n",
		Trace: []Frame{
			{Name: "f", File: "x.pho", Span: sp(1, 1, 1, 7)},
			{Name: "<top level>", File: "x.pho", Span: sp(1, 1, 1, 7)},
		},
	}
	colored := Render(e, StyleANSI)
	stripped := stripSGR(colored)
	plain := Render(e, StylePlain)
	if stripped != plain {
		t.Errorf("stripped ANSI != plain\n--- stripped ---\n%q\n--- plain ---\n%q", stripped, plain)
	}
}

// TestRenderTraceCollapse: a run of identical consecutive frames
// (recursion) collapses to one line plus a dim repeat marker, and the
// surrounding distinct frames still render in order.
func TestRenderTraceCollapse(t *testing.T) {
	trace := []Frame{
		{Name: "down", File: "d.pho", Span: sp(1, 31, 1, 43)},
	}
	for i := 0; i < 59; i++ {
		trace = append(trace, Frame{Name: "down", File: "d.pho", Span: sp(1, 45, 1, 57)})
	}
	trace = append(trace, Frame{Name: "<top level>", File: "d.pho", Span: sp(2, 1, 2, 9)})

	e := RuntimeError{
		Diagnostic: Diagnostic{File: "d.pho", Span: sp(1, 31, 1, 43), Severity: SeverityError, Code: ErrType, Message: "boom"},
		Source:     "(down)\n(down)\n",
		Trace:      trace,
	}
	got := Render(e, StylePlain)
	for _, want := range []string{
		"   0: down         d.pho:1:31",
		"   1: down         d.pho:1:45",
		"... down repeated 58 more times ...",
		"   2: <top level>  d.pho:2:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace missing %q; got:\n%s", want, got)
		}
	}
	if n := strings.Count(got, ": down "); n != 2 {
		t.Errorf("expected 2 rendered down frames after collapse, got %d:\n%s", n, got)
	}
}

// TestRenderTraceTruncate: a trace too deep to fully show (after
// collapsing fails to shrink it — every frame distinct) keeps the
// innermost and outermost frames with an elision marker between.
func TestRenderTraceTruncate(t *testing.T) {
	var trace []Frame
	for i := 0; i < 100; i++ {
		// Distinct spans so nothing collapses.
		trace = append(trace, Frame{Name: "f", File: "x.pho", Span: sp(i+1, 1, i+1, 2)})
	}
	e := RuntimeError{
		Diagnostic: Diagnostic{File: "x.pho", Span: sp(1, 1, 1, 2), Severity: SeverityError, Code: ErrType, Message: "x"},
		Trace:      trace,
	}
	got := Render(e, StylePlain)
	if !strings.Contains(got, "frames elided ...") {
		t.Errorf("expected elision marker; got:\n%s", got)
	}
	// 30 innermost + 9 outermost frames shown, never all 100.
	if n := strings.Count(got, ": f "); n != 39 {
		t.Errorf("expected 39 shown frames (30 head + 9 tail), got %d", n)
	}
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := strings.IndexByte(s[i:], 'm')
			if j >= 0 {
				i += j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
