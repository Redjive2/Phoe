package core

import (
	"regexp"
	"strconv"
)

// Leaf-classification regexes, compiled once. ttleaf.Evaluate runs on every
// leaf of every expression, so compiling these per call was a real cost.
var (
	numberPattern = regexp.MustCompile("^-?[0-9]+$")
	charPattern   = regexp.MustCompile("^`.`$")
	// A leading letter, no trailing underscore, and an optional single
	// trailing '?' (the Lisp/Ruby predicate convention, e.g. `atom?`).
	identPattern = regexp.MustCompile("^#?[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))\\??$")
	// An atom body is a valid identifier or an all-digit run (digits keep
	// leading zeros, so `:01` and `:1` are distinct atoms).
	atomDigitsPattern = regexp.MustCompile("^[0-9]+$")
)

// IsIdent reports whether s is a valid Pho identifier. Shared with the
// builtins package (the Dot accessor validates instance keys with it).
func IsIdent(s string) bool {
	return identPattern.MatchString(s)
}

// IsAtomName reports whether s is a legal atom body — a valid identifier or
// an all-digit run (the text after the leading ':'). Shared by the leaf
// evaluator and the str->atom builtin so both agree on what an atom is.
func IsAtomName(s string) bool {
	return identPattern.MatchString(s) || atomDigitsPattern.MatchString(s)
}

// spreadArg returns the inner expression of a `(spread expr)` argument and
// true; (nil, false) for anything else. A `(spread …)` that isn't a direct
// 2-element form (e.g. the `(spread rest)` marker nested inside a parameter
// list) is intentionally NOT matched here.
func spreadArg(node ttnode) (ttnode, bool) {
	if br, ok := AsBranch(node); ok && len(br) == 2 && br[0] == ttleaf("spread") {
		return br[1], true
	}
	return nil, false
}

// DistributeSpreadExpressions evaluates each argv node and flattens any
// (spread expr) wrapper, splicing the resulting array's elements in place.
// Spreading a non-array reports a diagnostic and returns ok=false; the
// caller must then skip the call entirely rather than invoke it with the
// spread argument silently dropped.
func DistributeSpreadExpressions(ctx Context, branch ttbranch) ([]Tval, bool) {
	var result []Tval

	for _, node := range branch {
		if expr, ok := spreadArg(node); ok {
			val := expr.Evaluate(ctx)
			listPtr, ok := val.Val.(*[]Tval)
			if !ok {
				ctx.Errorf(ErrBadSpread, "cannot spread a value of kind '%s' — only arrays can be spread", val.Kind)
				return nil, false
			}

			result = append(result, *listPtr...)
			continue
		}

		result = append(result, node.Evaluate(ctx))
	}

	return result, true
}

// ttvalue is a node that evaluates to a fixed, already-computed value. It
// has no surface syntax; expandSpread creates them to splice an array's
// elements back into a call's argument list as ordinary nodes, so any
// callee that evaluates its arguments sees the spliced values.
type ttvalue struct{ v Tval }

func (n ttvalue) Evaluate(Context) Tval { return n.v }

// Lit wraps an already-evaluated value as a Node that evaluates to itself.
// Used to feed pre-computed values into a fun call (e.g. handing a setter its
// new value), where the call interface expects unevaluated arg nodes.
func Lit(v Tval) Node { return ttvalue{v} }

// readProperty evaluates a free-standing property by calling its getter (a
// zero-arg fun). Struct-field properties go through the Dot accessor instead,
// which supplies the receiver instance as self. The getter is guaranteed
// KindFun at property-creation time.
func readProperty(ctx Context, prop tproperty) Tval {
	return prop.Getter.Val.(tfun)(ctx, nil)
}

// ReadProperty invokes a free-standing property's getter and returns the
// computed value. It lets code outside this package (the package-member
// accessor in builtins/dot.go, reading an exported `pkg.Prop`) delegate to
// the getter the same way the in-module leaf reader does. The value's Kind
// must be KindProperty.
func ReadProperty(ctx Context, v Tval) Tval {
	return readProperty(ctx, v.Val.(tproperty))
}

// expandSpread rewrites a call's argument list, replacing each direct
// `(spread arr)` argument with one node per element of the array `arr`
// evaluates to. Non-spread arguments are passed through untouched and
// UNEVALUATED — only the spread expressions themselves are evaluated here,
// so the fexpr model (builtins receive unevaluated nodes) is preserved for
// every other argument. The common no-spread case returns the input slice
// unchanged, with no allocation. ok=false means a spread target wasn't an
// array; the diagnostic is already reported and the caller must abort.
func expandSpread(ctx Context, argv ttbranch) (ttbranch, bool) {
	hasSpread := false
	for _, node := range argv {
		if _, ok := spreadArg(node); ok {
			hasSpread = true
			break
		}
	}
	if !hasSpread {
		return argv, true
	}

	out := make(ttbranch, 0, len(argv)+2)
	for _, node := range argv {
		expr, ok := spreadArg(node)
		if !ok {
			out = append(out, node)
			continue
		}
		val := expr.Evaluate(ctx)
		listPtr, ok := val.Val.(*[]Tval)
		if !ok {
			ctx.Errorf(ErrBadSpread, "cannot spread a value of kind '%s' — only arrays can be spread", val.Kind)
			return nil, false
		}
		for _, e := range *listPtr {
			out = append(out, ttvalue{e})
		}
	}
	return out, true
}

func (br ttbranch) Evaluate(ctx Context) Tval {
	if len(br) == 0 {
		return TvNil
	}

	// Splice any `(spread arr)` arguments into the call's argument list
	// before dispatch, so spread works uniformly in every call — builtin,
	// user fun, or constructor — not just in user-fun parameter binding.
	// A call with no spread argument is returned unchanged.
	args, ok := expandSpread(ctx, br[1:])
	if !ok {
		return TvNil
	}

	if fname, ok := br[0].(ttleaf); ok {
		fn, found := ctx.Resolve(string(fname))

		if !found {
			return ctx.Errorf(ErrUnresolved, "operation '%s' is not defined", string(fname))
		}

		switch fn.Kind {
		case KindFun:
			return fn.Val.(tfun)(ctx, args)
		case KindType:
			if v, ok := buildFromType(ctx, fn.Val.(*PhoType), args); ok {
				return v
			}
			return ctx.Errorf(ErrNotCallable, "type '%s' is not constructible (only struct types can be called)", fn.Val.(*PhoType).Name())
		default:
			return ctx.Errorf(ErrNotCallable, "'%s' is not callable (kind '%s')", string(fname), fn.Kind)
		}
	}

	// The head is an expression (a nested call, possibly span-wrapped).
	// Evaluate it generically: this also removes the unchecked ttbranch
	// assertion that could panic the host on exotic heads.
	fn := br[0].Evaluate(ctx)

	switch fn.Kind {
	case KindFun:
		return fn.Val.(tfun)(ctx, args)
	case KindType:
		if ctor, ok := ConstructorOf(fn.Val.(*PhoType)); ok {
			return ctor(ctx, args)
		}
		return ctx.Errorf(ErrNotCallable, "type '%s' is not constructible (only struct types can be called)", fn.Val.(*PhoType).Name())
	default:
		return ctx.Errorf(ErrNotCallable, "'%s' is not callable (kind '%s')", Inspect(br[0]), fn.Kind)
	}
}

func (lf ttleaf) Evaluate(ctx Context) Tval {
	s := string(lf)

	// match numbers (the lexer splits '.' into its own token, so decimal
	// fractions are reassembled by the Dot operator, not by this regex)
	if numberPattern.MatchString(s) {
		num, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return TvNum(num)
		}

		return ctx.Errorf(ErrBadLiteral, "value '%s' could not be parsed as a number", s)
		// match strings
	} else if IsStrLit(s) {
		return TvStr(unescapeStringLit(StrLitBody(s)))
		// match chars
	} else if charPattern.MatchString(s) {
		return TvChr(rune(s[1]))
		// match nil — `none` is the new spelling; `Nil` is accepted during the
		// syntax migration and dropped at the hard cutover (Doc/PlanV1/Syntax.md).
	} else if s == "Nil" || s == "none" {
		return TvNil
		// match bools — `true`/`false` are the new spellings; `True`/`False`
		// are likewise accepted transitionally.
	} else if s == "True" || s == "False" || s == "true" || s == "false" {
		return TvBool(s == "True" || s == "true")
		// match atoms (`:name` / `:123`); the lexer already glued the colon
		// to an identifier/digit run, so validate the body and intern it.
	} else if len(s) >= 2 && s[0] == ':' {
		body := s[1:]
		if IsAtomName(body) {
			return TvAtom(body)
		}
		return ctx.Errorf(ErrBadLiteral, "invalid atom '%s' — atoms must be an identifier or digits", s)
		// match identifiers
	} else if identPattern.MatchString(s) {
		data, found := ctx.Resolve(s)
		if !found {
			return ctx.Warnf(ErrUnresolved, "identifier '%s' is not defined", s)
		}
		// A free-standing property reads through its getter — the name holds a
		// KindProperty value, not the computed value itself.
		if data.Kind == KindProperty {
			return readProperty(ctx, data.Val.(tproperty))
		}
		return data
	}

	// last resort: for functions not using identifier syntax (+, -, etc)
	data, found := ctx.Resolve(s)
	if found {
		return data
	}

	return ctx.Errorf(ErrBadLiteral, "value '%s' could not be parsed", s)
}

// unescapeStringLit translates conventional C-style backslash escapes
// inside a string literal's body (the content between the quotes).
// The legacy backtick passthrough (“ `X “) is left untouched: the
// lexer already accepted the pair, and the backtick stays in the
// resulting string by design.
//
// An unknown escape (e.g. `\q`) is preserved literally — both the
// backslash and the following byte — rather than erroring. This keeps
// the lexer-eval contract loose: anything the lexer accepted gets a
// reasonable interpretation, and users adding their own escape habits
// don't get a hard failure for typos.
// UnescapeStringLit unescapes the BODY of a string literal (the text between
// the quotes) exactly as the evaluator does, so other packages (the linter's
// string-literal singleton types) parse string values identically.
func UnescapeStringLit(body string) string { return unescapeStringLit(body) }

// IsStrLit reports whether a leaf value s is a string literal — its text
// wrapped in the `'` string delimiter (the opening and closing `'` must both
// be present).
func IsStrLit(s string) bool {
	return len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\''
}

// StrLitBody returns the body of a string-literal leaf (the text between the
// delimiters). Callers must have confirmed IsStrLit(s) first.
func StrLitBody(s string) string { return s[1 : len(s)-1] }

// QuoteStrLit renders s as a Pho single-quoted string literal — the inverse of
// UnescapeStringLit(StrLitBody(...)). The delimiter and control bytes are
// escaped (a literal `'` becomes `\'`; a `"` needs no escape) so the result
// re-parses back to s.
func QuoteStrLit(s string) string {
	q := strconv.Quote(s) // "..." with Go's control/quote escaping
	body := q[1 : len(q)-1]
	out := make([]byte, 0, len(body)+2)
	out = append(out, '\'')
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			if body[i+1] == '"' {
				out = append(out, '"') // \" -> " (no escape needed in '...')
			} else {
				out = append(out, '\\', body[i+1])
			}
			i++
			continue
		}
		if c == '\'' {
			out = append(out, '\\', '\'') // ' -> \'
			continue
		}
		out = append(out, c)
	}
	out = append(out, '\'')
	return string(out)
}

func unescapeStringLit(body string) string {
	if !needsUnescape(body) {
		return body
	}
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out = append(out, body[i])
			continue
		}
		switch body[i+1] {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case '\\':
			out = append(out, '\\')
		case '"':
			out = append(out, '"')
		case '\'':
			out = append(out, '\'')
		case '%':
			// `\%` escapes a literal % so the interpolation pass
			// doesn't see it. Decoded back to a bare % at eval time.
			out = append(out, '%')
		case '0':
			out = append(out, 0)
		case 'a':
			out = append(out, 0x07)
		case 'b':
			out = append(out, 0x08)
		case 'f':
			out = append(out, 0x0C)
		case 'v':
			out = append(out, 0x0B)
		default:
			// Unknown escape — leave both bytes as-is.
			out = append(out, '\\', body[i+1])
		}
		i++
	}
	return string(out)
}

// needsUnescape is a fast pre-check: if a string body contains no
// backslash, we can return it untouched and skip the byte-by-byte
// pass. Most string literals don't contain escapes, so this saves
// an allocation in the common case.
func needsUnescape(body string) bool {
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' {
			return true
		}
	}
	return false
}
