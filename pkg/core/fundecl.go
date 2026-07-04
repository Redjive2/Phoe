package core

// Function declarations + clause-based implementations (Features.md §1–§3, §9).
//
// A `(fun name (T…) R)` / `(method O.n (T…) R)` / `(static method …)` type
// SIGNATURE is no longer erased: it registers an Overload on the name's
// FunDecl. Each `(let name (patterns) [where guard] = body)` clause compiles
// (in pkg/builtins) to a Clause whose Try closure matches, binds, guards, and
// runs the body; the dispatcher tries clauses in declaration order.
//
// Core holds the dumb storage — the pattern engine, sig evaluation, and the
// dispatchers live in pkg/builtins (mirroring how DiscSet stored compiled Funs
// while builtins compiled and dispatched them).

// SigParamMode classifies one signature parameter slot.
type SigParamMode int

const (
	ParamRequired SigParamMode = iota
	ParamVar                   // (var T) — mutable binding (static contract)
	ParamSpread                // (spread T) — trailing rest-arg
	ParamOptional              // (optional T) / (optional T else D)
	ParamConst                 // (const T) — call sites pass a parse-time constant
)

// SigParam is one evaluated signature parameter.
type SigParam struct {
	Type *PhoType
	Mode SigParamMode
	// Default is the `else` expression of `(optional T else D)`, evaluated in
	// the declaration's context when the argument is `none` (omitted or
	// explicit — none-coalescing). nil when the optional has no default.
	Default Node
}

// Clause is one compiled implementation clause. Try matches args against the
// clause's patterns, evaluates the guard, and — on success — runs the body,
// returning (result, true); (_, false) means "next clause".
type Clause struct {
	Try func(ctx Context, args []Tval) (Tval, bool)
	// CatchAll marks an unguarded all-binder clause (matches anything) — the
	// fall-through the exhaustiveness rules require when coverage is
	// undecidable. Arity is how many values the fixed patterns consume.
	CatchAll bool
	Guarded  bool
	Arity    int
	Spread   bool   // trailing (spread name) collects the rest
	Desc     string // rendered patterns, for no-matching-impl errors
}

// Overload is one signature plus its adjacent clauses. Params is nil for an
// implicit (sig-less, .pho-inference) overload: dispatch then checks only each
// clause's own arity.
type Overload struct {
	Params []SigParam
	Result *PhoType
	// Required is the minimum argument count (params before the first
	// optional/spread); Max the maximum (-1 with a trailing spread).
	Required int
	Max      int
	Clauses  []Clause
	// DefCtx is the declaration-site context, used to evaluate `else`
	// defaults (sig params are types, not names, so a default is a closed
	// expression over the declaring scope).
	DefCtx Context
	Static bool // declared `static method` — receiver is the type value
}

// FunDecl is every overload of one callable name (free function, method, or
// static method — the registry key distinguishes them).
type FunDecl struct {
	Name      string
	Overloads []*Overload
	Implicit  bool // created by a clause with no signature (.pho inference)
	// Installed marks that the dispatcher has been bound/stored (set by the
	// first clause); later clauses and overloads mutate the shared *FunDecl.
	Installed bool
}

// Latest returns the most recently declared overload — the one adjacent
// clauses attach to.
func (fd *FunDecl) Latest() *Overload {
	if len(fd.Overloads) == 0 {
		return nil
	}
	return fd.Overloads[len(fd.Overloads)-1]
}

// FunDeclFor returns the env-level FunDecl under key, creating it on first
// use. Keys are composite: "name" (free fun), "Owner.name" (method or
// extension method), "Owner/name" (static method) — def-time linking only;
// dispatchers capture the *FunDecl directly.
func (ctx Context) FunDeclFor(key, name string) *FunDecl {
	if ctx.Env.FunDecls == nil {
		ctx.Env.FunDecls = map[string]*FunDecl{}
	}
	fd := ctx.Env.FunDecls[key]
	if fd == nil {
		fd = &FunDecl{Name: name}
		ctx.Env.FunDecls[key] = fd
	}
	return fd
}

// LookupFunDecl returns the FunDecl under key, or nil.
func (ctx Context) LookupFunDecl(key string) *FunDecl {
	if ctx.Env.FunDecls == nil {
		return nil
	}
	return ctx.Env.FunDecls[key]
}
