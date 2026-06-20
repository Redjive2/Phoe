package diag

import (
	"fmt"
	"os"

	"pho/pkg/span"
)

// The diagnostic model: the shared data shape for every problem Pho
// reports, static or runtime. It lives here — not in core — so the
// linter, the loader, and package main can all describe and count
// diagnostics without depending on the interpreter runtime. core depends
// on this package (its ctx.Errorf builds a RuntimeError); the dependency
// never points the other way, which is what keeps diag a leaf alongside
// pkg/span and pkg/ast.
//
// RuntimeError carries the source text to excerpt and a path-only call
// trace — never a *core.File — so the renderer needs nothing from the
// runtime.

// Severity is the LSP-aligned level of a diagnostic.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityInfo
	SeverityHint
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	case SeverityHint:
		return "hint"
	}
	return "unknown"
}

// Stable diagnostic codes. Kebab-case to match the linter's existing
// codes ("parse-error", "no-top-level-var"). Never inline a code string
// at an error site — add a constant here so codes stay greppable and
// tooling can rely on them. core re-exports these so error sites that
// already hold a core.Context can name them without importing diag.
const (
	ErrParse        = "parse-error" // shared with pkg/lint
	ErrUnresolved   = "unresolved"
	ErrNotCallable  = "not-callable"
	ErrBadLiteral   = "bad-literal"
	ErrBadSpread    = "bad-spread"
	ErrArity        = "arity"
	ErrType         = "type-mismatch"
	ErrConstAssign  = "const-assign"
	ErrBadAssign    = "bad-assign"
	ErrNoReceiver   = "no-receiver"
	ErrBadImport    = "bad-import"
	ErrLibraryForm  = "library-form"
	ErrBadExport    = "bad-export"
	ErrTopLevelFlow = "top-level-flow"
	ErrGoPanic      = "go-panic"
	ErrBadForm      = "bad-form"          // malformed special-form syntax (bad arg list, wrong head shape)
	ErrRedeclare    = "redeclare"         // name already in use
	ErrIndexRange   = "index-range"       // array index / slice bounds out of range
	ErrField        = "no-field"          // struct field / member access (missing or private)
	ErrGoCall       = "go-call"           // Go-interop dispatch failure (goop.Call)
	ErrHost         = "host-error"        // host-layer IO failure (spawn, wire, stream)
	ErrInterp       = "bad-interpolation" // string-interpolation lowering failure
	ErrRecursion    = "recursion-limit"   // call depth exceeded the guard
)

// Diagnostic is a single problem found in source code. Code is a stable
// short identifier (e.g. "no-top-level-var") that tooling can filter on.
// Notes and Help render as "= note:" / "= help:" lines under the source
// excerpt; the linter currently leaves them empty.
type Diagnostic struct {
	File     string
	Span     span.Span
	Severity Severity
	Code     string
	Message  string
	Notes    []string
	Help     []string
}

// Format renders a Diagnostic in GCC-style:
//
//	main.pho:5:3: error[no-top-level-var]: 'var' is not allowed at the top level
func (d Diagnostic) Format() string {
	return fmt.Sprintf("%s:%d:%d: %s[%s]: %s",
		d.File, d.Span.StartLine, d.Span.StartCol,
		d.Severity, d.Code, d.Message,
	)
}

// oneLine is the fallback rendering used when no renderer is installed
// (bare sessions, embedders). The GCC line when a position is known;
// without one, plain "severity[code]: message" rather than a misleading
// ":0:0:".
func (d Diagnostic) oneLine() string {
	if d.Span != (span.Span{}) && d.File != "" {
		return d.Format()
	}
	if d.File != "" {
		return fmt.Sprintf("%s: %s[%s]: %s", d.File, d.Severity, d.Code, d.Message)
	}
	return fmt.Sprintf("%s[%s]: %s", d.Severity, d.Code, d.Message)
}

// Frame is one Pho-level call-stack entry. File is the call-site file
// path (not a runtime *File — diag stays decoupled from core); the trace
// is captured by a later phase, so renderers show the section only when
// non-empty.
type Frame struct {
	Name string    // "double", "Point.Shift", "<top level>"
	File string    // call-site file path; "" = unknown
	Span span.Span // call-site span; zero = unknown
}

// Expansion is a secondary excerpt for an error that originated in
// macro-generated code: the macro's name (empty = unknown / direct
// `resume`), the generated code rendered as Pho source, and the span
// within it that the error points at. The renderer shows it as an
// "expanded from macro" block beneath the primary call-site excerpt.
type Expansion struct {
	Macro  string
	Source string
	Span   span.Span
}

// RuntimeError is a Diagnostic plus the context a renderer needs: the
// source text to excerpt and the call stack at the moment the error was
// raised. Source is the full text of the offending file (empty = no
// excerpt) — passed by value so the renderer never reaches into the
// runtime's file model.
type RuntimeError struct {
	Diagnostic
	Source    string     // source text to excerpt; "" = no excerpt
	Trace     []Frame    // innermost first; nil = no trace section
	Expansion *Expansion // macro-generated origin; nil = not from an expansion
}

// Session collects every diagnostic of one interpreter run: it counts
// errors for the process exit code, holds the live Pho call stack, and
// forwards each RuntimeError to the injected Report hook (package main
// wires in NewReporter). One session is shared across all packages of a
// run, threaded as a core.Context field — core holds no package-level
// diagnostic state. core drives the stack through ctx.PushCallFrame /
// PopCallFrame; the frame bookkeeping lives here so the whole diagnostic
// surface stays in one package.
type Session struct {
	Report func(RuntimeError)

	// Strict, when set (PHO_STRICT), tells the loader to stop evaluating
	// further top-level forms once any error has been reported, rather
	// than continuing in print-and-continue mode.
	Strict bool

	errors, warnings int

	// frames is the live Pho call stack in push order (outermost first).
	// Pushed on entry to a user function/method/constructor/go-interop
	// call, popped on normal return. A foreign Go panic leaves frames in
	// place — the per-call pops are skipped during unwind — so the
	// top-level recover can still snapshot a trace; the loader's Truncate
	// clears them between top-level forms.
	frames []Frame
}

func NewSession() *Session { return &Session{} }

func (s *Session) ErrorCount() int {
	if s == nil {
		return 0
	}
	return s.errors
}

func (s *Session) WarningCount() int {
	if s == nil {
		return 0
	}
	return s.warnings
}

// Emit counts e and hands it to the Report hook. Nil-safe on both the
// receiver and the hook: a bare session (or a nil one, e.g. a test
// Context) still gets a plain one-line report on stderr rather than
// silence or a panic.
func (s *Session) Emit(e RuntimeError) {
	if s != nil {
		// Only errors gate the exit code; warnings are tallied separately.
		// Info/Hint are advisory and count as neither — otherwise a future
		// Info diagnostic would silently make `pho run` fail.
		switch e.Severity {
		case SeverityError:
			s.errors++
		case SeverityWarning:
			s.warnings++
		}
		if s.Report != nil {
			s.Report(e)
			return
		}
	}
	fmt.Fprintln(os.Stderr, e.oneLine())
}

// Push records entry into a Pho call. Nil-safe.
func (s *Session) Push(f Frame) {
	if s == nil {
		return
	}
	s.frames = append(s.frames, f)
}

// Pop removes the innermost frame on a normal call return. Nil-safe.
func (s *Session) Pop() {
	if s == nil || len(s.frames) == 0 {
		return
	}
	s.frames = s.frames[:len(s.frames)-1]
}

// Depth is the current frame-stack depth. The loader records it before
// evaluating a top-level form and Truncates back to it afterward, so a
// nested package load (via import) restores only its own frames.
func (s *Session) Depth() int {
	if s == nil {
		return 0
	}
	return len(s.frames)
}

// Truncate drops frames back to depth n (a backstop that also cleans up
// the frames a foreign panic left behind).
func (s *Session) Truncate(n int) {
	if s == nil || n < 0 || n > len(s.frames) {
		return
	}
	s.frames = s.frames[:n]
}

// Trace builds the display call stack for a precise (Pho-level) error at
// (file, sp): innermost first, each frame paired with the source location
// of where execution was within it. The innermost frame shows the error's
// own span; each outer frame shows the call site of the frame just inside
// it (stored as that inner frame's call site at push time). Returns nil
// when there are no frames.
func (s *Session) Trace(file string, sp span.Span) []Frame {
	if s == nil || len(s.frames) == 0 {
		return nil
	}
	n := len(s.frames)
	out := make([]Frame, n)
	for k := 0; k < n; k++ {
		j := n - 1 - k // session index, innermost first
		out[k].Name = s.frames[j].Name
		if j == n-1 {
			out[k].File, out[k].Span = file, sp
		} else {
			out[k].File, out[k].Span = s.frames[j+1].File, s.frames[j+1].Span
		}
	}
	return out
}

// TraceRaw builds a call-site trace for a foreign panic, whose precise
// throw point inside the innermost frame is unknown: every frame shows the
// call site it was invoked from, innermost first.
func (s *Session) TraceRaw() []Frame {
	if s == nil || len(s.frames) == 0 {
		return nil
	}
	n := len(s.frames)
	out := make([]Frame, n)
	for k := 0; k < n; k++ {
		out[k] = s.frames[n-1-k]
	}
	return out
}
