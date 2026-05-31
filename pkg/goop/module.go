// Package goop implements the Go-interop layer: registration of Go modules
// that Pho can call into via `goimport`, and the reflective dispatch for
// invoking their methods from Pho code.
package goop

import (
	"fmt"
	"reflect"
	"unicode"

	"pho/pkg/core"
)

// PhoModule represents a Go-side module exposed to Pho code. The Data
// field holds a Go struct whose exported (capitalized) methods become
// callable from Pho via the reflective Call below.
type PhoModule struct {
	Name     string
	Children map[string]*PhoModule
	Data     any
}

// GoModules is the global registry of exposed Go modules, keyed by Name.
// Populated by Expose and read by the goimport builtin.
var GoModules = make(map[string]*PhoModule)

// Expose registers a top-level Go module. Returns false if a module with
// the same Name has already been registered.
func Expose(modulePtr *PhoModule) bool {
	if _, found := GoModules[modulePtr.Name]; found {
		return false
	}

	GoModules[modulePtr.Name] = modulePtr

	return true
}

// Call reflectively invokes a capitalized method on the module's Data. A
// single return value is unwrapped; multiple returns become a []any.
func Call(origin *PhoModule, funcName string, args []core.Value) any {
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
