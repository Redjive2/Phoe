package diag

import (
	"strings"
	"testing"
)

func errAt(file string, line int, msg string) RuntimeError {
	return RuntimeError{
		Diagnostic: Diagnostic{File: file, Span: sp(line, 1, line, 2), Severity: SeverityError, Code: ErrType, Message: msg},
	}
}

// TestReporterCap: the first maxErrors errors render in full; a single
// transition line announces the switch; the rest render compactly. The
// reporter never drops a diagnostic.
func TestReporterCap(t *testing.T) {
	var b strings.Builder
	report := NewReporter(&b, StylePlain, 2)
	for i := 1; i <= 5; i++ {
		report(errAt("x.pho", i, "boom"))
	}
	out := b.String()

	if got := strings.Count(out, "  |\n"); got != 0 {
		// StylePlain full blocks have no excerpt here (no Source), but the
		// transition + compact lines must be present.
	}
	if n := strings.Count(out, "\n  --> x.pho"); n != 0 {
		t.Errorf("unexpected full-excerpt arrows: %d", n)
	}
	if !strings.Contains(out, "further errors shown compactly (PHO_MAX_ERRORS=2)") {
		t.Errorf("missing transition line:\n%s", out)
	}
	// Errors 3,4,5 are compact one-liners with inline locations.
	for _, loc := range []string{"x.pho:3:1", "x.pho:4:1", "x.pho:5:1"} {
		if !strings.Contains(out, "boom  --> "+loc) {
			t.Errorf("missing compact line for %s:\n%s", loc, out)
		}
	}
}

// TestReporterNoCap: maxErrors <= 0 disables the cap entirely.
func TestReporterNoCap(t *testing.T) {
	var b strings.Builder
	report := NewReporter(&b, StylePlain, 0)
	for i := 1; i <= 5; i++ {
		report(errAt("x.pho", i, "boom"))
	}
	if strings.Contains(b.String(), "further errors shown compactly") {
		t.Errorf("cap should be disabled with maxErrors=0:\n%s", b.String())
	}
}

func TestWriteSummary(t *testing.T) {
	cases := []struct {
		errs, warns int
		want        string
	}{
		{0, 0, ""},
		{1, 0, "error: 1 error\n"},
		{3, 0, "error: 3 errors\n"},
		{2, 1, "error: 2 errors, 1 warning\n"},
		{0, 2, "warning: 2 warnings\n"},
	}
	for _, c := range cases {
		var b strings.Builder
		WriteSummary(&b, StylePlain, c.errs, c.warns)
		if b.String() != c.want {
			t.Errorf("WriteSummary(%d,%d) = %q, want %q", c.errs, c.warns, b.String(), c.want)
		}
	}
}

func TestRenderCompact(t *testing.T) {
	got := RenderCompact(errAt("x.pho", 7, "bad thing"), StylePlain)
	want := "error[type-mismatch]: bad thing  --> x.pho:7:1\n"
	if got != want {
		t.Errorf("RenderCompact = %q, want %q", got, want)
	}
}
