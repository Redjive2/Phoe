package core

import (
	"fmt"
	"regexp"
	"strconv"
)

// DistributeSpreadExpressions evaluates each argv node and flattens any
// (spread expr) wrapper, splicing the resulting array's elements in place.
func DistributeSpreadExpressions(ctx Context, branch ttbranch) []Tval {
	var result []Tval

	for _, node := range branch {
		if br, ok := node.(ttbranch); ok &&
			len(br) == 2 &&
			br[0] == ttleaf("spread") {

			list := *br[1].Evaluate(ctx).Val.(*[]Tval)

			for _, val := range list {
				result = append(result, val)
			}

			continue
		}

		result = append(result, node.Evaluate(ctx))
	}

	return result
}

func (br ttbranch) Evaluate(ctx Context) Tval {
	if len(br) == 0 {
		return TvNil
	}

	if fname, ok := br[0].(ttleaf); ok {
		fn, found := ctx.Resolve(string(fname))

		if !found {
			fmt.Println("(ERR): Operation '" + fname + "' not found @ 'core.ttbranch.Evaluate'.")
			return TvNil
		}

		switch fn.Kind {
		case KindFun:
			return fn.Val.(tfun)(ctx, br[1:])
		case KindConstructor:
			return fn.Val.(tconstructor).Constructor(ctx, br[1:])
		default:
			fmt.Println("(ERR): '" + string(fname) + "' is not callable (kind '" + fn.Kind + "') @ 'core.ttbranch.Evaluate'.")
			return TvNil
		}
	}

	funBranch := br[0].(ttbranch)
	fn := funBranch.Evaluate(ctx)

	switch fn.Kind {
	case KindFun:
		return fn.Val.(tfun)(ctx, br[1:])
	case KindConstructor:
		return fn.Val.(tconstructor).Constructor(ctx, br[1:])
	default:
		fmt.Println("(ERR): '" + Inspect(funBranch) + "' is not callable (kind '" + fn.Kind + "') @ 'core.ttbranch.Evaluate'.")
		return TvNil
	}
}

func (lf ttleaf) Evaluate(ctx Context) Tval {
	s := string(lf)

	// match numbers (the lexer splits '.' into its own token, so decimal
	// fractions are reassembled by the Dot operator, not by this regex)
	if regexp.MustCompile("^-?[0-9]+$").MatchString(s) {
		num, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return TvNum(num)
		}

		fmt.Println("(ERR): Value '" + s + "' could not be parsed as a number @ 'core.ttleaf.Evaluate'.")
		return TvNil
		// match strings
	} else if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return TvStr(unescapeStringLit(s[1 : len(s)-1]))
		// match chars
	} else if regexp.MustCompile("^`.`$").MatchString(s) {
		return TvChr(rune(s[1]))
		// match nil
	} else if s == "Nil" {
		return TvNil
		// match bools
	} else if s == "True" || s == "False" {
		return TvBool(s == "True")
		// match identifiers
	} else if regexp.MustCompile("^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))$").MatchString(s) {
		data, found := ctx.Resolve(s)
		if !found {
			fmt.Println("(WARN): Identifier '" + s + "' not found @ 'core.ttleaf.Evaluate'.")
			return TvNil
		}

		return data
	}

	// last resort: for functions not using identifier syntax (+, -, etc)
	data, found := ctx.Resolve(s)
	if found {
		return data
	}

	fmt.Println("(ERR): Value '" + s + "' could not be parsed @ 'core.ttleaf.Evaluate'.")
	return TvNil
}

// unescapeStringLit translates conventional C-style backslash escapes
// inside a string literal's body (the content between the quotes).
// The legacy backtick passthrough (`` `X ``) is left untouched: the
// lexer already accepted the pair, and the backtick stays in the
// resulting string by design.
//
// An unknown escape (e.g. `\q`) is preserved literally — both the
// backslash and the following byte — rather than erroring. This keeps
// the lexer-eval contract loose: anything the lexer accepted gets a
// reasonable interpretation, and users adding their own escape habits
// don't get a hard failure for typos.
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
