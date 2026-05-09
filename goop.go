package main

import (
	"fmt"
	"reflect"
	"unicode"
)

type LithpModule struct {
	Name     string
	Children map[string]*LithpModule
	Data     any
}

var GoModules = make(map[string]*LithpModule)

func Expose(modulePtr *LithpModule) bool {
	if _, found := GoModules[modulePtr.Name]; found {
		return false
	}

	GoModules[modulePtr.Name] = modulePtr

	return true
}

func Call(origin *LithpModule, funcName string, args []tval) any {
	if len(funcName) == 0 || !unicode.IsUpper(rune(funcName[0])) {
		fmt.Println("(ERR) unable to find method '" + funcName + "' on origin module '" + origin.Name + "': it must be capitalized @ 'goop.Call'.")
		return nil
	}

	var (
		value  = reflect.ValueOf(origin.Data)
		method = value.MethodByName(funcName)
	)

	if !method.IsValid() {
		fmt.Println("(ERR) failed to find method '" + funcName + "' on origin module '" + origin.Name + "' exposed @ 'goop.Call'.")
		return nil
	}

	argValues := make([]reflect.Value, len(args))

	for i, arg := range args {
		argValues[i] = reflect.ValueOf(arg.Val)
	}

	callResult := method.Call(argValues)

	if len(callResult) == 1 {
		return callResult[0].Interface()
	}

	result := make([]any, len(callResult))
	for i, returnValue := range callResult {
		result[i] = returnValue.Interface()
	}

	return result
}

func CallDirect(funcData reflect.Value, args []tval) any {
	argValues := make([]reflect.Value, len(args))

	for i, arg := range args {
		argValues[i] = reflect.ValueOf(arg.Val)
	}

	result := funcData.Call(argValues)

	return result[0].Interface()
}
