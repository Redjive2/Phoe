package lint

import (
	"fmt"

	"pho/pkg/ast"
	"pho/pkg/span"
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
func (w *walker) checkSpecialFormShape(br *ast.PBranch) {
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
		// (method Receiver.Name (args) body) — Receiver.Name is a dot PATTERN
		// (not code): the receiver names the owning struct, the name the method.
		if nargs != 3 {
			w.emitArity(br, head, "3 (Receiver.Name args body)", nargs)
			return
		}
		w.expectMethodPattern(br.Children[1])
		w.expectQuotedList(br.Children[2], "method: argument list")
		// Body (Children[3]) is any expression — no shape check.

	case "struct":
		if nargs < 1 {
			w.emitArity(br, head, "at least 1 (a name)", nargs)
			return
		}
		// Typed-field form `(struct Name.{ F T … })` parses to
		// `(struct (Name "F" T …))` — a single branch whose head is the struct
		// name and whose rest are quoted-name / type pairs. The names are string
		// literals and the types are expressions, neither a bare identifier, so
		// check only that the branch head names the struct (declOf reads the
		// pairs).
		if inner, ok := br.Children[1].(*ast.PBranch); ok && inner.Open == "(" {
			if len(inner.Children) >= 1 {
				w.expectQuotedIdent(inner.Children[0], "struct: name")
			}
			break
		}
		// Bare form `(struct Name f0 f1 …)` — a bare name then bare field idents.
		w.expectQuotedIdent(br.Children[1], "struct: name")
		for _, c := range br.Children[2:] {
			w.expectQuotedIdent(c, "struct: field")
		}

	case "property":
		// (property Recv.Name get getter)             — read-only
		// (property Recv.Name get getter set setter)  — read-write
		if nargs != 3 && nargs != 5 {
			w.emitArity(br, head, "3 (Name get getter) or 5 (… set setter)", nargs)
			return
		}
		w.expectKeyword(br.Children[2], "get", "property")
		if nargs == 5 {
			w.expectKeyword(br.Children[4], "set", "property")
		}

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
		// Binding names are evaluated at runtime: a quoted symbol ('x) is a
		// literal name, a bare identifier (x) a dynamic one — so the name may
		// be any expression. The check pass (checkDeclName) flags an unquoted
		// bare identifier, which is almost always a forgotten quote.

	case "let":
		// (let [var] name = value [name = value]*) — an optional `var` modifier
		// leads, then name/value pairs joined by `=` markers.
		args := br.Children[1:]
		if len(args) >= 1 {
			if mod, ok := args[0].(*ast.PLeaf); ok && mod.Value == "var" {
				args = args[1:]
			}
		}
		if len(args) < 3 || len(args)%3 != 0 {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     headSpan(br),
				Severity: SeverityError,
				Code:     "bad-form-arity",
				Message:  "'let' expects 'name = value' bindings (an optional 'var' modifier may lead)",
			})
			return
		}
		for j := 1; j < len(args); j += 3 {
			if eq, ok := args[j].(*ast.PLeaf); !ok || eq.Value != "=" {
				w.emit(Diagnostic{
					File:     w.file,
					Span:     args[j-1].GetSpan(),
					Severity: SeverityError,
					Code:     "bad-form-arity",
					Message:  "'let' binding expects 'name = value' (missing '=')",
				})
				return
			}
		}

	case "if", "unless":
		// (if cond then expr [elif cond then expr]* [else expr]) and the
		// elif-less (unless cond then expr [else expr]). parseIfForm validates
		// the then/elif/else keyword layout and reports the first problem.
		if f := parseIfForm(br, head, head == "if"); f.Bad != "" {
			w.emit(Diagnostic{
				File:     w.file,
				Span:     f.BadSpan,
				Severity: SeverityError,
				Code:     "bad-form-shape",
				Message:  f.Bad,
			})
		}

	case "foreach":
		// (foreach name in collection body)
		if nargs != 4 {
			w.emitArity(br, head, "4 (name in collection body)", nargs)
			return
		}
		w.expectKeyword(br.Children[2], "in", "foreach")

	case "while", "until":
		// (while cond then body) / (until cond then body)
		if nargs != 3 {
			w.emitArity(br, head, "3 (cond then body)", nargs)
			return
		}
		w.expectKeyword(br.Children[2], "then", head)

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
		switch lhs.(type) {
		case *ast.PLeaf:
			// Bare name target — (= sum 5).
		case *ast.PDot:
			// Dot / index accessor — (= obj.field 5), (= arr.[i] 5).
		default:
			w.emit(Diagnostic{
				File:     w.file,
				Span:     lhs.GetSpan(),
				Severity: SeverityError,
				Code:     "bad-form-shape",
				Message:  "=: target must be a bare name (sum) or a dot accessor (a.b)" + sigilHint(lhs),
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
func headSpan(br *ast.PBranch) span.Span {
	if len(br.Children) > 0 {
		return br.Children[0].GetSpan()
	}
	return br.Span
}

func (w *walker) emitArity(br *ast.PBranch, head, expected string, got int) {
	w.emit(Diagnostic{
		File:     w.file,
		Span:     headSpan(br),
		Severity: SeverityError,
		Code:     "bad-form-arity",
		Message: fmt.Sprintf("'%s' expects %s argument(s); got %d",
			head, expected, got),
	})
}

// Post-cutover the declaration/control forms take BARE arguments — the
// '/& sigils are gone. The helpers below enforce the slots that still have
// a fixed shape: names must be bare identifiers, parameter and field lists
// must be parenthesized. Body and arm slots accept any expression (a quoted
// VALUE like 'overwrite is legitimate there), so their former checks are
// now no-ops, kept to preserve the call sites during the cutover.

// expectQuotedIdent flags `n` unless it's a bare identifier. A leftover
// `'name` is a PSigil, not an identifier, so it's reported with a hint.
func (w *walker) expectQuotedIdent(n ast.PNode, ctx string) {
	if _, _, ok := declIdent(n); ok {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     n.GetSpan(),
		Severity: SeverityError,
		Code:     "bad-form-shape",
		Message:  ctx + ": expected a bare identifier (e.g. name)" + sigilHint(n),
	})
}

// expectQuoted is a no-op: function/method bodies and the block argument
// are ordinary expressions post-cutover, with no required shape.
func (w *walker) expectQuoted(n ast.PNode, ctx string) {}

// expectQuotedList flags `n` unless it's a bare parenthesized list `(a b …)`.
// Used for parameter lists and struct fields.
func (w *walker) expectQuotedList(n ast.PNode, ctx string) {
	if _, ok := declList(n); ok {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     n.GetSpan(),
		Severity: SeverityError,
		Code:     "bad-form-shape",
		Message:  ctx + ": expected a parenthesized list (e.g. (a b))" + sigilHint(n),
	})
}

// expectBlockSigil is a no-op: if/for arms are ordinary expressions
// post-cutover (the deferring `&` is gone), with no required shape.
func (w *walker) expectBlockSigil(n ast.PNode, ctx string) {}

// expectMethodPattern flags `n` unless it's a `Receiver.Name` dot pattern —
// two bare identifiers joined by a dot. It's the structural shape of a method
// declaration's first argument (the receiver and method name), not code.
func (w *walker) expectMethodPattern(n ast.PNode) {
	if dot, ok := n.(*ast.PDot); ok {
		_, lok := dot.LHS.(*ast.PLeaf)
		_, rok := dot.RHS.(*ast.PLeaf)
		if lok && rok {
			return
		}
	}
	// A bare receiver leaf is the ANONYMOUS method form `(method Receiver
	// (args) body)` — valid (used as a property get/set delegate).
	if leaf, ok := n.(*ast.PLeaf); ok && looksLikeIdentifier(leaf.Value) {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     n.GetSpan(),
		Severity: SeverityError,
		Code:     "bad-form-shape",
		Message:  "method: expected a 'Receiver.Name' pattern (e.g. Point.Shift) or a bare 'Receiver' for an anonymous method",
	})
}

// expectKeyword flags a bad-form-shape when n isn't the bare keyword `kw` —
// the noop markers `in` (foreach) and `then` (while/until) that must sit
// between the form's operands.
func (w *walker) expectKeyword(n ast.PNode, kw, form string) {
	if leaf, ok := n.(*ast.PLeaf); ok && leaf.Value == kw {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     n.GetSpan(),
		Severity: SeverityError,
		Code:     "bad-form-shape",
		Message:  fmt.Sprintf("'%s': expected the keyword '%s' here", form, kw),
	})
}

// sigilHint returns a pointed suffix when n is a stray `&` block sigil sitting
// where a bare name or list is expected — a common typo, since a block sigil
// is only valid in value position.
func sigilHint(n ast.PNode) string {
	if sig, ok := n.(*ast.PSigil); ok {
		return "; remove the leading '" + sig.Sigil + "' — a block sigil isn't valid here"
	}
	return ""
}
