package core

import "fmt"

// BindFun produces a tfun closure over the function's body, argList, and
// the Context it was defined in. The defining file/package are captured so
// file-scoped imports resolve correctly when the function is called from a
// different file or package; only the call's Env-on-entry is taken from
// the caller (then re-pointed to the captured Env for the body).
func BindFun(repr ttnode, argList []string, defCtx Context) tfun {
	var restArg string

	if len(argList) > 0 {
		finalArg := argList[len(argList)-1]

		if len(finalArg) > 0 && finalArg[0] == '#' {
			argList = argList[:len(argList)-1]
			restArg = finalArg[1:]
		}
	}

	return func(callCtx Context, argv []ttnode) (result Tval) {
		// Outermost defer: catch (return v) panics raised anywhere in the
		// body and surface v as this call's value. Registered first so it
		// runs last — all other body defers (frame pop, context pop) have
		// already fired by the time we land here.
		defer func() {
			if v, ok := RecoverReturn(recover()); ok {
				result = v
			}
		}()

		args := DistributeSpreadExpressions(callCtx, argv)
		argFrame := make(map[string]Tval)

		if len(args) < len(argList) {
			fmt.Println("(ERR) function called with too few arguments: expected '" + fmt.Sprint(len(argList)) + "', got '" + fmt.Sprint(len(args)) + "' @ 'core.BindFun'.")
			return TvNil
		}

		for i := range argList {
			argFrame[argList[i]] = args[i]
		}

		if restArg != "" {
			argFrame[restArg] = TvSlice(args[len(argList):])
		}

		// The body runs under the captured ctx (so file-scoped imports +
		// package state come from the function's source), but with a fresh
		// frame and closure-capture context pushed. InFunction is flipped
		// on so top-level-only restrictions (like the var check) can tell
		// they're now inside a body.
		bodyCtx := defCtx
		bodyCtx.InFunction = true
		bodyCtx.PushFrame()
		defer bodyCtx.PopFrame()
		bodyCtx.PushContext(repr)
		defer bodyCtx.PopContext()

		for argName, argValue := range argFrame {
			bodyCtx.Env.Stack[0][argName] = tStackEntry{argValue, true}
		}

		return repr.Evaluate(bodyCtx)
	}
}

// BindMethod is BindFun with self-injection: argList[0] is bound from the
// instance pushed onto callCtx.Env.InstStack by the Dot accessor's wrapper.
func BindMethod(repr ttnode, argList []string, defCtx Context) tfun {
	var restArg string

	if len(argList) > 0 {
		finalArg := argList[len(argList)-1]

		if len(finalArg) > 0 && finalArg[0] == '#' {
			argList = argList[:len(argList)-1]
			restArg = finalArg[1:]
		}
	}

	return func(callCtx Context, argv []ttnode) (result Tval) {
		// Outermost defer: same job as BindFun's. Crucially, this runs
		// AFTER the Privileged-reset defer below, so a (return) from
		// inside a method body still clears the receiver's Privileged
		// flag before unwinding.
		defer func() {
			if v, ok := RecoverReturn(recover()); ok {
				result = v
			}
		}()

		args := DistributeSpreadExpressions(callCtx, argv)
		argFrame := make(map[string]Tval)

		argFrame[argList[0]] = callCtx.Env.InstStack[0]

		// argList[0] is `self`, bound from InstStack above. argList[1:] are
		// the user-supplied parameters; rest-arg has already been stripped
		// from argList earlier.
		userArgs := argList[1:]
		if len(args) < len(userArgs) {
			fmt.Println("(ERR) method called with too few arguments: expected '" + fmt.Sprint(len(userArgs)) + "', got '" + fmt.Sprint(len(args)) + "' @ 'core.BindMethod'.")
			return TvNil
		}

		for i := 0; i < len(userArgs); i++ {
			argFrame[userArgs[i]] = args[i]
		}

		if restArg != "" {
			argFrame[restArg] = TvSlice(args[len(userArgs):])
		}

		bodyCtx := defCtx
		bodyCtx.InFunction = true
		bodyCtx.PushFrame()
		defer bodyCtx.PopFrame()
		bodyCtx.PushContext(repr)
		defer bodyCtx.PopContext()

		for argName, argValue := range argFrame {
			bodyCtx.Env.Stack[0][argName] = tStackEntry{argValue, true}
		}

		inst := bodyCtx.Env.Stack[0][argList[0]].Val
		inst.Val.(*tinstance).Privileged = true
		defer func() { inst.Val.(*tinstance).Privileged = false }()

		return repr.Evaluate(bodyCtx)
	}
}

// BindCallback wraps an AST node as a parameterless tfun. Unlike BindFun,
// it does not capture a definition-site context: the body runs in the
// caller's ctx. Used by the `block`, `if`, and `while` builtins.
func BindCallback(repr ttnode) tfun {
	return func(ctx Context, argv []ttnode) Tval {
		ctx.PushContext(repr)
		defer ctx.PopContext()

		return repr.Evaluate(ctx)
	}
}
