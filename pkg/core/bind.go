package core

import "fmt"

// BindFun produces a tfun closure over the function's body, argList, and
// the Context it was defined in. The defining file/package are captured so
// file-scoped imports resolve correctly when the function is called from a
// different file or package, and the definition-site scope chain is
// captured (LexicalFrames) so the body sees its lexical scope — never the
// caller's locals.
//
// name labels the call in stack traces; "" becomes "<fun>" for anonymous
// lambdas.
func BindFun(name string, repr ttnode, argList []string, defCtx Context) tfun {
	var restArg string

	if len(argList) > 0 {
		finalArg := argList[len(argList)-1]

		if len(finalArg) > 0 && finalArg[0] == '#' {
			argList = argList[:len(argList)-1]
			restArg = finalArg[1:]
		}
	}

	defFrames := defCtx.LexicalFrames()
	if name == "" {
		name = "<fun>"
	}

	return func(callCtx Context, argv []ttnode) (result Tval) {
		// Outermost defer: catch (return v) panics raised anywhere in the
		// body and surface v as this call's value. Registered first so it
		// runs last — all other body defers (frame pop, context pop) have
		// already fired by the time we land here. A (return) is a normal
		// exit, so it pops the call frame here; a foreign panic flows
		// through RecoverReturn (which re-panics) with the frame left in
		// place, so the top-level recover can still snapshot a trace.
		defer func() {
			if v, ok := RecoverReturn(recover()); ok {
				callCtx.PopCallFrame()
				result = v
			}
		}()

		args, ok := DistributeSpreadExpressions(callCtx, argv)
		if !ok {
			return TvNil
		}
		argFrame := make(map[string]Tval)

		required, names := splitOptional(argList)
		if len(args) < required {
			return callCtx.Errorf(ErrArity, "function called with too few arguments: %s, got %d", arityExpectation(required, names, restArg), len(args))
		}

		for i, name := range names {
			if i < len(args) {
				argFrame[name] = args[i]
			} else {
				argFrame[name] = TvNil
			}
		}

		if restArg != "" {
			rest := []Tval{}
			if len(args) > len(names) {
				rest = args[len(names):]
			}
			argFrame[restArg] = TvSlice(rest)
		}

		// The body runs under the captured ctx (so file-scoped imports +
		// package state come from the function's source), but with a fresh
		// frame for the arguments and a function context that hides the
		// caller's frames. InFunction is flipped on so top-level-only
		// restrictions (like the var check) can tell they're now inside a
		// body.
		bodyCtx := defCtx
		bodyCtx.InFunction = true
		bodyCtx.PushFrame()
		defer bodyCtx.PopFrame()
		bodyCtx.PushFunContext(defFrames)
		defer bodyCtx.PopContext()

		for argName, argValue := range argFrame {
			bodyCtx.Env.Stack[0][argName] = tStackEntry{argValue, false}
		}

		// Recursion guard: the body evaluates on the Go stack, so without
		// this unbounded recursion crashes the host with Go's fatal stack
		// overflow. Checked before pushing this call's frame, so the limit
		// is the depth of live calls; unwinds to evalTopLevel as a clean
		// recursion-limit diagnostic.
		if callCtx.Diag.Depth() >= maxCallDepth {
			panic(RecursionSignal{})
		}

		// The call frame records the call SITE (callCtx). Pushed only once
		// we commit to running the body — a failed-arity call is blamed on
		// the caller, not a phantom callee frame — and popped explicitly on
		// normal return so a foreign panic leaves it for the trace snapshot.
		callCtx.PushCallFrame(name)
		if debugBind {
			fmt.Printf("DBG call %q: stackLen=%d hidden? defFrames=%d visible=%d\n", name, len(bodyCtx.Env.Stack), len(defFrames), len(bodyCtx.visibleStack()))
			for i, fr := range defFrames {
				keys := []string{}
				for k := range fr {
					keys = append(keys, k)
				}
				fmt.Printf("  defFrame[%d] keys=%v\n", i, keys)
			}
		}
		result = repr.Evaluate(bodyCtx)
		callCtx.PopCallFrame()
		return result
	}
}

var debugBind = false

// BindMethod is BindFun with self-injection: argList[0] is bound from the
// instance pushed onto callCtx.Env.InstStack by the Dot accessor's wrapper.
//
// name labels the call in stack traces (e.g. "Point.Shift").
func BindMethod(name string, repr ttnode, argList []string, defCtx Context) tfun {
	var restArg string

	if len(argList) > 0 {
		finalArg := argList[len(argList)-1]

		if len(finalArg) > 0 && finalArg[0] == '#' {
			argList = argList[:len(argList)-1]
			restArg = finalArg[1:]
		}
	}

	defFrames := defCtx.LexicalFrames()
	if name == "" {
		name = "<method>"
	}

	return func(callCtx Context, argv []ttnode) (result Tval) {
		// Outermost defer: same job as BindFun's. Crucially, this runs
		// AFTER the Privileged-reset defer below, so a (return) from
		// inside a method body still clears the receiver's Privileged
		// flag before unwinding. A (return) pops the call frame here; a
		// foreign panic leaves it for the top-level trace snapshot.
		defer func() {
			if v, ok := RecoverReturn(recover()); ok {
				callCtx.PopCallFrame()
				result = v
			}
		}()

		if len(argList) == 0 {
			return callCtx.Errorf(ErrNoReceiver, "method has no receiver parameter")
		}

		if len(callCtx.Env.InstStack) == 0 {
			return callCtx.Errorf(ErrNoReceiver, "method called without a receiver instance")
		}

		args, ok := DistributeSpreadExpressions(callCtx, argv)
		if !ok {
			return TvNil
		}
		argFrame := make(map[string]Tval)

		argFrame[argList[0]] = callCtx.Env.InstStack[0]

		// argList[0] is `self`, bound from InstStack above. argList[1:] are
		// the user-supplied parameters; rest-arg has already been stripped
		// from argList earlier. Optionals (parseArgList guarantees they
		// trail the required params) live entirely within argList[1:].
		required, names := splitOptional(argList[1:])
		if len(args) < required {
			return callCtx.Errorf(ErrArity, "method called with too few arguments: %s, got %d", arityExpectation(required, names, restArg), len(args))
		}

		for i, name := range names {
			if i < len(args) {
				argFrame[name] = args[i]
			} else {
				argFrame[name] = TvNil
			}
		}

		if restArg != "" {
			rest := []Tval{}
			if len(args) > len(names) {
				rest = args[len(names):]
			}
			argFrame[restArg] = TvSlice(rest)
		}

		bodyCtx := defCtx
		bodyCtx.InFunction = true
		bodyCtx.PushFrame()
		defer bodyCtx.PopFrame()
		bodyCtx.PushFunContext(defFrames)
		defer bodyCtx.PopContext()

		for argName, argValue := range argFrame {
			bodyCtx.Env.Stack[0][argName] = tStackEntry{argValue, false}
		}

		// Grant the receiver private access for the body. Save and restore
		// the prior value rather than hard-clearing it: the same *tinstance
		// is shared by pointer, so a nested or recursive method call on the
		// same receiver must not strip privilege from the still-running
		// outer call when the inner one returns.
		inst := bodyCtx.Env.Stack[0][argList[0]].Val.Val.(*tinstance)
		wasPrivileged := inst.Privileged
		inst.Privileged = true
		defer func() { inst.Privileged = wasPrivileged }()

		if callCtx.Diag.Depth() >= maxCallDepth {
			panic(RecursionSignal{})
		}

		callCtx.PushCallFrame(name)
		result = repr.Evaluate(bodyCtx)
		callCtx.PopCallFrame()
		return result
	}
}

// splitOptional partitions a rest-stripped argList into the minimum
// required argument count and the clean parameter names (with the '?'
// optional marker removed). Optionals are trailing — parseArgList
// rejects a required parameter after an optional one — so required is
// simply the index of the first '?'-prefixed entry.
func splitOptional(argList []string) (required int, names []string) {
	required = len(argList)
	names = make([]string, len(argList))
	for i, a := range argList {
		if len(a) > 0 && a[0] == '?' {
			if required == len(argList) {
				required = i
			}
			names[i] = a[1:]
		} else {
			names[i] = a
		}
	}
	return required, names
}

// arityExpectation renders the "expected …" clause of a too-few-args
// error: an exact count when every parameter is required, or a lower
// bound when optionals or a rest-arg make the upper count flexible.
func arityExpectation(required int, names []string, restArg string) string {
	if required < len(names) || restArg != "" {
		return fmt.Sprintf("expected at least %d", required)
	}
	return fmt.Sprintf("expected %d", required)
}

// BindCallback wraps an AST node as a parameterless tfun. Unlike BindFun,
// it does not capture a definition-site context: the body runs directly in
// the caller's ctx and scope. Used by the `if` and `for` builtins, whose
// blocks execute in the scope they were written in.
func BindCallback(repr ttnode) tfun {
	return func(ctx Context, argv []ttnode) Tval {
		return repr.Evaluate(ctx)
	}
}
