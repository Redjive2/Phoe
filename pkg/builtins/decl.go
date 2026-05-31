package builtins

import (
	"fmt"
	"slices"
	"strconv"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// declBuiltins returns the declaration / binding / assignment builtins:
// var, const, fun, method, struct, =, block.
func declBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"fun": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) == 2 {
				argBranch := syntax.Derepr(argv[0]).(core.Branch)
				argList := make([]string, len(argBranch))

				for i, node := range argBranch {
					leaf, _ := node.(core.Leaf)
					argList[i] = string(leaf)
				}

				return core.TvFun(core.BindFun(syntax.Derepr(argv[1]), argList, ctx))
			}

			if len(argv) != 3 {
				fmt.Println("(ERR) invalid argument count '" + fmt.Sprint(len(argv)) + "' passed @ 'builtins.fun'.")
				return core.TvNil
			}

			funName := argv[0].Evaluate(ctx).Val.(string)

			argBranch := syntax.Derepr(argv[1]).(core.Branch)
			argList := make([]string, len(argBranch))

			for i, node := range argBranch {
				if branch, ok := node.(core.Branch); ok {
					if i == len(argBranch)-1 &&
						len(branch) == 2 &&
						branch[0] == core.Leaf("spread") {

						leaf := branch[1].(core.Leaf)
						argList[i] = fmt.Sprint("#", leaf)
						break
					}

					fmt.Println("(ERR) cannot parse argument pattern '" + fmt.Sprint(argBranch[i]) + "' passed @ 'builtins.fun'.")
					return core.TvNil
				}

				leaf, _ := node.(core.Leaf)
				argList[i] = string(leaf)
			}

			if !ctx.Declare(funName, core.TvFun(core.BindFun(syntax.Derepr(argv[2]), argList, ctx)), true) {
				fmt.Println("(ERR) cannot declare function '" + funName + "': name already in use @ 'builtins.fun'.")
			}

			return core.TvNil
		}),

		"method": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (method myStruct 'name '(self m)
			//      '(+ self.n m)
			// )

			var (
				baseStructPtr = argv[0].Evaluate(ctx).Val.(core.Constructor).StructData

				methodName = argv[1].Evaluate(ctx).Val.(string)

				argBranch = syntax.Derepr(argv[2]).(core.Branch)
				argList   = make([]string, len(argBranch))
			)

			for i, node := range argBranch {
				if branch, ok := node.(core.Branch); ok {
					if i == len(argBranch)-1 &&
						len(branch) == 2 &&
						branch[0] == core.Leaf("spread") {

						leaf := branch[1].(core.Leaf)
						argList[i] = fmt.Sprint("#", leaf)
						break
					}

					fmt.Println("(ERR) cannot parse argument pattern '" + fmt.Sprint(argBranch[i]) + "' passed @ 'builtins.method'.")
					return core.TvNil
				}

				leaf := node.(core.Leaf)
				argList[i] = string(leaf)
			}

			method := core.BindMethod(syntax.Derepr(argv[3]), argList, ctx)

			baseStructPtr.Methods[methodName] = method

			//if !ctx.Declare(methodName, core.TvMethod(baseStructPtr, method), true) {
				//fmt.Println("(ERR) cannot declare method '" + methodName + "': name already in use @ 'builtins.method'.")
				//}

			return core.TvNil
		}),

		"block": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (block '(+ 1 2))
			return core.TvFun(core.BindCallback(syntax.Derepr(argv[0])))
		}),

		// only two valid syntax types:
		//   (= 'ident newVal)             -->   (= "ident" newVal)
		//   (= collection.index newVal)   -->   (= (core.Dot collection index) newVal)
		"=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if _, ok := argv[0].(core.Leaf); ok {
				var (
					ident  = string(syntax.Derepr(argv[0]).(core.Leaf))
					newVal = argv[1].Evaluate(ctx)
				)

				ctx.Set(ident, newVal)
				return core.TvNil
			}

			br := argv[0].(core.Branch)

			if len(br) != 3 {
				fmt.Println("(ERR) cannot parse assignment target '" + core.Inspect(br) + "' of length '" + strconv.Itoa(len(br)) + "'. Expected length '3' and dot syntax @ 'builtins.`=`'.")
				return core.TvNil
			}

			var (
				collection = br[1].Evaluate(ctx)
				index      = br[2].Evaluate(ctx)
			)

			if collection.Kind == core.KindInstance {
				instance := collection.Val.(*core.Instance)
				instance.Fields[index.Val.(string)] = argv[1].Evaluate(ctx)
				return core.TvNil
			}

			fmt.Println("(ERR) cannot set fields of non-struct instance type '" + collection.Kind + "' passed @ 'builtins.`=`'.")
			return core.TvNil
		}),

		"var": global(func(ctx core.Context, argv []core.Node) core.Value {
			// Mutable bindings are allowed in two places:
			//   - inside function/method bodies
			//   - at the top level of a .pho program file
			// .phl library files reject top-level `var` because their
			// declarations are package-visible (in principle) and the
			// cross-file reasoning the linter does on package scopes
			// needs them to be immutable.
			inProgram := ctx.File != nil && ctx.File.Mode == core.ModeProgram
			if !ctx.InFunction && !inProgram {
				fmt.Println("(ERR) 'var' is not allowed at the top level of a library file — use 'const' instead @ 'builtins.var'.")
				return core.TvNil
			}

			type constRequest struct {
				Identifier string
				Repr       core.Node
			}

			if len(argv)%2 != 0 {
				fmt.Println("(ERR) cannot parse argument list of length '" + fmt.Sprint(len(argv)) + "' passed. Must be a multiple of 2 @ 'builtins.var'.")
				return core.TvNil
			}

			var requests []constRequest

			for i := 0; i < len(argv)-1; i += 2 {
				requests = append(requests, constRequest{string(syntax.Derepr(argv[i]).(core.Leaf)), argv[i+1]})
			}

			for _, request := range requests {
				if _, found := ctx.Env.Stack[0][request.Identifier]; found {
					fmt.Println("(ERR) cannot redeclare value '" + request.Identifier + "' passed @ 'builtins.var'.")
					return core.TvNil
				}

				ctx.Declare(request.Identifier, request.Repr.Evaluate(ctx), false)
			}

			return core.TvNil
		}),

		"const": global(func(ctx core.Context, argv []core.Node) core.Value {
			type constRequest struct {
				Identifier string
				Repr       core.Node
			}

			if len(argv)%2 != 0 {
				fmt.Println("(ERR) cannot parse argument list of length '" + fmt.Sprint(len(argv)) + "' passed. Must be a multiple of 2 @ 'builtins.const'.")
				return core.TvNil
			}

			var requests []constRequest

			for i := 0; i < len(argv)-1; i += 2 {
				requests = append(requests, constRequest{string(syntax.Derepr(argv[i]).(core.Leaf)), argv[i+1]})
			}

			for _, request := range requests {
				if _, found := ctx.Env.Stack[0][request.Identifier]; found {
					fmt.Println("(ERR) cannot redeclare value '" + request.Identifier + "' passed @ 'builtins.const'.")
					return core.TvNil
				}

				ctx.Declare(request.Identifier, request.Repr.Evaluate(ctx), true)
			}

			return core.TvNil
		}),

		// (struct 'myStruct '(PublicField privateField))
		// (var 'myInst (myStruct { 'PublicField 1
		//							'privateField 2 })
		"struct": global(func(ctx core.Context, argv []core.Node) core.Value {
			args, ok := ParseArgs(ctx, "builtins.struct", []string{core.KindStr, core.KindArray}, argv)
			if !ok {
				return core.TvNil
			}

			structName := args[0].(string)

			fieldsNode := *args[1].(*[]core.Value)
			fields := make([]string, len(fieldsNode))

			for i, fieldNode := range fieldsNode {
				fields[i] = fieldNode.Val.(string)
			}

			env := ctx.Env

			structData := core.Struct{
				Fields:  fields,
				Methods: map[string]core.Fun{},
				Origin:  env,
			}

			newFun := core.Fun(func(ctx core.Context, argv []core.Node) core.Value {
				fieldMap := make(map[string]core.Value)

				if len(argv) > 0 {
					arg := *argv[0].Evaluate(ctx).Val.(*map[core.Value]core.Value)

					for key, val := range arg {
						keyStr, success := key.Val.(string)
						if !success {
							fmt.Println("(ERR): Passed invalid type '" + key.Kind + "' as key to struct initializer map @ 'builtins.struct.new'.")
							return core.TvNil
						}

						if !slices.Contains(fields, keyStr) {
							fmt.Println("(ERR): Passed invalid key '" + keyStr + "' to struct initializer map @ 'builtins.struct.new'.")
							return core.TvNil
						}

						fieldMap[keyStr] = val
					}

					for _, fieldName := range fields {
						if _, found := fieldMap[fieldName]; !found {
							fmt.Println("(WARN): Failed to pass key '" + fieldName + "' to struct initializer map @ 'builtins.struct.new'.")
							fieldMap[fieldName] = core.TvNil
						}
					}
				}

				return core.TvInstance(&structData, fieldMap, false, false)
			})

			// it's a hacky solution at best, but a solution nonetheless
			ctx.Env.Structs[structName] = &structData

			ctx.Declare(structName, core.TvConstructor(structName, &structData, newFun), true)

			return core.TvNil
		}),
	}
}
