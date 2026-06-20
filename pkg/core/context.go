package core

import "pho/pkg/diag"

// Context bundles the "where am I?" state that the interpreter threads
// through every evaluation: the current environment (frames, captures),
// the package being evaluated, the file the current expression came from,
// and whether we're inside a function body.
//
// Context is passed by value. Its fields are pointers, so mutations through
// them (e.g. PushFrame on Env, writing to File.Imports) propagate to the
// caller, but rebinding ctx.Env in a callee has no effect on the caller —
// which gives us cheap, lexical save/restore semantics for free.
//
// InFunction is set to true by BindFun / BindMethod when entering a body.
// It stays false at top-level loading and inside BindCallback bodies (the
// `if`/`while` branches), since those run inline in the caller's scope.
// `var` consults this flag to refuse top-level mutable declarations.
type Context struct {
	Env        *Env
	Package    *Package
	File       *File
	InFunction bool

	// Diag is the run-wide diagnostic session errors report through.
	// Shared by pointer across every Context of a run; nil (bare test
	// Contexts, embedders) degrades to plain one-line stderr reports.
	Diag *diag.Session

	// At is the span of the innermost enclosing positioned form, stamped
	// by ttspanned.Evaluate into each subtree's ctx copy. nil = unknown
	// (macro-generated code, bare test Contexts). Errorf reads it so
	// diagnostics point at the offending form.
	At *Span

	// Expand, when non-nil, marks that we're evaluating macro-generated
	// code (the output of `resume`). It carries the macro's name and the
	// rendered generated source so a diagnostic can show that code as a
	// secondary "expanded from macro" excerpt — while ctx.At still points
	// at the real call site (the macro invocation). Cleared automatically
	// when evaluation enters a real function body (BindFun derives the
	// body context from the function's definition site, where Expand is
	// nil), so only code that is literally generated carries it.
	Expand *Expansion

	// ExpandAt is the span of the innermost form WITHIN the generated
	// source, the expansion's analogue of At. While Expand is set,
	// ttspanned.Evaluate stamps here instead of At (which stays frozen at
	// the call site), so a diagnostic can caret the precise offending
	// sub-form of the generated code. nil = whole generated form.
	ExpandAt *Span
}

// Expansion is the static context of a macro expansion: the macro's name
// (empty when `resume` was called directly, not via `name!` sugar) and
// the generated code rendered to Pho source text.
type Expansion struct {
	Macro  string
	Source string
}

// WithEnv returns a copy of ctx with a different Env.
func (ctx Context) WithEnv(env *Env) Context {
	ctx.Env = env
	return ctx
}

// WithPackage returns a copy of ctx with a different Package.
func (ctx Context) WithPackage(pkg *Package) Context {
	ctx.Package = pkg
	return ctx
}

// WithFile returns a copy of ctx with a different File.
func (ctx Context) WithFile(f *File) Context {
	ctx.File = f
	return ctx
}

// WithExpansion returns a copy of ctx marked as evaluating macro-generated
// code: errors raised under it carry the generated source as a secondary
// excerpt. ctx.At is left as-is so the primary location stays the macro
// call site.
func (ctx Context) WithExpansion(macro, source string) Context {
	ctx.Expand = &Expansion{Macro: macro, Source: source}
	return ctx
}
