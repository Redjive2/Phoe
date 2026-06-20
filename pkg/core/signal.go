package core

// Non-local control-flow signals. The evaluator's only normal channel is
// the Tval returned by Evaluate, so `return`, `break`, and `continue`
// have to escape through panic + recover. Recovery sites are narrow:
//
//   - BindFun / BindMethod catch ReturnSignal and return its value.
//   - The `for` builtin catches BreakSignal (exit loop) and
//     ContinueSignal (skip to next iter).
//   - modload installs a top-level recover so a stray signal at script
//     top-level becomes a diagnostic rather than crashing the host.
//
// Each recover site type-asserts for exactly the signals it owns; any
// other panic re-panics so real bugs aren't silently swallowed.

// ReturnSignal carries the value passed to `(return v)`. `(return)`
// with no argument carries TvNil.
type ReturnSignal struct{ Value Tval }

// BreakSignal exits the nearest enclosing `for` loop.
type BreakSignal struct{}

// ContinueSignal jumps to the next iteration of the nearest enclosing
// `for` loop.
type ContinueSignal struct{}

// RecursionSignal is raised by BindFun / BindMethod when the Pho call
// depth exceeds maxCallDepth. The evaluator recurses on the Go stack, so
// runaway recursion would otherwise hit Go's fatal (unrecoverable) stack
// overflow; this guard unwinds it to modload.evalTopLevel, which turns
// it into an ordinary recursion-limit diagnostic. Like the other
// signals, it rides through RecoverReturn / the `for` recover (which
// re-panic anything they don't own) untouched.
type RecursionSignal struct{}

// RecoverReturn inspects a recover()'d value. If it's a ReturnSignal
// it returns the carried value and true; if it's any other non-nil
// value it re-panics (so real bugs surface); if it's nil it returns
// TvNil and false.
//
// Usage:
//
//	defer func() {
//	    if v, ok := core.RecoverReturn(recover()); ok {
//	        result = v
//	    }
//	}()
func RecoverReturn(r any) (Tval, bool) {
	if r == nil {
		return TvNil, false
	}
	if rs, ok := r.(ReturnSignal); ok {
		return rs.Value, true
	}
	panic(r)
}
