package builtins

import "pho/pkg/core"

// The `lambda` builtin (Features.md §11): a first-class anonymous callable that
// can carry parameter types, a return type, an optional receiver, and effect
// suffixes. The header is FLAT — there is no parenthesized parameter list:
//
//	(lambda[?!=]  [RecvType]  [self]  <params…>  [RetType]  ->  body)
//
//   - A receiver is signalled by the literal token `self`. A bare Capitalized
//     leaf immediately before it is the explicit receiver type (`Self` means the
//     inferred/generic self); `self` alone infers the receiver type. No `self`
//     → a free function. `self` is reserved: a parameter may be named `self`
//     only as the receiver.
//   - Each parameter is a grouped typed param `(Type name)` or a bare lowercase
//     name (inferred type).
//   - A trailing bare Capitalized leaf (the last header token) is the return
//     type; otherwise the return type is inferred.
//   - The `!`/`=`/`?` suffix on `lambda` declares the lambda's effects.
//
// Types and the effect suffix are ERASED at runtime (like a signature): every
// suffix variant shares one handler, and the value is a plain callable whose
// receiver, when present, is simply its first parameter named `self`. The
// linter reads the types/effects for inference and the `!`/`=` conventions.

// lambdaSuffixes enumerates every effect-suffix combination in the fixed order
// `?!=` (Doc/PlanV1/Effects.md), so `(lambda! …)`, `(lambda= …)`, `(lambda?! …)`
// etc. all resolve to a builtin.
var lambdaSuffixes = []string{"", "?", "!", "=", "?!", "?=", "!=", "?!="}

// lambdaBuiltins registers the `lambda` family. Every suffix variant shares the
// same handler — the suffix is a static effect declaration the linter checks,
// not a runtime distinction.
func lambdaBuiltins() map[string]core.StackEntry {
	out := make(map[string]core.StackEntry, len(lambdaSuffixes))
	for _, sfx := range lambdaSuffixes {
		out["lambda"+sfx] = global(lambdaForm)
	}
	return out
}

// lambdaForm parses the flat lambda header and returns the bound callable.
func lambdaForm(ctx core.Context, argv []core.Node) core.Value {
	// Split the header from the body at the sole `->` leaf.
	arrow := -1
	for i, n := range argv {
		if lf, ok := core.AsLeaf(n); ok && string(lf) == "->" {
			arrow = i
			break
		}
	}
	if arrow < 0 {
		return ctx.Errorf(core.ErrBadForm, "'lambda' needs a '->' before its body: (lambda [Recv] [self] params… [Ret] -> body)")
	}
	if arrow != len(argv)-2 {
		return ctx.Errorf(core.ErrBadForm, "'lambda' takes exactly one body expression after '->'")
	}
	header, body := argv[:arrow], argv[arrow+1]

	// Receiver: an optional leading `[RecvType] self`.
	hasSelf := false
	if len(header) >= 2 && isTypeLeaf(header[0]) && isSelfLeaf(header[1]) {
		hasSelf, header = true, header[2:] // explicit receiver type (erased)
	} else if len(header) >= 1 && isSelfLeaf(header[0]) {
		hasSelf, header = true, header[1:] // inferred receiver type
	}

	// Return type: an optional trailing bare Capitalized leaf (erased).
	if n := len(header); n > 0 && isTypeLeaf(header[n-1]) {
		header = header[:n-1]
	}

	// The rest are parameters — a bare lowercase name or a typed `(Type name)`.
	argList := make([]string, 0, len(header)+1)
	if hasSelf {
		argList = append(argList, "self")
	}
	for _, p := range header {
		if isTypeLeaf(p) {
			return ctx.Errorf(core.ErrBadForm, "'lambda': a type may only be the receiver type (before 'self') or the return type (before '->'), not a bare parameter — got '%s'", core.Inspect(p))
		}
		name, ok := declBindName(ctx, p, "lambda")
		if !ok {
			return core.TvNil
		}
		if name == "self" {
			return ctx.Errorf(core.ErrBadForm, "'lambda': 'self' is the receiver name — put it first (optionally after a receiver type), not among the parameters")
		}
		argList = append(argList, name)
	}

	return core.TvFun(core.BindFun("<lambda>", body, argList, nil, ctx))
}

// isTypeLeaf reports whether a node is a bare Capitalized identifier leaf — a
// type name in receiver/return position. Its string value is returned too.
func isTypeLeaf(n core.Node) bool {
	lf, ok := core.AsLeaf(n)
	if !ok {
		return false
	}
	s := string(lf)
	return s != "" && s[0] >= 'A' && s[0] <= 'Z'
}

// isSelfLeaf reports whether a node is the bare `self` receiver token.
func isSelfLeaf(n core.Node) bool {
	lf, ok := core.AsLeaf(n)
	return ok && string(lf) == "self"
}
