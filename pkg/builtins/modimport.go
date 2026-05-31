package builtins

import (
	"fmt"
	"regexp"
	"strings"

	"pho/pkg/core"
	"pho/pkg/goop"
	"pho/pkg/modload"
)

// modimportBuiltins returns the import surface: `import` for Pho packages
// and `goimport` for Go-side modules registered via goop.Expose.
func modimportBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"goimport": global(func(ctx core.Context, argv []core.Node) core.Value {
			if ctx.File == nil {
				fmt.Println("(ERR): 'goimport' called outside of a file context @ 'builtins.goimport'.")
				return core.TvNil
			}

			requests := parseImportRequests(ctx, argv, "goimport")

			for _, req := range requests {
				str, alias := req.PackagePath, req.Alias

				if _, found := ctx.File.Imports[alias]; found {
					fmt.Println("(ERR) cannot override previously imported go package '.../" + alias + "' with go package '" + str + "' @ 'builtins.goimport'")
					continue
				}

				if !regexp.MustCompile(
					"^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))(/[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9])))*$",
				).MatchString(str) {
					fmt.Println("(ERR) invalid path '" + str + "' passed @ 'builtins.goimport'.")
					continue
				}

				var (
					parts               = strings.Split(str, "/")
					mod, foundTopModule = goop.GoModules[parts[0]]
				)

				if !foundTopModule {
					fmt.Println("(ERR) cannot find go parent module '" + parts[0] + "' in go import path '" + str + "' passed @ 'builtins.goimport'.")
					continue
				}

				for i := 1; i < len(parts); i++ {
					child, found := mod.Children[parts[i]]
					if !found {
						fmt.Println("(ERR) cannot find go module '" + parts[i] + "' in go import path '" + str + "' passed @ 'builtins.goimport'.")
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
				fmt.Println("(ERR): 'import' called outside of a file context @ 'builtins.import'.")
				return core.TvNil
			}

			requests := parseImportRequests(ctx, argv, "import")

			for _, req := range requests {
				str, alias := req.PackagePath, req.Alias

				if _, found := ctx.File.Imports[alias]; found {
					fmt.Println("(ERR) cannot override previously imported package '.../" + alias + "' with package '" + str + "' @ 'builtins.import'")
					continue
				}

				// path regex:  ^ident(/ident)*$
				if !regexp.MustCompile(
					"^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))(/[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9])))*$",
				).MatchString(str) {
					fmt.Println("(ERR) invalid path '" + str + "' passed @ 'builtins.import'.")
					continue
				}

				pkg, err := modload.LoadPackage(str)
				if err != nil {
					fmt.Println(str)
					fmt.Println("(ERR) " + err.Error() + " @ 'builtins.import'.")
					continue
				}

				ctx.File.Imports[alias] = core.TvPackage(pkg)
			}

			return core.TvNil
		}),
	}
}
