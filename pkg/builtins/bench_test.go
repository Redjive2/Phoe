package builtins

import (
	"testing"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// BenchmarkFib measures tree-walking eval throughput on a recursion-
// and call-heavy workload — the worst case for the span-wrapper
// overhead added in the diagnostics work (one extra dynamic dispatch +
// a Context span write per form).
//
// To measure the wrapper cost, compare:
//
//	go test -bench Fib -count 5 ./pkg/builtins
//	PHO_NO_SPANS=1 go test -bench Fib -count 5 ./pkg/builtins
//
// (PHO_NO_SPANS is read at package init, so the modes need separate
// processes.)
func BenchmarkFib(b *testing.B) {
	const src = `
(fun fib (n) (if (< n 2)
    then n
    else (+ (fib (- n 1)) (fib (- n 2)))))
(fib 15)
`
	env := NewEnv()
	file := &core.File{Mode: core.ModeProgram, Imports: map[string]core.Value{}}
	ctx := core.Context{Env: &env, File: file}
	ctx.PushFrame()

	tokens, _ := syntax.LexPos(src)
	tree, _ := syntax.ParsePos(tokens)
	forms := syntax.Lower(tree).(core.Branch)

	// Declare fib once; benchmark only the call.
	forms[0].Evaluate(ctx)
	call := forms[1]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if v := call.Evaluate(ctx); v.Kind != core.KindNum || v.Val.(float64) != 610 {
			b.Fatalf("fib(15) = %v (%s), want 610", v.Val, v.Kind)
		}
	}
}
