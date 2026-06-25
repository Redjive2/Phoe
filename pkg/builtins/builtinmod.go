package builtins

import (
	"embed"
	"strings"
	"sync"

	"pho/pkg/core"
	"pho/pkg/goop"
	"pho/pkg/syntax"
)

// The built-in object-model module: thin Pho bindings (pkg/builtins/pho/*.phl)
// that attach the core methods/properties to the primitive and universal
// types — .Size / .Keys (replacing len/keyof), and the universal Is? / In?.
// It is loaded once by the runtime and its members are always in scope (no
// import required), per Doc/PlanV1/ObjectModel.md §4.5.

//go:embed pho/*.phl
var builtinModuleFS embed.FS

var (
	builtinOnce sync.Once
	builtinPkg  *core.Package
)

// init wires the built-in module into core's import-scoped member resolver, so
// the dot accessor consults it for every value. Loading is deferred to first
// use (the first member resolution), keeping NewEnv cheap and avoiding work in
// envs that never dispatch a member.
func init() {
	core.BuiltinExtensions = builtinModule
}

// builtinModule loads the embedded built-in module once and returns the package
// holding its extension tables. Returns nil only if the embedded sources fail
// to load (they are compiled in, so that is not expected).
func builtinModule() *core.Package {
	builtinOnce.Do(func() {
		// The bindings call the `stdDependencies` goop module — e.g.
		// (prim.Size self). Ensure it is exposed; Expose is idempotent.
		goop.Expose(goop.StdDependenciesModule())

		env := NewEnv()
		pkg := &core.Package{
			Path:    "<builtin>",
			Files:   map[string]*core.File{},
			Exports: map[string]core.Value{},
			Env:     &env,
		}
		// One package-level frame, shared across the module's files (mirrors
		// modload). Methods/properties attach to pkg's extension tables.
		ctx := core.Context{Env: &env, Package: pkg}
		ctx.PushFrame()

		// Go-native universal methods, attached before the .phl bindings load.
		// Membership `(x.Is? T)` lives here, NOT as a builtin, so the dot form
		// is the only way to test a type. Being pure Go, it is also immune to
		// any breakage in the macro/do pipeline the .phl bindings rely on.
		ctx.AddMethod(core.UnknownTypeKey, "Is?", unknownIsMethod, false, true)

		entries, err := builtinModuleFS.ReadDir("pho")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".phl") {
				continue
			}
			src, err := builtinModuleFS.ReadFile("pho/" + e.Name())
			if err != nil {
				continue
			}
			file := &core.File{
				FileName: e.Name(),
				Path:     "pho/" + e.Name(),
				Src:      string(src),
				Pkg:      pkg,
				Imports:  map[string]core.Value{},
				Mode:     core.ModeLibrary,
			}
			pkg.Files[e.Name()] = file
			fileCtx := ctx.WithFile(file)

			tokens, _ := syntax.LexPos(string(src))
			tree, _ := syntax.ParsePos(tokens)
			lowered, ok := syntax.Lower(tree).(core.Branch)
			if !ok {
				continue
			}
			for _, form := range lowered {
				form.Evaluate(fileCtx)
			}
		}
		builtinPkg = pkg
	})
	return builtinPkg
}
