package core

import (
	"fmt"
	"os"
	"runtime"
	"strconv"

	"pho/pkg/diag"
)

// DebugMode (PHO_DEBUG) turns on interpreter-debugging aids: a Go stack
// trace attached to internal panics, and a "raised at <go-file:line>"
// provenance note on every diagnostic (restoring, behind a flag, the
// `@ pkg.func` origin the messages used to carry). Off by default and
// gated so it costs nothing in normal runs.
var DebugMode = os.Getenv("PHO_DEBUG") != ""

// maxCallDepth bounds Pho call recursion. The evaluator recurses on the
// Go stack, so unbounded recursion would hit Go's fatal, unrecoverable
// stack overflow; BindFun/BindMethod raise a RecursionSignal once the
// live frame depth reaches this. PHO_MAX_DEPTH overrides the default.
var maxCallDepth = envInt("PHO_MAX_DEPTH", 6000)

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// MaxCallDepth reports the active recursion limit (for the loader's
// diagnostic message).
func MaxCallDepth() int { return maxCallDepth }

// The diagnostic model (Severity, Diagnostic, RuntimeError, the call-stack
// Session, and the stable codes) lives in pkg/diag — a leaf package the
// linter and the loader share without depending on the runtime. core keeps
// only the raising end: ctx.Errorf / ctx.Warnf build a diag.RuntimeError
// from the current evaluation site, and ctx.PushCallFrame / PopCallFrame
// drive the session's call stack (the bookkeeping itself lives on
// diag.Session).
//
// The error codes are re-exported below so error sites that already hold a
// core.Context (the evaluator, builtins) can name them as core.ErrX
// without importing diag directly. They are the same untyped string
// constants — core.ErrArity == diag.ErrArity.
const (
	ErrParse        = diag.ErrParse
	ErrUnresolved   = diag.ErrUnresolved
	ErrNotCallable  = diag.ErrNotCallable
	ErrBadLiteral   = diag.ErrBadLiteral
	ErrBadSpread    = diag.ErrBadSpread
	ErrArity        = diag.ErrArity
	ErrType         = diag.ErrType
	ErrConstAssign  = diag.ErrConstAssign
	ErrBadAssign    = diag.ErrBadAssign
	ErrNoReceiver   = diag.ErrNoReceiver
	ErrBadImport    = diag.ErrBadImport
	ErrLibraryForm  = diag.ErrLibraryForm
	ErrBadExport    = diag.ErrBadExport
	ErrTopLevelFlow = diag.ErrTopLevelFlow
	ErrGoPanic      = diag.ErrGoPanic
	ErrBadForm      = diag.ErrBadForm
	ErrRedeclare    = diag.ErrRedeclare
	ErrIndexRange   = diag.ErrIndexRange
	ErrField        = diag.ErrField
	ErrGoCall       = diag.ErrGoCall
	ErrHost         = diag.ErrHost
	ErrInterp       = diag.ErrInterp
	ErrRecursion    = diag.ErrRecursion
)

// Errorf reports a runtime error at the current evaluation site and
// returns TvNil, so error paths read `return ctx.Errorf(...)`. File
// attribution comes from ctx.File; the span is the innermost enclosing
// positioned form (ctx.At), giving form-level caret excerpts.
func (ctx Context) Errorf(code, format string, args ...any) Tval {
	ctx.emit(diag.SeverityError, code, fmt.Sprintf(format, args...))
	return TvNil
}

// Warnf is Errorf at warning severity: rendered, but does not affect the
// process exit code.
func (ctx Context) Warnf(code, format string, args ...any) Tval {
	ctx.emit(diag.SeverityWarning, code, fmt.Sprintf(format, args...))
	return TvNil
}

// ErrorfAt reports an error positioned at `at`'s span when it carries one
// (a positioned form), otherwise at the current evaluation site. Lets a
// builtin caret the specific offending argument rather than the whole
// enclosing call.
func (ctx Context) ErrorfAt(at Node, code, format string, args ...any) Tval {
	if sp, ok := SpanOf(at); ok {
		ctx.At = &sp
	}
	return ctx.Errorf(code, format, args...)
}

func (ctx Context) emit(sev diag.Severity, code, msg string) {
	sp := ctx.spanAt()
	// Errors carry a call-stack snapshot; warnings don't (they're frequent
	// and a trace would be noise). The renderer omits the trace section at
	// depth <= 1, so top-level errors stay trace-free anyway.
	var trace []diag.Frame
	if sev == diag.SeverityError {
		trace = ctx.Diag.Trace(ctx.fileName(), sp)
	}
	var notes []string
	if DebugMode {
		if _, file, line, ok := runtime.Caller(2); ok { // emit <- Errorf/Warnf <- site
			notes = append(notes, fmt.Sprintf("raised at %s:%d", file, line))
		}
	}
	// When the error originated in macro-generated code, attach that code
	// as a secondary excerpt. The primary span/excerpt stays the macro
	// call site (ctx.At); this shows the user the code they didn't write.
	// Phase A carets the whole generated form (single-line Inspect output);
	// Phase B will narrow it to the offending sub-form.
	var expansion *diag.Expansion
	if ctx.Expand != nil {
		src := ctx.Expand.Source
		// Caret the precise offending sub-form (ExpandAt) when one was
		// reached; fall back to the whole generated form otherwise (e.g.
		// the error fired on a bare leaf, which carries no wrapper).
		sp := Span{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: len(src) + 1}
		if ctx.ExpandAt != nil {
			sp = *ctx.ExpandAt
		}
		expansion = &diag.Expansion{Macro: ctx.Expand.Macro, Source: src, Span: sp}
	}
	ctx.Diag.Emit(diag.RuntimeError{
		Diagnostic: diag.Diagnostic{File: ctx.fileName(), Span: sp, Severity: sev, Code: code, Message: msg, Notes: notes},
		Source:     ctx.fileSrc(),
		Trace:      trace,
		Expansion:  expansion,
	})
}

// EmitPanic reports a diagnostic whose precise throw point inside the
// innermost frame is unknown — a foreign Go panic (interpreter bug or
// unrecovered host error) or a recursion-limit unwind, both caught at the
// top level. There's no excerpt; the trace shows each frame at its call
// site. Extra notes (e.g. a Go stack under PHO_DEBUG) are appended.
func (ctx Context) EmitPanic(code, msg string, notes ...string) {
	ctx.Diag.Emit(diag.RuntimeError{
		Diagnostic: diag.Diagnostic{File: ctx.fileName(), Severity: diag.SeverityError, Code: code, Message: msg, Notes: notes},
		Source:     ctx.fileSrc(),
		Trace:      ctx.Diag.TraceRaw(),
	})
}

// PushCallFrame records entry into a named Pho call (function, method,
// constructor, go-interop) for stack traces, storing the call site —
// where execution was when the call was made. Pair with PopCallFrame on
// the normal-return path. Nil-safe.
func (ctx Context) PushCallFrame(name string) {
	ctx.Diag.Push(diag.Frame{Name: name, File: ctx.fileName(), Span: ctx.spanAt()})
}

// PopCallFrame removes the innermost call frame. Call only on the
// normal-return path: a foreign panic must leave the frame in place so the
// top-level recover can snapshot the trace.
func (ctx Context) PopCallFrame() { ctx.Diag.Pop() }

func (ctx Context) spanAt() Span {
	if ctx.At == nil {
		return Span{}
	}
	return *ctx.At
}

func (ctx Context) fileName() string {
	if ctx.File == nil {
		return ""
	}
	if ctx.File.Path != "" {
		return ctx.File.Path
	}
	return ctx.File.FileName
}

func (ctx Context) fileSrc() string {
	if ctx.File == nil {
		return ""
	}
	return ctx.File.Src
}
