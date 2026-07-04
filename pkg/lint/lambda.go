package lint

import (
	"fmt"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/span"
)

// The `lambda` builtin (Features.md §11): a flat-header anonymous callable —
//
//	(lambda[?!=]  [RecvType]  [self]  params…  [RetType]  ->  body)
//
// The parser mirrors the runtime's (pkg/builtins/lambda.go). Types and the
// effect suffix are erased for reference checking; the suffix still drives the
// `!`/`=` effect conventions (missing-bang / missing-equals on the lambda head).

// lambdaHeads is the lambda builtin family — every effect-suffix combination in
// the fixed order `?!=`. Kept in sync with builtins.lambdaSuffixes (guarded by
// the builtin-drift test) and seeded into builtinNames.
var lambdaHeads = []string{
	"lambda", "lambda?", "lambda!", "lambda=",
	"lambda?!", "lambda?=", "lambda!=", "lambda?!=",
}

// checkLambda reference-checks a lambda form. It peels the optional
// `[RecvType] self` receiver and trailing return type from the flat header,
// synthesizes a `(params)` list, and reuses walkFunctionBody in clause-param
// mode — which binds a `(Type name)` typed param's name and already exempts the
// §3 capitalized-param check (types are first-class in a lambda header).
func (w *walker) checkLambda(scope *Scope, br *ast.PBranch) {
	argv := br.Children[1:]

	arrow := -1
	for i, n := range argv {
		if lf, ok := n.(*ast.PLeaf); ok && lf.Value == "->" {
			arrow = i
			break
		}
	}
	if arrow < 0 || arrow != len(argv)-2 {
		w.emit(Diagnostic{
			File: w.file, Span: br.Span, Severity: SeverityError, Code: "bad-lambda",
			Message: "a lambda is written (lambda [Recv] [self] params… [Ret] -> body)",
		})
		return
	}
	header, body := argv[:arrow], argv[arrow+1]

	// Receiver: an optional leading `[RecvType] self`. owner drives self's shape
	// and the inMethod flag: "" = free function, "Unknown" = inferred receiver,
	// a concrete type name = explicit receiver type.
	owner, hasSelf := "", false
	if len(header) >= 2 && isTypeLeafNode(header[0]) && isSelfNode(header[1]) {
		owner, hasSelf, header = leafValue(header[0]), true, header[2:]
	} else if len(header) >= 1 && isSelfNode(header[0]) {
		owner, hasSelf, header = "Unknown", true, header[1:]
	}

	// Return type: an optional trailing bare Capitalized leaf (erased for refs).
	if n := len(header); n > 0 && isTypeLeafNode(header[n-1]) {
		header = header[:n-1]
	}

	items := make([]ast.PNode, 0, len(header)+1)
	if hasSelf {
		items = append(items, &ast.PLeaf{Value: "self", Span: br.Span})
	}
	items = append(items, header...)
	synth := &ast.PBranch{Open: "(", Close: ")", Children: items, Span: br.Span}

	prev := w.clauseParams
	w.clauseParams = true
	w.walkFunctionBody(scope, synth, body, owner)
	w.clauseParams = prev

	w.checkLambdaEffects(scope, headIdent(br), br.Span, synth, body)
}

// checkLambdaEffects checks the lambda's declared effect suffix against its
// body's inferred effects. A lambda's `!`/`=` suffix is its effect declaration
// (there is no separate signature to anchor on), so the diagnostic sits on the
// `lambda…` head. Like a named callable, only MISSING marks are flagged (a
// declared suffix is trusted). The `=` suffix is also what grants self-mutation,
// so it feeds recvMut and there is no effect-through-readonly for a lambda.
func (w *walker) checkLambdaEffects(scope *Scope, head string, at span.Span, argList, body ast.PNode) {
	if !EffectCheck || body == nil {
		return
	}
	hasBang := strings.Contains(head, "!")
	hasEquals := strings.Contains(head, "=")
	r := scanEffects(body, hasEquals, varParamNames(argList), freeVarClassifier(scope, argList, body))
	if r.set.needsBang() && !hasBang {
		w.emit(Diagnostic{
			File: w.file, Span: at, Severity: SeverityError, Code: "missing-bang",
			Message: fmt.Sprintf("this lambda has an environmental effect (%s) — write it 'lambda!'", r.set),
		})
	}
	if r.set.needsEquals() && !hasEquals {
		w.emit(Diagnostic{
			File: w.file, Span: at, Severity: SeverityError, Code: "missing-equals",
			Message: fmt.Sprintf("this lambda mutates a value passed to it (%s) — write it 'lambda='", r.set),
		})
	}
}

func isTypeLeafNode(n ast.PNode) bool {
	lf, ok := n.(*ast.PLeaf)
	return ok && lf.Value != "" && lf.Value[0] >= 'A' && lf.Value[0] <= 'Z'
}

func isSelfNode(n ast.PNode) bool {
	lf, ok := n.(*ast.PLeaf)
	return ok && lf.Value == "self"
}

func leafValue(n ast.PNode) string {
	if lf, ok := n.(*ast.PLeaf); ok {
		return lf.Value
	}
	return ""
}
