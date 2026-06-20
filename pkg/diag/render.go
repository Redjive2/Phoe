package diag

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"pho/pkg/span"
)

// NewReporter returns a Session.Report hook that renders each diagnostic
// to out as it is emitted. After maxErrors fully-rendered errors,
// subsequent errors render in compact one-line form so a cascade can't
// bury the run — information is de-weighted, never dropped (the summary
// line still counts every one). maxErrors <= 0 means no cap. Warnings
// always render in full.
func NewReporter(out io.Writer, st Style, maxErrors int) func(RuntimeError) {
	errs := 0
	return func(e RuntimeError) {
		if e.Severity == SeverityWarning {
			io.WriteString(out, Render(e, st))
			return
		}
		errs++
		switch {
		case maxErrors <= 0 || errs <= maxErrors:
			io.WriteString(out, Render(e, st))
		case errs == maxErrors+1:
			// Announce the switch once, then stay compact.
			io.WriteString(out, st.paint(st.Dim, fmt.Sprintf("... further errors shown compactly (PHO_MAX_ERRORS=%d) ...", maxErrors)))
			io.WriteString(out, "\n")
			io.WriteString(out, RenderCompact(e, st))
		default:
			io.WriteString(out, RenderCompact(e, st))
		}
	}
}

// RenderCompact renders a diagnostic as a single line — header plus
// location — for errors past the cap. No excerpt, no trace.
func RenderCompact(e RuntimeError, st Style) string {
	headColor := st.Red
	if e.Severity == SeverityWarning {
		headColor = st.Yellow
	}
	var b strings.Builder
	b.WriteString(st.paint(headColor, fmt.Sprintf("%s[%s]", e.Severity, e.Code)))
	b.WriteString(": ")
	b.WriteString(e.Message)
	if e.File != "" {
		b.WriteString("  ")
		b.WriteString(st.paint(st.Blue, "-->"))
		b.WriteByte(' ')
		if e.Span.StartLine > 0 {
			fmt.Fprintf(&b, "%s:%d:%d", e.File, e.Span.StartLine, e.Span.StartCol)
		} else {
			b.WriteString(e.File)
		}
	}
	b.WriteByte('\n')
	return b.String()
}

// WriteSummary emits the run's final verdict line (once, last) when any
// diagnostic was produced: "error: N errors, M warnings" in bold red, or
// "warning: M warnings" in bold yellow when there were only warnings.
// Nothing is written for a clean run.
func WriteSummary(out io.Writer, st Style, errors, warnings int) {
	switch {
	case errors > 0:
		msg := fmt.Sprintf("%d error%s", errors, plural(errors))
		if warnings > 0 {
			msg += fmt.Sprintf(", %d warning%s", warnings, plural(warnings))
		}
		io.WriteString(out, st.paint(st.Red, "error:")+" "+st.paint(st.Bold, msg)+"\n")
	case warnings > 0:
		io.WriteString(out, st.paint(st.Yellow, "warning:")+" "+st.paint(st.Bold, fmt.Sprintf("%d warning%s", warnings, plural(warnings)))+"\n")
	}
}

// Render produces the full multi-line block for one diagnostic:
//
//	error[code]: message
//	 --> path:line:col
//	  |
//	6 |     '(* n 2))
//	  |         ^^^^
//	  |
//	  = note: ...
//	  = help: ...
//	trace (most recent call first):
//	   0: double       script/rps.pho:6:9
//
// Every block ends with exactly one blank line. Pure function of its
// inputs (no TTY, no env), so it is directly testable.
func Render(e RuntimeError, st Style) string {
	var b strings.Builder

	headColor := st.Red
	caretColor := st.Red
	if e.Severity == SeverityWarning {
		headColor, caretColor = st.Yellow, st.Yellow
	}

	// Header.
	b.WriteString(st.paint(headColor, fmt.Sprintf("%s[%s]", e.Severity, e.Code)))
	b.WriteString(": ")
	b.WriteString(st.paint(st.Bold, e.Message))
	b.WriteByte('\n')

	hasSpan := e.Span.StartLine > 0
	_, haveLine := lineAt(e.Source, e.Span.StartLine)

	// Gutter width: digits of the (single) excerpted line number.
	w := 1
	if hasSpan {
		w = len(fmt.Sprint(e.Span.StartLine))
	}

	// Location.
	if e.File != "" {
		b.WriteString(strings.Repeat(" ", w))
		b.WriteString(st.paint(st.Blue, "-->"))
		b.WriteByte(' ')
		if hasSpan {
			fmt.Fprintf(&b, "%s:%d:%d", e.File, e.Span.StartLine, e.Span.StartCol)
		} else {
			b.WriteString(e.File)
		}
		b.WriteByte('\n')
	}

	// Primary excerpt with caret underline.
	if hasSpan && haveLine {
		writeExcerpt(&b, st, e.Source, e.Span, caretColor)
	}

	// Trailer — notes, help, and the macro-expansion block — separated
	// from the excerpt above by a closing gutter bar.
	indent := strings.Repeat(" ", w+1)
	bar := indent + st.paint(st.Blue, "|")
	if hasSpan && haveLine && (len(e.Notes) > 0 || len(e.Help) > 0 || e.Expansion != nil) {
		b.WriteString(bar + "\n")
	}
	for _, n := range e.Notes {
		b.WriteString(indent + st.paint(st.Blue, "=") + " " + st.paint(st.Bold, "note:") + " " + n + "\n")
	}
	for _, h := range e.Help {
		b.WriteString(indent + st.paint(st.Blue, "=") + " " + st.paint(st.Cyan, "help:") + " " + h + "\n")
	}

	// Macro-expansion block: the generated code the error actually came
	// from, shown as its own excerpt beneath the call-site one.
	if e.Expansion != nil {
		intro := "expanded from generated code:"
		if e.Expansion.Macro != "" {
			intro = fmt.Sprintf("expanded from macro '%s':", e.Expansion.Macro)
		}
		b.WriteString(indent + st.paint(st.Blue, "=") + " " + st.paint(st.Bold, intro) + "\n")
		writeExcerpt(&b, st, e.Expansion.Source, e.Expansion.Span, caretColor)
	}

	// Trace — omitted at depth <= 1: the --> line already locates a
	// top-level error, and repeating it is noise.
	if len(e.Trace) > 1 {
		rows := buildTraceRows(e.Trace)
		b.WriteString(st.paint(st.Bold, "trace (most recent call first):"))
		b.WriteByte('\n')
		idxW := len(fmt.Sprint(len(rows) - 1))
		nameW := 0
		for _, r := range rows {
			if r.elided == 0 {
				nameW = max(nameW, len(r.frame.Name))
			}
		}
		for i, r := range rows {
			if r.elided > 0 {
				b.WriteString("   ")
				b.WriteString(st.paint(st.Dim, fmt.Sprintf("... %d frame%s elided ...", r.elided, plural(r.elided))))
				b.WriteByte('\n')
				continue
			}
			f := r.frame
			b.WriteString("   ")
			b.WriteString(st.paint(st.Dim, fmt.Sprintf("%*d:", idxW, i)))
			b.WriteByte(' ')
			nameColor := st.Bold
			if strings.HasPrefix(f.Name, "<") {
				nameColor = st.Dim
			}
			b.WriteString(st.paint(nameColor, f.Name))
			b.WriteString(strings.Repeat(" ", nameW-len(f.Name)+2))
			b.WriteString(frameLocation(f))
			b.WriteByte('\n')
			if r.repeat > 0 {
				b.WriteString("   ")
				b.WriteString(st.paint(st.Dim, fmt.Sprintf("... %s repeated %d more time%s ...", f.Name, r.repeat, plural(r.repeat))))
				b.WriteByte('\n')
			}
		}
	}

	b.WriteByte('\n')
	return b.String()
}

// traceRow is one rendered trace line: a frame (with a repeat count for a
// collapsed run of identical frames), or — when elided > 0 — a truncation
// marker standing in for that many omitted frames.
type traceRow struct {
	frame  Frame
	repeat int // additional identical frames collapsed after this one
	elided int // >0 => elision marker; frame/repeat unused
}

// buildTraceRows collapses consecutive identical frames (recursion would
// otherwise dump one line per call) and, if the result is still very
// deep, keeps the innermost and outermost frames with an elision marker
// between — so a runaway stack can't bury the rest of the diagnostic.
func buildTraceRows(frames []Frame) []traceRow {
	var rows []traceRow
	for i := 0; i < len(frames); {
		j := i + 1
		for j < len(frames) && frames[j] == frames[i] {
			j++
		}
		rows = append(rows, traceRow{frame: frames[i], repeat: j - i - 1})
		i = j
	}

	const head, tail = 30, 9
	if len(rows) > head+tail+1 {
		elided := len(rows) - head - tail
		trunc := make([]traceRow, 0, head+1+tail)
		trunc = append(trunc, rows[:head]...)
		trunc = append(trunc, traceRow{elided: elided})
		trunc = append(trunc, rows[len(rows)-tail:]...)
		rows = trunc
	}
	return rows
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// sourceLine fetches the 1-based StartLine of the diagnostic's source
// text. Missing source or an out-of-range line (e.g. the file model is
// stale) skips the excerpt rather than risking excerpting wrong code.
func lineAt(src string, lineNo int) (string, bool) {
	if src == "" || lineNo < 1 {
		return "", false
	}
	rest := src
	for n := 1; ; n++ {
		line := rest
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = ""
		}
		if n == lineNo {
			return strings.TrimSuffix(line, "\r"), true
		}
		if rest == "" {
			return "", false
		}
	}
}

// writeExcerpt renders one gutter excerpt — opening bar, the numbered
// source line, and the caret underline (plus a "to be continued" marker
// for a multi-line span) — for span sp against source text src. Shared by
// the primary call-site excerpt and the macro-expansion block. A no-op
// when the line isn't available.
func writeExcerpt(b *strings.Builder, st Style, src string, sp span.Span, caretColor string) {
	line, ok := lineAt(src, sp.StartLine)
	if !ok {
		return
	}
	w := len(fmt.Sprint(sp.StartLine))
	bar := strings.Repeat(" ", w+1) + st.paint(st.Blue, "|")

	startByte := clamp(sp.StartCol-1, 0, len(line))
	var endByte int
	if sp.EndLine == sp.StartLine {
		// Spans are half-open: EndCol points one past the last column.
		endByte = clamp(sp.EndCol-1, startByte, len(line))
	} else {
		endByte = len(strings.TrimRight(line, " \t"))
	}

	pad := cellWidth(line, 0, startByte)
	carets := max(cellWidth(line, startByte, endByte), 1)

	b.WriteString(bar)
	b.WriteByte('\n')
	b.WriteString(st.paint(st.Blue, fmt.Sprintf("%*d |", w, sp.StartLine)))
	b.WriteByte(' ')
	b.WriteString(displayLine(line))
	b.WriteByte('\n')
	b.WriteString(bar)
	b.WriteByte(' ')
	b.WriteString(strings.Repeat(" ", pad))
	b.WriteString(st.paint(caretColor, strings.Repeat("^", carets)))
	b.WriteByte('\n')
	if sp.EndLine > sp.StartLine {
		b.WriteString(strings.Repeat(" ", w))
		b.WriteString(st.paint(st.Dim, ".."))
		b.WriteByte('\n')
	}
}

func frameLocation(f Frame) string {
	if f.File == "" || f.Span.StartLine < 1 {
		return "<unknown>"
	}
	return fmt.Sprintf("%s:%d:%d", f.File, f.Span.StartLine, f.Span.StartCol)
}

// displayLine renders a source line for the excerpt: tabs expand to
// four spaces and invalid UTF-8 bytes become '?', mirroring exactly the
// cell accounting cellWidth does so carets align by construction.
func displayLine(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		switch {
		case r == '\t':
			b.WriteString("    ")
		case r == utf8.RuneError && size == 1:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// cellWidth returns the display width of line[from:to]: a tab is four
// cells, every other rune (including multibyte) is one. East Asian wide
// runes are treated as one cell — a documented v1 limitation.
func cellWidth(line string, from, to int) int {
	cells := 0
	for i := from; i < to && i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == '\t' {
			cells += 4
		} else {
			cells++
		}
		i += size
	}
	return cells
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
