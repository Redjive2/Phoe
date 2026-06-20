// Package goop implements the Go-interop layer: registration of Go modules
// that Pho can call into via `goimport`, and the reflective dispatch for
// invoking their methods from Pho code.
package goop

import (
	"fmt"
	"os"
	"reflect"
	"unicode"

	"pho/pkg/core"
)

// hostErr reports a host-layer (Go-side) failure that has no Pho source
// position — a broken pipe, a failed spawn, a bad stream handle. These go
// straight to stderr in the `pho:` register main uses for loader errors:
// goop methods are invoked reflectively (and some run on background
// goroutines), so there's no Context or diagnostic session to thread a
// structured, positioned diagnostic through. Dispatch-level failures
// (goop.Call) DO get a full Pho diagnostic — they're returned as an error
// to the runtime caller, which has the source position and call stack.
func hostErr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "pho: "+format+"\n", args...)
}

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
// Arity/type mismatches and Go-side panics are returned as an error (with
// a nil result) instead of crashing the host; the caller in the Pho
// runtime turns it into a positioned diagnostic with a stack trace.
func Call(origin *PhoModule, funcName string, args []core.Value) (result any, err error) {
	if len(funcName) == 0 || !unicode.IsUpper(rune(funcName[0])) {
		return nil, fmt.Errorf("unable to find method '%s' on module '%s': it must be capitalized", funcName, origin.Name)
	}

	var (
		value  = reflect.ValueOf(origin.Data)
		method = value.MethodByName(funcName)
	)

	if !method.IsValid() {
		return nil, fmt.Errorf("module '%s' has no exposed method '%s'", origin.Name, funcName)
	}

	argValues, err := core.BuildCallArgs(method.Type(), args)
	if err != nil {
		return nil, fmt.Errorf("cannot call method '%s' on module '%s': %w", funcName, origin.Name, err)
	}

	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("go method '%s' on module '%s' panicked: %v", funcName, origin.Name, r)
		}
	}()

	callResult := method.Call(argValues)

	if len(callResult) == 0 {
		return nil, nil
	}

	if len(callResult) == 1 {
		return callResult[0].Interface(), nil
	}

	out := make([]any, len(callResult))
	for i, returnValue := range callResult {
		out[i] = returnValue.Interface()
	}

	return out, nil
}
