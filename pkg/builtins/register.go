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
		collBuiltins(),
		ctrlBuiltins(),
		declBuiltins(),
		dotBuiltins(),
		metaBuiltins(),
		modimportBuiltins(),
		strinterpBuiltins(),
	} {
		for k, v := range m {
			builtins[k] = v
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
