package lint

import (
	"fmt"

	"pho/pkg/core"
)

// checkSpecialFormShape validates the structural shape of a single
// special-form call: arity and the syntactic kind of each argument.
// Pho uses syntactic distinctions (`'name`, `'(...)`, `&expr`) to
// pick out declaration sites and deferred bodies — a special form
// with the wrong sigil pattern will either crash the runtime or
// silently produce wrong behavior, so flagging the shape statically
// is the cheapest correctness boost we can offer.
//
// Diagnostics are emitted but do not halt the walk; the regular
// case-handlers in checkBranch still run on the best-effort tree so
// downstream reference-checking keeps firing.
func (w *walker) checkSpecialFormShape(br *core.PBranch) {
	head := headIdent(br)
	nargs := len(br.Children) - 1

	switch head {
	case "fun":
		// (fun 'name '(args) '(body))   — named
		// (fun '(args) '(body))         — anonymous
		// The body can be any quoted form, not just `'(...)`: bodies
		// like `'value` (the identity function `(fun '(value) 'value)`)
		// or `'42` are valid — the runtime just evaluates whatever
		// node Derepr returns. Only the argument list must be
		// parenthesized.
		switch nargs {
		case 2:
			w.expectQuotedList(br.Children[1], "fun: argument list")
			w.expectQuoted(br.Children[2], "fun: body")
		case 3:
			w.expectQuotedIdent(br.Children[1], "fun: name")
			w.expectQuotedList(br.Children[2], "fun: argument list")
			w.expectQuoted(br.Children[3], "fun: body")
		default:
			w.emitArity(br, head, "2 or 3", nargs)
		}

	case "method":
		// (method Owner 'name '(args) '(body))
		// Body is any quoted form — same rule as fun.
		if nargs != 4 {
			w.emitArity(br, head, "4", nargs)
			return
		}
		// Owner (Children[1]) is a runtime expression — no shape check.
		w.expectQuotedIdent(br.Children[2], "method: name")
		w.expectQuotedList(br.Children[3], "method: argument list")
		w.expectQuoted(br.Children[4], "method: body")

	case "struct":
		// (struct 'name '(fields))
		if nargs != 2 {
			w.emitArity(br, head, "2", nargs)
			return
		}
		w.expectQuotedIdent(br.Children[1], "struct: name")
		w.expectQuotedList(br.Children[2], "struct: fields")

	case "var", "const":
		// (var 'a v1 'b v2 ...) — pairs of (name, value).
		if nargs < 2 || nargs%2 != 0 {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     headSpan(br),
				Severity: SeverityError,
				Code:     "bad-form-arity",
				Message: fmt.Sprintf(
					"'%s' expects an even number of arguments (name/value pairs); got %d",
					head, nargs),
			})
			return
		}
		for i := 1; i+1 < len(br.Children); i += 2 {
			w.expectQuotedIdent(br.Children[i], head+": binding name")
		}

	case "if":
		// (if cond &then) or (if cond &then &else). Cond is an
		// expression; the arms must be blocks, since `if` calls
		// BindCallback on them at runtime.
		switch nargs {
		case 2:
			w.expectBlockSigil(br.Children[2], "if: then-arm")
		case 3:
			w.expectBlockSigil(br.Children[2], "if: then-arm")
			w.expectBlockSigil(br.Children[3], "if: else-arm")
		default:
			w.emitArity(br, head, "2 or 3", nargs)
		}

	case "for":
		// (for &cond &body)             — while-style
		// (for 'name collection &body)  — iterator-style
		switch nargs {
		case 2:
			w.expectBlockSigil(br.Children[1], "for: condition")
			w.expectBlockSigil(br.Children[2], "for: body")
		case 3:
			w.expectQuotedIdent(br.Children[1], "for: loop variable")
			// Collection is a runtime expression — no shape check.
			w.expectBlockSigil(br.Children[3], "for: body")
		default:
			w.emitArity(br, head, "2 or 3", nargs)
		}

	case "=":
		// LHS forms accepted by the runtime's `=`:
		//   bare leaf      `(= sum 5)`        — leaf text is the name
		//   quoted ident   `(= 'sum 5)`       — listifies to a string leaf
		//   string literal `(= "sum" 5)`      — same: leaf with quotes
		//   dot accessor   `(= obj.field 5)`  — Branch{Dot, …}
		// Anything else (a bare `(...)` form, a `&`-block, etc.)
		// crashes at the .(Leaf) / .(Branch) type assertions.
		if nargs != 2 {
			w.emitArity(br, head, "2", nargs)
			return
		}
		lhs := br.Children[1]
		switch n := lhs.(type) {
		case *core.PLeaf:
			// Any leaf — bare ident, quoted-ident-after-listify, or
			// string literal — is acceptable.
		case *core.PDot:
			_ = n
		case *core.PSigil:
			if n.Sigil != "'" {
				w.emit(Diagnostic{
					File:     w.file,
					Span:     lhs.GetSpan(),
					Severity: SeverityError,
					Code:     "bad-form-shape",
					Message:  "=: LHS must be a name or a dot accessor, not a block",
				})
			}
		default:
			w.emit(Diagnostic{
				File:     w.file,
				Span:     lhs.GetSpan(),
				Severity: SeverityError,
				Code:     "bad-form-shape",
				Message:  "=: LHS must be a name (sum, 'sum) or a dot accessor (a.b)",
			})
		}

	case "block":
		// (block 'expr) — rarely written by hand; & is the sugared form.
		if nargs != 1 {
			w.emitArity(br, head, "1", nargs)
			return
		}
		w.expectQuoted(br.Children[1], "block: body")

	case "do":
		// (do expr ...) — at least one child.
		if nargs < 1 {
			w.emitArity(br, head, "1 or more", nargs)
		}

	case "return":
		// (return) or (return expr). Anything else is an arity error;
		// the runtime would print its own error and return Nil.
		if nargs > 1 {
			w.emitArity(br, head, "0 or 1", nargs)
		}

	case "break", "continue":
		// (break) / (continue) — no arguments.
		if nargs != 0 {
			w.emitArity(br, head, "0", nargs)
		}
	}
}

// headSpan returns the head identifier's span (Children[0].GetSpan())
// when there is one, falling back to the whole-form span. Used so
// arity diagnostics point at the form name rather than the entire
// (possibly very long) form.
func headSpan(br *core.PBranch) core.Span {
	if len(br.Children) > 0 {
		return br.Children[0].GetSpan()
	}
	return br.Span
}

func (w *walker) emitArity(br *core.PBranch, head, expected string, got int) {
	w.emit(Diagnostic{
		File:     w.file,
		Span:     headSpan(br),
		Severity: SeverityError,
		Code:     "bad-form-arity",
		Message: fmt.Sprintf("'%s' expects %s argument(s); got %d",
			head, expected, got),
	})
}

// expectQuotedIdent flags `n` if it isn't `'name` (a tick followed by
// a bare identifier). `ctx` describes the position in the form so the
// diagnostic message can pinpoint which slot is malformed.
func (w *walker) expectQuotedIdent(n core.PNode, ctx string) {
	if _, _, ok := quotedIdent(n); ok {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     n.GetSpan(),
		Severity: SeverityError,
		Code:     "bad-form-shape",
		Message:  ctx + ": expected a quoted identifier (e.g. 'name)",
	})
}

// expectQuoted flags `n` if it isn't `'expr` — any quoted form. The
// inner expression can be a leaf, a parenthesized list, or anything
// else the parser produced; only the leading tick is required. Used
// for function/method bodies and the `block` builtin's argument,
// where the runtime evaluates whatever node Derepr hands back.
func (w *walker) expectQuoted(n core.PNode, ctx string) {
	if sig, ok := n.(*core.PSigil); ok && sig.Sigil == "'" {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     n.GetSpan(),
		Severity: SeverityError,
		Code:     "bad-form-shape",
		Message:  ctx + ": expected a quoted form (e.g. 'name or '(...))",
	})
}

// expectQuotedList flags `n` if it isn't `'(...)` — a tick wrapping
// a parenthesized form. Used for argument lists and struct fields,
// where the inner has to enumerate names.
func (w *walker) expectQuotedList(n core.PNode, ctx string) {
	sig, ok := n.(*core.PSigil)
	if !ok || sig.Sigil != "'" {
		w.emit(Diagnostic{
			File:     w.file,
			Span:     n.GetSpan(),
			Severity: SeverityError,
			Code:     "bad-form-shape",
			Message:  ctx + ": expected a quoted form (e.g. '(...))",
		})
		return
	}
	if br, ok := sig.Inner.(*core.PBranch); !ok || br.Open != "(" {
		w.emit(Diagnostic{
			File:     w.file,
			Span:     n.GetSpan(),
			Severity: SeverityError,
			Code:     "bad-form-shape",
			Message:  ctx + ": expected a quoted parenthesized form (e.g. '(...))",
		})
	}
}

// expectBlockSigil flags `n` if it isn't `&expr` — a `&` sigil wrapping
// any expression. The runtime expects blocks (lowered to (block ...))
// for if/for arms, and treating a bare expression as one would crash
// at the .(core.Branch) type assertion in the if/for builtins.
func (w *walker) expectBlockSigil(n core.PNode, ctx string) {
	sig, ok := n.(*core.PSigil)
	if !ok || sig.Sigil != "&" {
		w.emit(Diagnostic{
			File:     w.file,
			Span:     n.GetSpan(),
			Severity: SeverityError,
			Code:     "bad-form-shape",
			Message:  ctx + ": expected a block (e.g. &expr or &(do ...))",
		})
	}
}
