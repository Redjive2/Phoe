package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

var (
	activeEnv     *tenv
	activePackage *tpackage
	activeFile    *tfile
)

func global(fn func(argv []ttnode) tval) tStackEntry {
	return tStackEntry{TvFun(fn), true}
}

func NewEnv() tenv {
	stack := []map[string]tStackEntry{{
		// noop; only used as a marker for the interpreter
		"spread": global(func(argv []ttnode) tval {
			return TvNil
		}),

		"goimport": global(func(argv []ttnode) tval {
			type importRequest struct {
				PackagePath string
				Alias       string
			}

			args := make([]importRequest, len(argv))
			for i, argNode := range argv {
				arg := argNode.Evaluate()

				// "path/to/lib" -> importRequest{"path/to/lib", "lib"}
				if arg.Kind == KindStr {
					var (
						parts   = strings.Split(arg.Val.(string), "/")
						pkgName = parts[len(parts)-1]
					)

					args[i] = importRequest{arg.Val.(string), pkgName}
					continue
				}

				// ["path/to/lib" 'alias] -> importRequest{"path/to/lib", "alias"}
				if arg.Kind == KindArray {
					argArray := *arg.Val.(*[]tval)

					if len(argArray) != 2 || argArray[0].Kind != KindStr || argArray[1].Kind != KindStr {
						fmt.Println("(ERR): cannot parse invalid aliased go import request '" + fmt.Sprint(arg.Val) + "' passed @ 'builtins.goimport'")
						continue
					}

					args[i] = importRequest{argArray[0].Val.(string), argArray[1].Val.(string)}
					continue
				}

				fmt.Println("(ERR): cannot parse go import request '" + fmt.Sprint(arg.Val) + "' passed @ 'builtins.goimport'")
			}

			for _, arg := range args {
				var (
					str     = arg.PackagePath
					pkgName = arg.Alias
				)

				if _, found := activeEnv.ImportContext[activeEnv.NameStack[0]][pkgName]; found {
					fmt.Println("(ERR) cannot override previously imported go package '.../" + pkgName + "' with go package '" + str + "' @ 'builtins.goimport'")
					continue
				}

				// path regex:  ^ident(/ident)*$
				if !regexp.MustCompile(
					"^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))(/[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9])))*$",
				).MatchString(str) {
					fmt.Println("(ERR) invalid path '" + str + "' passed @ 'builtins.goimport'.")
					continue
				}

				var (
					parts               = strings.Split(str, "/")
					mod, foundTopModule = GoModules[parts[0]]
				)

				if !foundTopModule {
					fmt.Println("(ERR) cannot find go parent module '" + parts[0] + "' in go import path '" + str + "' passed @ 'builtins.goimport'.")
					continue
				}

				if len(parts) > 1 {
					if len(parts) >= 3 {
						for i := 1; i < len(parts)-1; i++ {
							child, found := mod.Children[parts[i]]

							if found {
								mod = child
								continue
							}

							fmt.Println("(ERR) cannot find go parent module '" + parts[0] + "' in go import path '" + str + "' passed @ 'builtins.goimport'.")
						}
					}

					endModule, found := mod.Children[parts[len(parts)-1]]

					if !found {
						fmt.Println("(ERR) cannot find go module '" + parts[len(parts)-1] + "' in go import path '" + str + "' passed @ 'builtins.goimport'.")
						continue
					}

					activeEnv.ImportContext[activeEnv.NameStack[0]][pkgName] = TvGoPackage(endModule)
				}

				activeEnv.ImportContext[activeEnv.NameStack[0]][pkgName] = TvGoPackage(mod)
			}

			return TvNil
		}),

		"import": global(func(argv []ttnode) tval {
			type importRequest struct {
				PackagePath string
				Alias       string
			}

			args := make([]importRequest, len(argv))
			for i, argNode := range argv {
				arg := argNode.Evaluate()

				// "path/to/lib" -> importRequest{"path/to/lib", "lib"}
				if arg.Kind == KindStr {
					var (
						parts   = strings.Split(arg.Val.(string), "/")
						pkgName = parts[len(parts)-1]
					)

					args[i] = importRequest{arg.Val.(string), pkgName}
					continue
				}

				// ["path/to/lib" 'alias] -> importRequest{"path/to/lib", "alias"}
				if arg.Kind == KindArray {
					argArray := *arg.Val.(*[]tval)

					if len(argArray) != 2 || argArray[0].Kind != KindStr || argArray[1].Kind != KindStr {
						fmt.Println("(ERR): cannot parse invalid aliased import request '" + fmt.Sprint(arg.Val) + "' passed @ 'builtins.import'")
						continue
					}

					args[i] = importRequest{argArray[0].Val.(string), argArray[1].Val.(string)}
					continue
				}

				fmt.Println("(ERR): cannot parse import request '" + fmt.Sprint(arg.Val) + "' passed @ 'builtins.import'")
			}

			for _, arg := range args {
				var (
					str     = arg.PackagePath
					pkgName = arg.Alias
				)

				if _, found := activeEnv.ImportContext[activeEnv.NameStack[0]][pkgName]; found {
					fmt.Println("(ERR) cannot override previously imported package '.../" + pkgName + "' with package '" + str + "' @ 'builtins.import'")
				}

				// path regex:  ^ident(/ident)*$
				if !regexp.MustCompile(
					"^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))(/[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9])))*$",
				).MatchString(str) {
					fmt.Println("(ERR) invalid path '" + str + "' passed @ 'builtins.import'.")
					continue
				}

				fileList, err := os.ReadDir(str)

				names := make([]string, len(fileList))
				for i, file := range fileList {
					names[i] = file.Name()
				}

				if err != nil {
					panic(err)
				}

				contents := make([][]byte, len(fileList))

				for j, file := range fileList {
					if !strings.HasSuffix(file.Name(), ".lth") {
						continue
					}

					contents[j], err = os.ReadFile("./" + str + "/" + file.Name())

					if err != nil {
						panic(err)
					}
				}

				if _, found := activeEnv.ImportContext[activeEnv.NameStack[0]]; !found {
					activeEnv.ImportContext[activeEnv.NameStack[0]] = map[string]tval{}
				}

				activeEnv.ImportContext[activeEnv.NameStack[0]][pkgName] = TvPackage(contents, names)
			}

			return TvNil
		}),

		"fun": global(func(argv []ttnode) tval {
			if len(argv) == 2 {
				argBranch := Derepr(argv[0]).(ttbranch)
				argList := make([]string, len(argBranch))

				for i, node := range argBranch {
					leaf, _ := node.(ttleaf)
					argList[i] = string(leaf)
				}

				return TvFun(BindFun(Derepr(argv[1]), argList, activeEnv))
			}

			if len(argv) != 3 {
				fmt.Println("(ERR) invalid argument count '" + fmt.Sprint(len(argv)) + "' passed @ 'builtins.fun'.")
				return TvNil
			}

			funName := argv[0].Evaluate().Val.(string)

			argBranch := Derepr(argv[1]).(ttbranch)
			argList := make([]string, len(argBranch))

			for i, node := range argBranch {
				if branch, ok := node.(ttbranch); ok {
					if i == len(argBranch)-1 &&
						len(branch) == 2 &&
						branch[0] == ttleaf("spread") {

						leaf := branch[1].(ttleaf)
						argList[i] = fmt.Sprint("#", leaf)
						break
					}

					fmt.Println("(ERR) cannot parse argument pattern '" + fmt.Sprint(argBranch[i]) + "' passed @ 'builtins.fun'.")
					return TvNil
				}

				leaf, _ := node.(ttleaf)
				argList[i] = string(leaf)
			}

			Declare(funName, TvFun(BindFun(Derepr(argv[2]), argList, activeEnv)), true)

			return TvNil
		}),

		"method": global(func(argv []ttnode) tval {
			// (method myStruct 'name '(self m)
			//      '(+ self.n m)
			// )

			var (
				baseStructPtr = argv[0].Evaluate().Val.(tconstructor).StructData

				methodName = argv[1].Evaluate().Val.(string)

				argBranch = Derepr(argv[2]).(ttbranch)
				argList   = make([]string, len(argBranch))
			)

			for i, node := range argBranch {
				if branch, ok := node.(ttbranch); ok {
					if i == len(argBranch)-1 &&
						len(branch) == 2 &&
						branch[0] == ttleaf("spread") {

						leaf := branch[1].(ttleaf)
						argList[i] = fmt.Sprint("#", leaf)
						break
					}

					fmt.Println("(ERR) cannot parse argument pattern '" + fmt.Sprint(argBranch[i]) + "' passed @ 'builtins.method'.")
					return TvNil
				}

				leaf := node.(ttleaf)
				argList[i] = string(leaf)
			}

			method := BindMethod(Derepr(argv[3]), argList, activeEnv)

			baseStructPtr.Methods[methodName] = method

			Declare(methodName, TvMethod(baseStructPtr, method), true)

			return TvNil
		}),

		"block": global(func(argv []ttnode) tval {
			// (block '(+ 1 2))
			return TvFun(BindCallback(Derepr(argv[0])))
		}),

		"~": global(func(argv []ttnode) tval {
			return TvBool(!argv[0].Evaluate().Val.(bool))
		}),

		"and": global(func(argv []ttnode) tval {
			return TvBool(argv[0].Evaluate().Val.(bool) && argv[1].Evaluate().Val.(bool))
		}),

		"or": global(func(argv []ttnode) tval {
			return TvBool(argv[0].Evaluate().Val.(bool) || argv[1].Evaluate().Val.(bool))
		}),

		"has": global(func(argv []ttnode) tval {
			col := argv[0].Evaluate()
			switch col.Kind {
			case KindDict:
				dict := col.Val.(map[tval]tval)
				key := argv[1].Evaluate()

				_, found := dict[key]
				if found {
					return TvBool(true)
				}
			case KindArray:
				array := *col.Val.(*[]tval)
				idx := int(argv[1].Evaluate().Val.(float64))

				if idx < len(array) && idx > 0 {
					return TvBool(true)
				}
			case KindStr:
				str := col.Val.(string)
				idx := int(argv[1].Evaluate().Val.(float64))

				if idx < len(str) && idx > 0 {
					return TvBool(true)
				}

			}
			return TvBool(false)
		}),

		Dot: global(func(argv []ttnode) tval {
			col := argv[0].Evaluate()

			switch col.Kind {
			case KindDict:
				dict := *col.Val.(*map[tval]tval)
				key := argv[1].Evaluate()

				val, found := dict[key]
				if found {
					return val
				}

				return TvNil
			case KindArray:
				array := *col.Val.(*[]tval)

				if br, ok := argv[1].(ttbranch); ok && br[0] == ttleaf("slice") {
					var (
						lhs int
						rhs int
					)

					// myList.[: b]
					if br[1] == ttleaf(":") {
						lhs = 0
						rhs = int(br[2].Evaluate().Val.(float64))
						// myList.[a : b]
					} else if len(br) == 4 && br[2] == ttleaf(":") {
						lhs = int(br[1].Evaluate().Val.(float64))
						rhs = int(br[3].Evaluate().Val.(float64))
						// myList.[a :]
					} else if len(br) == 3 && br[2] == ttleaf(":") {
						lhs = int(br[1].Evaluate().Val.(float64))
						rhs = len(array)
					} else if len(br) == 2 && br[1] == ttleaf(":") {
						lhs = 0
						rhs = len(array)
					} else {
						fmt.Println("(ERR): Invalid slicing syntax passed @ 'builtins.internal.dot'.")
						return TvNil
					}

					return TvSlice(array[lhs:rhs])
				}

				idx := int(argv[1].Evaluate().Val.(float64))

				if idx < len(array) && idx > 0 {
					return array[idx]
				}

				return TvNil
			case KindStr:
				str := col.Val.(string)
				idx := int(argv[1].Evaluate().Val.(float64))

				if idx < len(str) && idx > 0 {
					return TvChr(rune(str[idx]))
				}

				return TvNil
			case KindInstance:
				inst := col.Val.(*tinstance)

				if lf, ok := argv[1].(ttleaf); ok {
					ident := string(lf)

					if !regexp.MustCompile("^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))$").MatchString(ident) {
						fmt.Println("(ERR): Cannot index struct instance with non-identifier key '" + ident + "' @ 'builtins.internal.dot'.")
						return TvNil
					}

					if val, found := inst.Fields[ident]; found {
						if unicode.IsLower(rune(ident[0])) && !inst.Privileged {
							fmt.Println("(ERR): Cannot index struct instance with private key '" + ident + "' @ 'builtins.internal.dot'.")
							return TvNil
						}

						return val
					}

					prevEnv := activeEnv
					activeEnv = inst.Struct.Origin

					result := TvNil
					if val, found := Resolve(ident); found {
						if val.Kind != KindMethod {
							fmt.Println("(ERR): Could not find method or field '" + ident + "' on struct instance @ 'builtins.internal.dot'.")
							return TvNil
						} else {
							method := val.Val.(tmethod)

							if unicode.IsLower(rune(ident[0])) && !inst.Privileged {
								fmt.Println("(ERR): Cannot index struct instance with private key '" + ident + "' @ 'builtins.internal.dot'.")
								return TvNil
							}

							result = TvFun(func(argv []ttnode) tval {
								activeEnv.InstStack = append([]tval{col}, activeEnv.InstStack...)
								innerResult := method.Fun(argv)
								// InstStack is popped by method.Fun's BindMethod wrapper
								return innerResult
							})
						}
					} else {
						fmt.Println("(ERR): Could not resolve method or field '" + ident + "' on struct instance @ 'builtins.internal.dot'.")
						return TvNil
					}

					inst.Struct.Origin = activeEnv
					activeEnv = prevEnv

					return result
				}

				fmt.Println("(ERR): Cannot dynamically index struct instance with expression '" + Inspect(argv[1]) + "' @ 'builtins.internal.dot'.")
				return TvNil

			case KindPackage:
				// (math.Add 1 2) -> ((read math Add) 1 2)
				pkg := col.Val.(tpackage)

				if _, ok := argv[1].(ttbranch); ok {
					fmt.Println("(ERR): Package accessors must be unqualified identifiers: expected identifier, got call '" + Inspect(argv[0]) + "' @ 'builtins.internals.dot'.")
					return TvNil
				}

				if val, found := pkg.Exports[string(argv[1].(ttleaf))]; found {
					if val.Kind == KindFun {
						return TvFun(val.Val.(tfun))
					}

					if val.Kind == KindConstructor {
						return TvFun(val.Val.(tconstructor).Constructor)
					}

					fmt.Println("Should be impossible? Non-function export found @ 'builtins.internal.dot'.")
					return TvNil
				}

				return TvNil
			case KindGoPackage:
				// (math.Add 1 2) -> ((read math Add) 1 2)
				gopkg := col.Val.(*LithpModule)

				if _, ok := argv[1].(ttbranch); ok {
					fmt.Println("(ERR): Go package accessors must be unqualified identifiers: expected identifier, got call '" + Inspect(argv[0]) + "' @ 'builtins.internals.dot'.")
					return TvNil
				}

				funcName := string(argv[1].(ttleaf))

				return TvFun(func(callArgv []ttnode) tval {
					args := DistributeSpreadExpressions(callArgv)

					return TvUnknown(Call(gopkg, funcName, args))
				})
			case KindNum:
				rhs := argv[1].Evaluate()

				if rhs.Kind != KindNum {
					panic("uh oh. failed to transform a decimal @ 'builtins.internals.dot'")
				}

				var (
					n       = rhs.Val.(float64)
					digits  = len(fmt.Sprint(n))
					decimal = n / math.Pow(10, float64(digits))
				)

				return TvNum(col.Val.(float64) + decimal)
			}

			fmt.Println("(ERR): Cannot index a value of type '" + col.Kind + "' @ 'builtins.internal.dot'.")
			return TvNil
		}),

		"+": global(func(argv []ttnode) tval {
			sum := argv[0].Evaluate()
			targetKind := sum.Kind

			for _, arg := range argv[1:] {
				switch targetKind {
				case KindNum:
					sum.Val = sum.Val.(float64) + arg.Evaluate().Val.(float64)
				case KindStr:
					sum.Val = sum.Val.(string) + arg.Evaluate().Val.(string)
				}
			}

			return sum
		}),

		"-": global(func(argv []ttnode) tval {
			return TvNum(argv[0].Evaluate().Val.(float64) - argv[1].Evaluate().Val.(float64))
		}),

		"*": global(func(argv []ttnode) tval {
			return TvNum(argv[0].Evaluate().Val.(float64) * argv[1].Evaluate().Val.(float64))
		}),

		"/": global(func(argv []ttnode) tval {
			return TvNum(argv[0].Evaluate().Val.(float64) / argv[1].Evaluate().Val.(float64))
		}),

		"~=": global(func(argv []ttnode) tval {
			cond := argv[0].Evaluate().Val != argv[1].Evaluate().Val
			return TvBool(cond)
		}),

		"==": global(func(argv []ttnode) tval {
			cond := argv[0].Evaluate().Val == argv[1].Evaluate().Val
			return TvBool(cond)
		}),

		"<=": global(func(argv []ttnode) tval {
			cond := argv[0].Evaluate().Val.(float64) <= argv[1].Evaluate().Val.(float64)
			return TvBool(cond)
		}),

		">=": global(func(argv []ttnode) tval {
			cond := argv[0].Evaluate().Val.(float64) >= argv[1].Evaluate().Val.(float64)
			return TvBool(cond)
		}),

		"<": global(func(argv []ttnode) tval {
			cond := argv[0].Evaluate().Val.(float64) < argv[1].Evaluate().Val.(float64)
			return TvBool(cond)
		}),

		">": global(func(argv []ttnode) tval {
			cond := argv[0].Evaluate().Val.(float64) > argv[1].Evaluate().Val.(float64)
			return TvBool(cond)
		}),

		"if": global(func(argv []ttnode) tval {
			result := TvNil

			cond := argv[0].Evaluate().Val.(bool)
			if cond {
				result = BindCallback(Derepr(argv[1].(ttbranch)[1]))([]ttnode{})
			} else if len(argv) == 3 {
				result = BindCallback(Derepr(argv[2].(ttbranch)[1]))([]ttnode{})
			}

			return result
		}),

		"while": global(func(argv []ttnode) tval {
			lastVal := TvNil

			cond := BindFun(argv[1].(ttbranch)[2], []string{}, activeEnv)
			bodyFunc := BindFun(argv[2].(ttbranch)[2], []string{}, activeEnv)

			for cond([]ttnode{}).Val.(bool) {
				lastVal = bodyFunc([]ttnode{})
			}

			return lastVal
		}),

		"do": global(func(argv []ttnode) tval {
			value := TvNil

			for _, node := range argv {
				node.Evaluate()
			}

			return value
		}),

		// only two valid syntax types:
		//   (= 'ident newVal)             -->   (= "ident" newVal)
		//   (= collection.index newVal)   -->   (= (Dot collection index) newVal)
		"=": global(func(argv []ttnode) tval {
			if _, ok := argv[0].(ttleaf); ok {
				var (
					ident  = string(Derepr(argv[0]).(ttleaf))
					newVal = argv[1].Evaluate()
				)

				Set(ident, newVal)
				return TvNil
			}

			br := argv[0].(ttbranch)

			if len(br) != 3 {
				fmt.Println("(ERR) cannot parse assignment target '" + Inspect(br) + "' of length '" + strconv.Itoa(len(br)) + "'. Expected length '3' and dot syntax @ 'builtins.`=`'.")
				return TvNil
			}

			var (
				collection = br[1].Evaluate()
				index      = br[2].Evaluate()
			)

			if collection.Kind == KindInstance {
				instance := collection.Val.(*tinstance)
				instance.Fields[index.Val.(string)] = argv[1].Evaluate()
				return TvNil
			}

			fmt.Println("(ERR) cannot set fields of non-struct instance type '" + collection.Kind + "' passed @ 'builtins.`=`'.")
			return TvNil
		}),

		"var": global(func(argv []ttnode) tval {
			type constRequest struct {
				Identifier string
				Repr       ttnode
			}

			if len(argv)%2 != 0 {
				fmt.Println("(ERR) cannot parse argument list of length '" + fmt.Sprint(len(argv)) + "' passed. Must be a multiple of 2 @ 'builtins.var'.")
				return TvNil
			}

			var requests []constRequest

			for i := 0; i < len(argv)-1; i += 2 {
				requests = append(requests, constRequest{string(Derepr(argv[i]).(ttleaf)), argv[i+1]})
			}

			for _, request := range requests {
				if _, found := activeEnv.Stack[0][request.Identifier]; found {
					fmt.Println("(ERR) cannot redeclare value '" + request.Identifier + "' passed @ 'builtins.var'.")
					return TvNil
				}

				Declare(request.Identifier, request.Repr.Evaluate(), false)
			}

			return TvNil
		}),

		"const": global(func(argv []ttnode) tval {
			type constRequest struct {
				Identifier string
				Repr       ttnode
			}

			if len(argv)%2 != 0 {
				fmt.Println("(ERR) cannot parse argument list of length '" + fmt.Sprint(len(argv)) + "' passed. Must be a multiple of 2 @ 'builtins.const'.")
				return TvNil
			}

			var requests []constRequest

			for i := 0; i < len(argv)-1; i += 2 {
				requests = append(requests, constRequest{string(Derepr(argv[i]).(ttleaf)), argv[i+1]})
			}

			for _, request := range requests {
				if _, found := activeEnv.Stack[0][request.Identifier]; found {
					fmt.Println("(ERR) cannot redeclare value '" + request.Identifier + "' passed @ 'builtins.const'.")
					return TvNil
				}

				Declare(request.Identifier, request.Repr.Evaluate(), true)
			}

			return TvNil
		}),

		"drop": global(func(argv []ttnode) tval {
			array := argv[0].Evaluate()

			return TvSlice((*array.Val.(*[]tval))[int(argv[1].Evaluate().Val.(float64)):])
		}),

		"append": global(func(argv []ttnode) tval {
			var (
				arrayVal = argv[0].Evaluate()
				array    = *arrayVal.Val.(*[]tval)
			)

			for _, arg := range argv[1:] {
				array = append(array, arg.Evaluate())
			}

			return TvSlice(array)
		}),

		"len": global(func(argv []ttnode) tval {
			length := len(*argv[0].Evaluate().Val.(*[]tval))
			return TvNum(float64(length))
		}),

		"slice": global(func(argv []ttnode) tval {
			var result []tval

			for _, entry := range argv {
				result = append(result, entry.Evaluate())
			}

			return TvSlice(result)
		}),

		"map": global(func(argv []ttnode) tval {
			result := map[tval]tval{}

			for i := 0; i < len(argv); i += 2 {
				key := argv[i].Evaluate()
				val := argv[i+1].Evaluate()

				result[key] = val
			}

			return TvDict(result)
		}),

		// (struct 'myStruct '(PublicField privateField))
		// (var 'myInst (myStruct { 'PublicField 1
		//							'privateField 2 })
		"struct": global(func(argv []ttnode) tval {
			args, ok := ParseArgs("builtins.struct", []string{KindStr, KindArray}, argv)
			if !ok {
				return TvNil
			}

			structName := args[0].(string)

			fieldsNode := *args[1].(*[]tval)
			fields := make([]string, len(fieldsNode))

			for i, fieldNode := range fieldsNode {
				fields[i] = fieldNode.Val.(string)
			}

			env := activeEnv

			structData := tstruct{
				Fields:  fields,
				Methods: map[string]tfun{},
				Origin:  env,
			}

			newFun := tfun(func(argv []ttnode) tval {
				fieldMap := make(map[string]tval)

				if len(argv) > 0 {
					arg := argv[0].Evaluate().Val.(map[tval]tval)

					for key, val := range arg {
						keyStr, success := key.Val.(string)
						if !success {
							fmt.Println("(ERR): Passed invalid type '" + key.Kind + "' as key to struct initializer map @ 'builtins.struct.new'.")
							return TvNil
						}

						if !slices.Contains(fields, keyStr) {
							fmt.Println("(ERR): Passed invalid key '" + keyStr + "' to struct initializer map @ 'builtins.struct.new'.")
							return TvNil
						}

						fieldMap[keyStr] = val
					}

					for _, fieldName := range fields {
						if _, found := fieldMap[fieldName]; !found {
							fmt.Println("(WARN): Failed to pass key '" + fieldName + "' to struct initializer map @ 'builtins.struct.new'.")
							fieldMap[fieldName] = TvNil
						}
					}
				}

				return TvInstance(&structData, fieldMap, false, false)
			})

			// it's a hacky solution at best, but a solution nonetheless
			activeEnv.Structs[structName] = &structData

			Declare(structName, TvConstructor(structName, &structData, newFun), true)

			return TvNil
		}),

		"resume": global(func(argv []ttnode) tval {
			val := argv[0].Evaluate()
			tree := TreeifyVal(val)
			node := Derepr(tree)
			block := BindCallback(node)
			return block([]ttnode{})
		}),

		"pause": global(func(argv []ttnode) tval {
			return ListifyVal(argv[0].Evaluate())
		}),

		"inspect": global(func(argv []ttnode) tval {
			return TvStr(Inspect(argv[0]))
		}),
	}}

	return tenv{
		Globals:       &stack[0],
		Stack:         stack,
		CtxStack:      []tcontext{},
		Structs:       map[string]*tstruct{},
		InstStack:     []tval{},
		NameStack:     []string{},
		ImportContext: map[string]map[string]tval{},
	}
}
