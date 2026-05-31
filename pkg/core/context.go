package core

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
