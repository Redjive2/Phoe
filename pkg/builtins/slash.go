package builtins

import (
	"os"

	"pho/pkg/core"
	"pho/pkg/goop"
	"pho/pkg/modload"
)

// slashBuiltins returns the mangled core.Slash accessor.
//
// The `a/b/c` surface syntax is rewritten by the parser into nested
// (core.Slash a b) calls (core.Slash is the randomized internal name from
// mangle.go). It navigates an imported namespace — a Pho `import` package or a
// `goimport` goop module — resolving each segment as:
//
//	package    — an export of the package, else a SUBPACKAGE (a subdirectory,
//	             lazily loaded once); the two coexist so `std/core/print-line`
//	             walks std → core (subpackage) → print-line (export).
//	go module  — a Go-side method binding (returned as a wrapper core.Fun).
//
// Value/type member access (struct fields, methods, indexing) stays on `.`
// (core.Dot); `/` on a non-package value is an error.
func slashBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		core.Slash: global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "the slash accessor requires exactly 2 operands; got %d", len(argv))
			}

			col := argv[0].Evaluate(ctx)
			member, ok := core.AsLeaf(argv[1])
			if !ok {
				return ctx.Errorf(core.ErrField, "package navigation uses a bare name: expected an identifier after '/', got '%s'", core.Inspect(argv[1]))
			}
			name := string(member)

			switch col.Kind {
			case core.KindPackage:
				pkg := col.Val.(*core.Package)
				// An export of this package wins over a same-named subpackage.
				if v, found := packageExport(ctx, pkg, name); found {
					return v
				}
				// Else a subpackage: a subdirectory, lazily loaded.
				if sub, ok := loadSubpackage(pkg, name); ok {
					return core.TvPackage(sub)
				}
				return ctx.Errorf(core.ErrField, "package '%s' has no export or subpackage '%s'", pkg.Path, name)

			case core.KindGoPackage:
				gopkg := col.Val.(*goop.PhoModule)
				return core.TvFun(func(ctx core.Context, callArgv []core.Node) core.Value {
					args, ok := core.DistributeSpreadExpressions(ctx, callArgv)
					if !ok {
						return core.TvNil
					}
					ctx.PushCallFrame("go:" + gopkg.Name + "/" + name)
					defer ctx.PopCallFrame()
					res, err := goop.Call(gopkg, name, args)
					if err != nil {
						return ctx.Errorf(core.ErrGoCall, "%s", err.Error())
					}
					return core.TvUnknown(res)
				})

			default:
				return ctx.Errorf(core.ErrType, "'/' navigates a package or goimport module; got a value of kind '%s' — use '.' for value members", col.Kind)
			}
		}),
	}
}

// packageExport resolves an export of pkg by name. A callable export is
// wrapped as a Fun; a non-callable one reads the LIVE binding from the
// package's own top frame (so importers see the module's own updates) and
// delegates a property to its getter. Mirrors the retired KindPackage branch
// of the dot accessor.
func packageExport(ctx core.Context, pkg *core.Package, name string) (core.Value, bool) {
	val, found := pkg.Exports[name]
	if !found {
		return core.TvNil, false
	}
	if val.Kind == core.KindFun {
		return core.TvFun(val.Val.(core.Fun)), true
	}
	if len(pkg.Env.Stack) > 0 {
		if live, ok := pkg.Env.Stack[0][name]; ok {
			if live.Val.Kind == core.KindProperty {
				return core.ReadProperty(ctx, live.Val), true
			}
			return live.Val, true
		}
	}
	if val.Kind == core.KindProperty {
		return core.ReadProperty(ctx, val), true
	}
	return val, true
}

// loadSubpackage returns the subpackage at pkg.Path/name when that path is a
// directory, loading it lazily (modload caches, so repeat navigations are
// cheap). ok=false when there is no such subdirectory; a lex/eval error INSIDE
// a real subpackage surfaces through modload's diagnostic session.
func loadSubpackage(pkg *core.Package, name string) (*core.Package, bool) {
	subPath := pkg.Path + "/" + name
	info, err := os.Stat(subPath)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	sub, err := modload.LoadPackage(subPath)
	if err != nil {
		return nil, false
	}
	return sub, true
}
