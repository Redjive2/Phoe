// Package builtins provides the built-in functions of the Pho language and
// the NewEnv constructor that assembles them into an environment.
package builtins

import (
	"pho/pkg/core"
	"pho/pkg/modload"
)

// init wires NewEnv into modload.EnvFactory so the package loader can
// construct envs with builtins without taking a direct dependency on this
// package (which would create a cycle: modload → builtins → modload).
func init() {
	modload.EnvFactory = NewEnv
}

// NewEnv constructs a fresh interpreter environment with all builtins
// installed. Each category file contributes a sub-map; we merge them here.
func NewEnv() core.Env {
	builtins := map[string]core.StackEntry{}

	for _, m := range []map[string]core.StackEntry{
		arithBuiltins(),
		atomBuiltins(),
		collBuiltins(),
		ctrlBuiltins(),
		declBuiltins(),
		lambdaBuiltins(),
		operatorBuiltins(),
		selectBuiltins(),
		dotBuiltins(),
		slashBuiltins(),
		metaBuiltins(),
		modimportBuiltins(),
		strinterpBuiltins(),
		typeBuiltins(),
	} {
		for k, v := range m {
			builtins[k] = v
		}
	}

	// Operator overloading (Features.md §7): wrap each primitive prefix operator
	// so a struct instance whose type overloads it dispatches to the overload.
	// The index operators [] / []= are handled in the index read/write paths.
	for _, op := range arithOverloadNames {
		if entry, ok := builtins[op]; ok {
			if fn, isFn := entry.Val.Val.(core.Fun); isFn {
				builtins[op] = global(withOverload(op, fn))
			}
		}
	}

	stack := []map[string]core.StackEntry{builtins}

	return core.Env{
		Globals:   &stack[0],
		Stack:     stack,
		CtxStack:  []core.ScopeCtx{},
		Structs:   map[string]*core.Struct{},
		InstStack: []core.Value{},
	}
}
