package main

import (
	"fmt"
	"regexp"
)

func BindFun(repr ttnode, argList []string, env *tenv) tfun {
	var restArg string

	if len(argList) > 0 {
		finalArg := argList[len(argList)-1]

		if len(finalArg) > 0 && finalArg[0] == '#' {
			argList = argList[:len(argList)-1]
			restArg = finalArg[1:]
		}
	}

	return func(argv []ttnode) tval {
		args := DistributeSpreadExpressions(argv)
		argFrame := make(map[string]tval)

		var i int
		for i = range argList {
			argName := argList[i]
			argFrame[argName] = args[i]
		}

		if restArg != "" {
			argFrame[restArg] = TvSlice(args[i:])
		}

		prevEnv := activeEnv
		activeEnv = env

		PushFrame()
		PushContext(repr)

		for argName, argValue := range argFrame {
			activeEnv.Stack[0][argName] = tStackEntry{argValue, true}
		}

		result := repr.Evaluate()

		PopFrame()
		PopContext()

		env = activeEnv
		activeEnv = prevEnv

		return result
	}
}

func BindMethod(repr ttnode, argList []string, env *tenv) tfun {
	var restArg string

	if len(argList) > 0 {
		finalArg := argList[len(argList)-1]

		if len(finalArg) > 0 && finalArg[0] == '#' {
			argList = argList[:len(argList)-1]
			restArg = finalArg[1:]
		}
	}

	return func(argv []ttnode) tval {
		args := DistributeSpreadExpressions(argv)
		argFrame := make(map[string]tval)

		argFrame[argList[0]] = activeEnv.InstStack[0]

		var i int
		for i = 0; i < len(argList)-2; i++ {
			argName := argList[i+1]
			argFrame[argName] = args[i]
		}

		if restArg != "" {
			argFrame[restArg] = TvSlice(args[i:])
		}

		prevEnv := activeEnv
		activeEnv = env

		PushFrame()
		PushContext(repr)

		for argName, argValue := range argFrame {
			activeEnv.Stack[0][argName] = tStackEntry{argValue, true}
		}

		inst := activeEnv.Stack[0][argList[0]].Val
		inst.Val.(*tinstance).Privileged = true

		result := repr.Evaluate()

		inst.Val.(*tinstance).Privileged = false

		PopFrame()
		PopContext()

		env = activeEnv
		activeEnv = prevEnv

		return result
	}
}

func BindCallback(repr ttnode) tfun {
	return func(argv []ttnode) tval {
		PushContext(repr)
		result := repr.Evaluate()
		PopContext()

		return result
	}
}

func Declare(targetIdent string, targetValue tval, isConst bool) bool {
	if len(activeEnv.CtxStack) > 0 {
		for i := range activeEnv.CtxStack[0].MaxStackDepth {
			if _, found := activeEnv.Stack[i][targetIdent]; found {
				return false
			}
		}
	} else {
		if _, found := activeEnv.Stack[0][targetIdent]; found {
			return false
		}
	}

	for _, ctx := range activeEnv.CtxStack {
		if _, found := ctx.Captures[targetIdent]; found {
			return false
		}
	}

	if _, found := (*activeEnv.Globals)[targetIdent]; found {
		return false
	}

	activeEnv.Stack[0][targetIdent] = tStackEntry{targetValue, isConst}
	return true
}

// Resolve resolves an identifier to a value in the current scope
func Resolve(targetIdent string) (tval, bool) {
	if len(activeEnv.CtxStack) > 0 {
		for i := range activeEnv.CtxStack[0].MaxStackDepth {
			if val, found := activeEnv.Stack[i][targetIdent]; found {
				return val.Val, true
			}
		}
	} else {
		if val, found := activeEnv.Stack[0][targetIdent]; found {
			return val.Val, true
		}
	}

	if val, found := (*activeEnv.Globals)[targetIdent]; found {
		return val.Val, true
	}

	if mod, found := activeEnv.ImportContext[activeEnv.NameStack[0]][targetIdent]; found {
		return mod, true
	}

	return TvNil, false
}

func Set(targetIdent string, newVal tval) bool {
	if len(activeEnv.CtxStack) > 0 {
		for i := range activeEnv.CtxStack[0].MaxStackDepth {
			if val, found := activeEnv.Stack[i][targetIdent]; found {
				if val.IsConstant {
					fmt.Println("(ERR) cannot set constant value '" + targetIdent + "' passed @ 'internal/runtime.go:Set'")
				}

				activeEnv.Stack[i][targetIdent] = tStackEntry{newVal, false}
				return true
			}
		}
	} else {
		if val, found := activeEnv.Stack[0][targetIdent]; found {
			if val.IsConstant {
				fmt.Println("(ERR) cannot set constant value '" + targetIdent + "' passed @ 'internal/runtime.go:Set'")
			}

			activeEnv.Stack[0][targetIdent] = tStackEntry{newVal, false}
			return true
		}
	}

	for _, ctx := range activeEnv.CtxStack {
		if captureFunc, found := ctx.Captures[targetIdent]; found {
			captureFunc(newVal, true)
			return true
		}
	}

	if val, found := (*activeEnv.Globals)[targetIdent]; found {
		if val.IsConstant {
			fmt.Println("(ERR) cannot set constant value '" + targetIdent + "' passed @ 'internal/runtime.go:Set'")
		}

		(*activeEnv.Globals)[targetIdent] = tStackEntry{newVal, false}
		return true
	}

	return false
}

func findIdentifiers(code ttnode) []string {
	if lf, ok := code.(ttleaf); ok {
		str := string(lf)

		if regexp.MustCompile("^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9\\-]*[a-zA-Z0-9]))$").MatchString(str) {
			return []string{str}
		}

		return []string{}
	}

	var (
		result []string
		branch = code.(ttbranch)
	)

	for _, node := range branch {
		result = append(result, findIdentifiers(node)...)
	}

	return result
}

func capture(targetIdent string) func(tval, bool) tval {
	return func(newVal tval, doEdit bool) tval {
		if doEdit {
			Set(targetIdent, newVal)
		}

		result, _ := Resolve(targetIdent)
		return result
	}
}

func PushContext(code ttnode) {
	result := tcontext{
		Captures:      map[string]func(tval, bool) tval{},
		MaxStackDepth: len(activeEnv.Stack),
	}

	identList := findIdentifiers(code)

	for _, ident := range identList {
		if _, found := Resolve(ident); found {
			result.Captures[ident] = capture(ident)
		}
	}

	activeEnv.CtxStack = append([]tcontext{result}, activeEnv.CtxStack...)
}

func PopContext() {
	activeEnv.CtxStack = activeEnv.CtxStack[1:]
}

func PushFrame() {
	activeEnv.Stack = append([]map[string]tStackEntry{{}}, activeEnv.Stack...)
}

func PopFrame() {
	activeEnv.Stack = activeEnv.Stack[1:]
}

func PushName(name string) {
	activeEnv.NameStack = append([]string{name}, activeEnv.NameStack...)
}

func PopName() {
	activeEnv.NameStack = activeEnv.NameStack[1:]
}
