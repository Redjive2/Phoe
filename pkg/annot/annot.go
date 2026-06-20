// Package annot evaluates Pho parse-time annotations — the `--@ (form)`
// expressions pkg/syntax captures onto a top-level form (ast.PBranch.
// Annotations). Each annotation runs in a fresh, fully isolated interpreter
// environment (the standard builtins plus a macro overlay), under its own
// diagnostic session, so one annotation can neither observe nor corrupt
// another, nor the program being compiled.
//
// Annotations communicate results by side effect rather than return value:
// a macro calls (meta.Attach 'key value) on the phoAnnot Go module, which
// records the pair against the annotation currently being evaluated. The
// macro's own return value is discarded — which is why the macro-call
// `resume` wrapper is stripped before evaluation (see prepare/deBang).
//
// Annotations are evaluated sequentially at parse time. The package is not
// safe for concurrent Evaluate calls: the phoAnnot sink is process-global,
// mirroring goop's global module registry.
package annot

import (
	"fmt"
	"runtime/debug"
	"sync"

	"pho/pkg/ast"
	"pho/pkg/builtins"
	"pho/pkg/core"
	"pho/pkg/diag"
	"pho/pkg/goop"
	"pho/pkg/modload"
	"pho/pkg/span"
	"pho/pkg/syntax"
)

// FileAnnotations is the value stashed on core.File.Annotations: each
// annotated top-level form's span mapped to that form's evaluated
// annotation results. Consumers type-assert core.File.Annotations to this.
type FileAnnotations = map[span.Span][]Result

func init() {
	// Let the package loader stash annotation results on each core.File
	// without importing this package (which imports modload).
	modload.AnnotationStasher = stashAnnotations
}

// stashAnnotations evaluates the parse-time annotations on a freshly-parsed
// file's top-level forms and stashes them on file.Annotations as a
// span-keyed table. Installed on modload.AnnotationStasher. tree is the
// loader's []ast.PNode (passed as any to keep modload free of pkg/ast).
func stashAnnotations(tree any, file *core.File) {
	pnodes, ok := tree.([]ast.PNode)
	if !ok {
		return
	}
	table := FileAnnotations{}
	for _, form := range pnodes {
		br, ok := form.(*ast.PBranch)
		if !ok || len(br.Annotations) == 0 {
			continue
		}
		table[br.Span] = defaultEval.EvaluateBranch(br)
	}
	if len(table) > 0 {
		file.Annotations = table
	}
}

// Entry is one key/value pair an annotation attached via (meta.Attach 'k v),
// kept in attachment order.
type Entry struct {
	Key   string
	Value core.Value
}

// Result is the outcome of evaluating a single annotation: the metadata it
// attached and any diagnostics it raised. A clean annotation has empty Diags.
type Result struct {
	Entries []Entry
	Diags   []diag.RuntimeError
}

// Evaluator evaluates parse-time annotations. macros is the overlay of
// annotation builtins — the sig!/type!/... definitions harvested from the
// macro library, installed into every annotation env (empty until the
// harvest phase wires it up). Results are memoized by the annotation's raw
// source text, so an editor re-linting on every keystroke pays for each
// distinct annotation only once.
type Evaluator struct {
	macros map[string]core.StackEntry
	memo   map[string]Result
}

// New returns an Evaluator whose annotation envs include macros as builtins.
func New(macros map[string]core.StackEntry) *Evaluator {
	ensureExposed()
	if macros == nil {
		macros = map[string]core.StackEntry{}
	}
	return &Evaluator{macros: macros, memo: map[string]Result{}}
}

// LoadMacros loads the annotation-macro library at importPath (e.g.
// "std/annot") through the package loader and harvests its top-level
// definitions into an overlay ready for New. The library is an ordinary
// .phl with the full goop + stdlib toolchain available; harvesting its whole
// package frame (not just the capitalized Exports) is what installs the
// lowercase macro names — `sig`, `type`, ... — that the `(name! ...)`
// lowering resolves to.
//
// goop is exposed first (the std toolchain plus the phoAnnot sink) so the
// library's goimports resolve at load time. The package loader caches its
// result, so repeat calls are cheap. A load failure (missing library, or a
// parse/eval error inside it) is returned so the caller can decide whether
// annotations are available for the run.
func LoadMacros(importPath string) (map[string]core.StackEntry, error) {
	goop.Expose(goop.StdDependenciesModule())
	ensureExposed()

	// Capture (and discard) the library's own load diagnostics in a silent
	// session: a broken macro library degrades to "macros unavailable" — it
	// must not spam the program's stderr on every run. modload's session is
	// package-global and the program resets it before its own load.
	silent := diag.NewSession()
	silent.Report = func(diag.RuntimeError) {}
	modload.SetSession(silent)

	pkg, err := modload.LoadPackage(importPath)
	if err != nil {
		return nil, err
	}

	frame := pkg.Env.Stack[0]
	overlay := make(map[string]core.StackEntry, len(frame))
	for name, entry := range frame {
		overlay[name] = entry
	}
	return overlay, nil
}

// defaultEval is the process-wide annotation evaluator. Lint and the LSP
// reach it through Default so the memo (and the loaded macro library) are
// shared across every file analyzed in the process — an editor re-linting
// per keystroke pays for each distinct annotation once. It starts empty (no
// macros); InitDefault loads a library into it.
var defaultEval = New(nil)

// Default returns the process-wide annotation evaluator. Until InitDefault
// succeeds it carries no macros, so sig!/type!/... resolve as undefined.
// Evaluation is single-threaded — the Evaluator is not safe for concurrent
// use, so callers must serialize annotation evaluation.
func Default() *Evaluator { return defaultEval }

// InitDefault loads the macro library at importPath and installs it as the
// process-wide evaluator (Default). Call once at startup — the CLI after the
// chdir to the entry's directory, the LSP at initialization. On failure
// (missing library, or an error inside it) Default is left unchanged and the
// error returned, so annotations degrade to "macro not found" rather than
// crashing the run.
func InitDefault(importPath string) error {
	macros, err := LoadMacros(importPath)
	if err != nil {
		return err
	}
	defaultEval = New(macros)
	return nil
}

// SetDefault installs e as the process-wide evaluator — for explicit setup
// and tests.
func SetDefault(e *Evaluator) { defaultEval = e }

// EnsureDefault makes sure the process-wide evaluator has its macro library
// loaded, loading it from macrosImportPath if not. A no-op once loaded.
// When still unloaded it re-attempts, but cheaply: the package loader serves
// a remembered parse failure (or a missing directory re-stats) without
// re-reading, so calling this on every analysis is fine. Best-effort — a
// failing load leaves the evaluator empty (annotations report "not defined")
// until ReloadDefault or a successful retry.
func EnsureDefault(macrosImportPath string) {
	if len(defaultEval.macros) > 0 {
		return
	}
	_ = InitDefault(macrosImportPath)
}

// ReloadDefault forces a fresh load of the macro library at macrosImportPath,
// discarding any cached version, and installs it as the process-wide
// evaluator. The LSP calls it when the library's own source changes, so a
// long-lived session picks up edits (including a fix to a broken library)
// without a restart.
func ReloadDefault(macrosImportPath string) {
	modload.Invalidate(macrosImportPath)
	_ = InitDefault(macrosImportPath)
}

// EvaluateBranch evaluates every annotation attached to br, in source order,
// returning one Result per annotation (nil if br carries none).
func (e *Evaluator) EvaluateBranch(br *ast.PBranch) []Result {
	if br == nil || len(br.Annotations) == 0 {
		return nil
	}
	out := make([]Result, len(br.Annotations))
	for i, a := range br.Annotations {
		out[i] = e.Evaluate(a.Raw, a.Form)
	}
	return out
}

// Evaluate runs one annotation's form in a fresh isolated environment and
// returns the metadata it attached. raw is the verbatim annotation body,
// used as the memo key.
func (e *Evaluator) Evaluate(raw string, form ast.PNode) Result {
	if r, ok := e.memo[raw]; ok {
		return r
	}

	node := prepare(form)

	// A fresh env per annotation is the isolation boundary: builtins plus
	// the macro overlay, nothing shared with the program or other
	// annotations. The macro closures carry their own defining file/imports
	// (BindFun captures defCtx), so they still reach goop and their own
	// stdlib even though this env itself has no File.
	env := builtins.NewEnv()
	for k, v := range e.macros {
		(*env.Globals)[k] = v
	}

	// A private session captures the annotation's diagnostics instead of
	// letting them reach the program's reporter or stderr.
	var diags []diag.RuntimeError
	session := diag.NewSession()
	session.Report = func(d diag.RuntimeError) { diags = append(diags, d) }

	// Route (meta.Attach ...) side effects into this annotation's entries
	// for the duration of the run, then detach.
	var entries []Entry
	theHost.current = &entries
	defer func() { theHost.current = nil }()

	ctx := core.Context{Env: &env, Diag: session}
	ctx.PushFrame()
	evalNode(node, ctx)

	res := Result{Entries: entries, Diags: diags}
	e.memo[raw] = res
	return res
}

// prepare lowers the annotation form to a runtime node and unwraps the
// macro-call sugar: a `(name! a ...)` form lowers to `(macrocall name 'a ...)`,
// but an annotation never uses the macro's return value — the work happens
// through (meta.Attach ...) side effects — so we evaluate the inner
// `(name 'a ...)` call directly and skip the macro `resume`'s re-evaluation.
// A non-macro annotation form lowers and evaluates unchanged.
func prepare(form ast.PNode) core.Node {
	if form == nil {
		return nil
	}
	top, ok := syntax.Lower([]ast.PNode{form}).(core.Branch)
	if !ok || len(top) == 0 {
		return nil
	}
	return deBang(top[0])
}

// deBang unwraps the macro-call sugar `(macrocall name 'a ...)` into the
// direct call `(name 'a ...)`, carrying the whole form's span. Evaluating the
// inner call straight runs the harvested helper (a plain function in the
// overlay) for its (meta.Attach ...) side effects, skipping both the strict
// macro-kind check the Macrocall builtin would impose and the resume's
// re-evaluation of generated code the annotation has no use for. Any other
// node (a bang-less annotation form) is returned unchanged. Span wrappers are
// seen through (AsBranch/AsLeaf).
func deBang(node core.Node) core.Node {
	br, ok := core.AsBranch(node)
	if !ok || len(br) < 2 {
		return node
	}
	if head, ok := core.AsLeaf(br[0]); !ok || string(head) != core.Macrocall {
		return node
	}
	inner := br[1:]
	if sp, ok := core.SpanOf(node); ok {
		return core.WithSpan(inner, sp)
	}
	return inner
}

// evalNode evaluates node under ctx with the same control-flow and panic
// guards modload uses for top-level forms: a stray return/break/continue,
// the recursion limit, and any foreign Go panic from a misbehaving macro all
// become diagnostics on ctx's (private) session rather than crashing the
// host. Diagnostics are positioned at the node's span.
func evalNode(node core.Node, ctx core.Context) {
	if node == nil {
		return
	}
	if sp, ok := core.SpanOf(node); ok {
		ctx.At = &sp
	}
	base := ctx.Diag.Depth()
	ctx.PushCallFrame("<annotation>")
	defer ctx.Diag.Truncate(base)
	defer func() {
		switch r := recover(); r.(type) {
		case nil:
		case core.ReturnSignal:
			ctx.Errorf(core.ErrTopLevelFlow, "'return' is not valid in an annotation")
		case core.BreakSignal:
			ctx.Errorf(core.ErrTopLevelFlow, "'break' is not valid in an annotation")
		case core.ContinueSignal:
			ctx.Errorf(core.ErrTopLevelFlow, "'continue' is not valid in an annotation")
		case core.RecursionSignal:
			ctx.EmitPanic(core.ErrRecursion,
				fmt.Sprintf("recursion limit exceeded (%d calls) in an annotation", core.MaxCallDepth()),
				"deep or infinite recursion in an annotation macro")
		default:
			note := "this is likely a bug in an annotation macro; re-run with PHO_DEBUG=1 for the Go stack"
			if core.DebugMode {
				note = "Go stack:\n" + string(debug.Stack())
			}
			ctx.EmitPanic(core.ErrGoPanic,
				fmt.Sprintf("runtime panic while evaluating annotation: %v", r), note)
		}
	}()
	node.Evaluate(ctx)
}

// --- phoAnnot goop module: the sink macros write metadata to ---

// host backs the phoAnnot Go module. current points at the entry slice of
// the annotation being evaluated right now (set by Evaluate around each
// run); a nil current means no annotation is in flight, so a stray Attach is
// dropped rather than panicking.
type host struct {
	current *[]Entry
}

// Attach records one metadata pair on the in-flight annotation. Called from
// Pho as (meta.Attach 'key value); capitalized so goop's reflective dispatch
// finds it. value keeps its Pho Kind — BuildCallArgs passes a core.Value
// parameter through untouched.
func (h *host) Attach(key string, value core.Value) {
	if h.current == nil {
		return
	}
	*h.current = append(*h.current, Entry{Key: key, Value: value})
}

var (
	theHost    = &host{}
	exposeOnce sync.Once
)

// ensureExposed registers the phoAnnot module with goop exactly once. The
// macro library reaches it via (goimport ("phoAnnot" meta)).
func ensureExposed() {
	exposeOnce.Do(func() {
		goop.Expose(&goop.PhoModule{Name: "phoAnnot", Data: theHost})
	})
}
