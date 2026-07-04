// Package core defines the core data model and evaluator for the Pho
// interpreter: AST node types, runtime values, scope/environment plumbing,
// and the tree-walking eval methods.
//
// Within core, types use lowercase names (ttnode, tval, etc.). The
// uppercase aliases below export the same types to other packages.
package core

// Internal type definitions — pure data, no behavior beyond Evaluate.

type Tval struct {
	Val  any
	Kind string
}

type tStackEntry struct {
	Val        Tval
	IsConstant bool
}

// tfun is the runtime signature for both built-in and user-defined
// functions. ctx carries the caller's environment/package/file; argv is the
// unevaluated argument list (each builtin chooses when to Evaluate).
type tfun func(ctx Context, argv []ttnode) Tval

type ttnode interface {
	Evaluate(ctx Context) Tval
}

type ttbranch []ttnode

type ttleaf string

// tcontext marks a function-call boundary on the scope stack. See the
// package comment in scope.go for the full model.
type tcontext struct {
	// DefFrames are the frames lexically visible where the function was
	// defined, innermost first, shared by reference.
	DefFrames []map[string]tStackEntry
	// Hidden is how many frames at the bottom of env.Stack belong to the
	// caller and are invisible from inside the body.
	Hidden int
}

type tenv struct {
	Globals  *map[string]tStackEntry
	Stack    []map[string]tStackEntry
	CtxStack []tcontext
	Structs  map[string]*tstruct // Maps the address of the 'new' function returned by 'struct'
	// to the struct's underlying representation. Used to add methods.
	InstStack []Tval // all 'tinstance's

	// AllowShadow lets top-level declarations in THIS env rebind a builtin
	// global — normally forbidden. It is set only for the annotation macro
	// library, whose helper funcs (e.g. `type`, backing the `~type` annotation)
	// are an intentional overlay that shadows same-named builtins in the
	// isolated annotation env, keeping `~type` and the `type` builtin distinct.
	AllowShadow bool

	// FunDecls links signatures to their adjacent implementation clauses at
	// definition time (Features.md §1/§9), keyed "name" / "Owner.name" /
	// "Owner/name". Dispatch never consults this — dispatchers capture their
	// *FunDecl. See fundecl.go.
	FunDecls map[string]*FunDecl

	// RecvRebind, when non-nil, is the slot the currently running `(var self)`
	// method call reports a whole-value receiver reassignment into. The dot
	// accessor's method wrapper installs a fresh slot before each method call;
	// the clause dispatcher records the rebound receiver here; the wrapper
	// writes it back to the receiver lvalue. See RecvRebind, RecordRecvRebind.
	RecvRebind *RecvRebind

	// FrameSink is a one-shot mailbox: when non-nil, the next function body to
	// bind its arguments stores its argument-frame map here and clears the
	// mailbox. BindClauseBody uses it to read a clause's `(var param)` final
	// values after the body runs (the map is mutated in place by `=`, so it
	// outlives the frame pop). Nothing else runs between the set and the
	// consume, so it never leaks to an unrelated call.
	FrameSink *map[string]tStackEntry
}

// RecvRebind carries a `(var self)` method's whole-value receiver reassignment
// back to the call site. A method body that rebinds its receiver (`(= self v)`,
// as opposed to an in-place field write that already shares the pointer) has no
// visible caller binding — the body runs with the caller's frames hidden — so
// the dispatcher records the new value here and the dot accessor's wrapper
// assigns it back to the receiver lvalue. Set is the explicit "did rebind"
// signal, distinguishing a genuine reassignment from an unchanged receiver
// without an unreliable value comparison. See Effects.md `(var self)`.
type RecvRebind struct {
	Set bool
	Val Tval
}

type tpackage struct {
	Path    string            // canonical import path (the directory)
	Files   map[string]*tfile // filename -> file
	Exports map[string]Tval   // capitalized identifiers, merged across files
	Env     *tenv             // package-private env shared across files

	// Methods and Properties are the type EXTENSIONS this package declares on
	// primitive (or universal "unknown") types, keyed by typeKey then member
	// name. Resolved import-scoped by the dot accessor (see member.go). Lazily
	// allocated by AddMethod/AddProperty.
	Methods    map[string]map[string]methodExt
	Properties map[string]map[string]propExt
}

type tfile struct {
	FileName string
	Path     string // display path relative to the run root ("std/io/io.phl")
	Src      string // full source text, retained for diagnostic excerpts
	Pkg      *tpackage
	Imports  map[string]Tval // alias -> KindPackage / KindGoPackage value
	Tree     ttnode
	Mode     string // ModeProgram (.pho) or ModeLibrary (.phl)

	// Annotations holds the evaluated parse-time annotation results for
	// this file, set by the loader through modload.AnnotationStasher (which
	// pkg/annot installs). Typed `any` to keep core free of an annot import;
	// the concrete value is a span-keyed table consumers type-assert. Nil
	// when the file has no annotations.
	Annotations any
}

// File modes. ModeProgram (.pho) files allow arbitrary top-level
// expressions; ModeLibrary (.phl) files only allow declaration and
// import forms at the top level.
const (
	ModeProgram = "program"
	ModeLibrary = "library"
)

type tstruct struct {
	Fields  []string
	Methods map[string]tfun
	// Properties are computed fields delegated to a getter and optional
	// setter (anonymous methods), registered by the `property` builtin. The
	// Dot accessor and `=` consult this when a plain field lookup misses.
	Properties map[string]tproperty
	// StaticMethods / StaticProperties are TYPE-level members declared with
	// `static method`/`static property`: they are reached through the struct's
	// TYPE value (`Point.At`), not an instance, and their receiver param `Self`
	// is bound to that type value. Kept per-struct (not in the prim:type
	// extension table, which every type value would share).
	StaticMethods    map[string]tfun
	StaticProperties map[string]tproperty
	Origin           *tenv
}

// tproperty is a computed field/variable: reads call Getter, writes call
// Setter. Both are funs (free-standing property) or anonymous methods
// (struct-field property, self bound from the instance). HasSetter is false
// for a read-only property — writing one is an error.
type tproperty struct {
	Getter    Tval
	Setter    Tval
	HasSetter bool
}

type tinstance struct {
	Struct     *tstruct
	Fields     map[string]Tval
	Privileged bool
}

type tmethod struct {
	Struct *tstruct
	Fun    tfun
}

const (
	KindNum       = "num"
	KindArray     = "array"
	KindDict      = "dict"
	KindStr       = "str"
	KindChr       = "chr"
	KindAtom      = "atom"
	KindBool      = "bool"
	KindNil       = "nil"
	KindFun       = "fun"
	KindMacro     = "macro"
	KindInstance  = "instance"
	KindMethod    = "method"
	KindPackage   = "package"
	KindGoPackage = "gopackage"
	KindProperty  = "property"
	KindType      = "type"
)

var (
	TvNil = Tval{nil, KindNil}
)

// Public aliases — exported names other packages use to reference these
// types. Aliases (= rather than =:) so the underlying types are identical;
// type assertions and conversions work freely between the two names.
type (
	Node       = ttnode
	Branch     = ttbranch
	Leaf       = ttleaf
	Value      = Tval
	StackEntry = tStackEntry
	Fun        = tfun
	ScopeCtx   = tcontext
	Env        = tenv
	Package    = tpackage
	File       = tfile
	Struct     = tstruct
	Instance   = tinstance
	Method     = tmethod
	Property   = tproperty
)
