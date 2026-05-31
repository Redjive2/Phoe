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

type tcontext struct {
	Captures      map[string]func(Tval, bool) Tval
	MaxStackDepth int
}

type tenv struct {
	Globals  *map[string]tStackEntry
	Stack    []map[string]tStackEntry
	CtxStack []tcontext
	Structs  map[string]*tstruct // Maps the address of the 'new' function returned by 'struct'
	// to the struct's underlying representation. Used to add methods.
	InstStack []Tval // all 'tinstance's
}

type tpackage struct {
	Path    string            // canonical import path (the directory)
	Files   map[string]*tfile // filename -> file
	Exports map[string]Tval   // capitalized identifiers, merged across files
	Env     *tenv             // package-private env shared across files
}

type tfile struct {
	FileName string
	Pkg      *tpackage
	Imports  map[string]Tval // alias -> KindPackage / KindGoPackage value
	Tree     ttnode
	Mode     string // ModeProgram (.pho) or ModeLibrary (.phl)
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
	Origin  *tenv
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

type tconstructor struct {
	StructName  string
	StructData  *tstruct
	Constructor tfun
}

const (
	KindNum         = "num"
	KindArray       = "array"
	KindDict        = "dict"
	KindStr         = "str"
	KindChr         = "chr"
	KindBool        = "bool"
	KindNil         = "nil"
	KindFun         = "fun"
	KindInstance    = "instance"
	KindMethod      = "method"
	KindPackage     = "package"
	KindGoPackage   = "gopackage"
	KindConstructor = "constructor"
)

var (
	TvNil = Tval{nil, KindNil}
)

// Public aliases — exported names other packages use to reference these
// types. Aliases (= rather than =:) so the underlying types are identical;
// type assertions and conversions work freely between the two names.
type (
	Node        = ttnode
	Branch      = ttbranch
	Leaf        = ttleaf
	Value       = Tval
	StackEntry  = tStackEntry
	Fun         = tfun
	ScopeCtx    = tcontext
	Env         = tenv
	Package     = tpackage
	File        = tfile
	Struct      = tstruct
	Instance    = tinstance
	Method      = tmethod
	Constructor = tconstructor
)
