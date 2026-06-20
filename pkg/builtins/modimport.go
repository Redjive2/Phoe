package builtins

import (
	"regexp"
	"strings"

	"pho/pkg/core"
	"pho/pkg/goop"
	"pho/pkg/modload"
)

// importPathRe matches an import path: a slash-separated chain of
// identifiers (`std/io`). Compiled once and shared by `import` and
// `goimport` rather than rebuilt on every call.
var importPathRe = regexp.MustCompile(
	"^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))(/[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9])))*$",
)

// modimportBuiltins returns the import surface: `import` for Pho packages
// and `goimport` for Go-side modules registered via goop.Expose.
func modimportBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"goimport": global(func(ctx core.Context, argv []core.Node) core.Value {
			if ctx.File == nil {
				return ctx.Errorf(core.ErrBadImport, "'goimport' called outside of a file context")
			}

			requests := parseImportRequests(ctx, argv, "goimport")

			for _, req := range requests {
				str, alias := req.PackagePath, req.Alias

				if _, found := ctx.File.Imports[alias]; found {
					ctx.Errorf(core.ErrBadImport, "cannot override previously imported go package '.../%s' with go package '%s'", alias, str)
					continue
				}

				if !importPathRe.MatchString(str) {
					ctx.Errorf(core.ErrBadImport, "invalid go import path '%s'", str)
					continue
				}

				var (
					parts               = strings.Split(str, "/")
					mod, foundTopModule = goop.GoModules[parts[0]]
				)

				if !foundTopModule {
					ctx.Errorf(core.ErrBadImport, "cannot find go parent module '%s' in go import path '%s'", parts[0], str)
					continue
				}

				for i := 1; i < len(parts); i++ {
					child, found := mod.Children[parts[i]]
					if !found {
						ctx.Errorf(core.ErrBadImport, "cannot find go module '%s' in go import path '%s'", parts[i], str)
						mod = nil
						break
					}
					mod = child
				}

				if mod == nil {
					continue
				}

				ctx.File.Imports[alias] = core.TvGoPackage(mod)
			}

			return core.TvNil
		}),

		"import": global(func(ctx core.Context, argv []core.Node) core.Value {
			if ctx.File == nil {
				return ctx.Errorf(core.ErrBadImport, "'import' called outside of a file context")
			}

			requests := parseImportRequests(ctx, argv, "import")

			for _, req := range requests {
				str, alias := req.PackagePath, req.Alias

				if _, found := ctx.File.Imports[alias]; found {
					ctx.Errorf(core.ErrBadImport, "cannot override previously imported package '.../%s' with package '%s'", alias, str)
					continue
				}

				if !importPathRe.MatchString(str) {
					ctx.Errorf(core.ErrBadImport, "invalid import path '%s'", str)
					continue
				}

				// Frame the load so an error inside a freshly-loading
				// dependency shows which import pulled it in.
				ctx.PushCallFrame(`import "` + str + `"`)
				pkg, err := modload.LoadPackage(str)
				ctx.PopCallFrame()
				if err != nil {
					// A parse failure in the dependency already rendered its
					// own diagnostics; this adds the importing site + trace.
					ctx.Errorf(core.ErrBadImport, "%s", err.Error())
					continue
				}

				ctx.File.Imports[alias] = core.TvPackage(pkg)
			}

			return core.TvNil
		}),
	}
}
