package builtins

import (
	"math"
	"reflect"
	"strings"
	"unicode/utf8"

	"pho/pkg/core"
)

// numIndex converts a numeric index into a slice/string position in [0,length).
// ok=false (out of range) when the index is non-finite (NaN/±Inf) or negative —
// including a fraction in (-1,0), which int() would otherwise truncate toward
// zero into a spurious valid 0. A non-negative fractional index truncates toward
// zero (a.[1.9] -> 1), preserving the established lenient behavior.
func numIndex(idx float64, length int) (int, bool) {
	if math.IsNaN(idx) || math.IsInf(idx, 0) || idx < 0 {
		return 0, false
	}
	i := int(idx)
	if i >= length {
		return 0, false
	}
	return i, true
}

// asCount validates a numeric count/bound argument: it must be finite, since
// int(NaN)/int(±Inf) silently become 0 (or garbage) and would otherwise be
// accepted as a no-op count. Returns the value truncated toward zero, or
// ok=false after reporting a diagnostic. caller is the builtin name.
func asCount(ctx core.Context, n float64, caller string) (int, bool) {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		ctx.Errorf(core.ErrType, "'%s': count must be a finite number, got %v", caller, n)
		return 0, false
	}
	return int(n), true
}

// global wraps a builtin function as a constant StackEntry suitable for
// installation into the global environment.
func global(fn func(ctx core.Context, argv []core.Node) core.Value) core.StackEntry {
	return core.StackEntry{Val: core.TvFun(fn), IsConstant: true}
}

// Pho strings are sequences of runes: len, indexing, slicing, and `foreach`
// all count by rune, so they agree on multi-byte UTF-8 (for ASCII this is
// identical to byte indexing). These helpers centralize that rule so every
// string operation shares one definition rather than each indexing bytes.

// strLen is the rune length of s.
func strLen(s string) int { return utf8.RuneCountInString(s) }

// strRuneAt returns the i-th rune of s and whether i is in range.
func strRuneAt(s string, i int) (rune, bool) {
	if i < 0 {
		return 0, false
	}
	for _, r := range s {
		if i == 0 {
			return r, true
		}
		i--
	}
	return 0, false
}

// strRuneSlice returns the substring spanning runes [lo, hi). Bounds must
// already be validated against strLen(s).
func strRuneSlice(s string, lo, hi int) string {
	return string([]rune(s)[lo:hi])
}

// scalarKey reports whether k may be used as a dict key, reporting a
// diagnostic if not. Only scalars (num/str/bool/chr/nil) qualify: they
// compare by value, so the Go map agrees with structural `==`. Composite
// values (array/dict/instance) are pointer-backed — the map would key them
// by identity, contradicting `==` — and function-like values (fun/method/
// constructor) aren't even comparable, so using one as a key would panic
// the host. Rejecting both at insertion keeps dicts predictable.
func scalarKey(ctx core.Context, k core.Value, caller string) bool {
	switch k.Kind {
	case core.KindNum:
		// NaN never compares equal to itself, so a NaN key can never be looked
		// up again — and multiple NaN inserts each "exist" yet inflate len.
		// Reject it so the value-equality retrieval invariant holds.
		if n, ok := k.Val.(float64); ok && math.IsNaN(n) {
			ctx.Errorf(core.ErrType, "'%s': NaN cannot be used as a dict key", caller)
			return false
		}
		return true
	case core.KindStr, core.KindBool, core.KindChr, core.KindNil, core.KindAtom, core.KindType:
		// KindType values are interned (one *PhoType per structure), so they
		// key by pointer identity — consistent with structural `==`.
		return true
	}
	ctx.Errorf(core.ErrType, "'%s': dict keys must be scalar (num, str, bool, chr, nil, atom, type); got '%s'", caller, k.Kind)
	return false
}

// asBool extracts a bool from a Value, reporting a diagnostic and
// returning (false, false) if the value isn't a bool. caller is the
// builtin name for error messages.
func asBool(ctx core.Context, v core.Value, caller string) (bool, bool) {
	b, ok := v.Val.(bool)
	if !ok {
		ctx.Errorf(core.ErrType, "'%s' expected a 'bool' argument, got '%s'", caller, v.Kind)
		return false, false
	}
	return b, true
}

// asNum extracts a float64 from a Value, reporting a diagnostic and
// returning (0, false) if the value isn't a num. caller is the builtin
// name for error messages.
func asNum(ctx core.Context, v core.Value, caller string) (float64, bool) {
	n, ok := v.Val.(float64)
	if !ok {
		ctx.Errorf(core.ErrType, "'%s' expected a 'num' argument, got '%s'", caller, v.Kind)
		return 0, false
	}
	return n, true
}

// tvalEqual is structural equality on Values. Arrays and dicts are compared
// element-wise; scalars use Go's == on the underlying Val. Dict comparison
// is reliable because dict keys are constrained to scalars (see scalarKey),
// so the cross-lookup `bm[k]` matches by value the same way `==` does.
func tvalEqual(a, b core.Value) bool {
	if a.Kind != b.Kind {
		return false
	}

	switch a.Kind {
	case core.KindAtom, core.KindType:
		// Atoms and types are interned, so identity is equality: structurally
		// equal values carry the identical *core.Atom / *core.PhoType pointer.
		return a.Val == b.Val

	case core.KindArray:
		as := *a.Val.(*[]core.Value)
		bs := *b.Val.(*[]core.Value)
		if len(as) != len(bs) {
			return false
		}
		for i := range as {
			if !tvalEqual(as[i], bs[i]) {
				return false
			}
		}
		return true

	case core.KindDict:
		am := *a.Val.(*map[core.Value]core.Value)
		bm := *b.Val.(*map[core.Value]core.Value)
		if len(am) != len(bm) {
			return false
		}
		for k, v := range am {
			bv, ok := bm[k]
			if !ok || !tvalEqual(v, bv) {
				return false
			}
		}
		return true

	default:
		// Funs, methods, and other reference kinds hold uncomparable Go
		// values; == on them would panic. They are never structurally
		// equal to anything.
		if a.Val == nil || b.Val == nil {
			return a.Val == b.Val
		}
		if !reflect.TypeOf(a.Val).Comparable() || !reflect.TypeOf(b.Val).Comparable() {
			return false
		}
		return a.Val == b.Val
	}
}

type importRequest struct {
	PackagePath string
	Alias       string
}

// parseImportRequests handles both bare-string and aliased-pair import args.
// Caller is the builtin name, used in error messages.
//
// The aliased form is ("path" alias) — a parenthesized pair whose head is the
// path string and whose tail is a bare alias name. It must NOT be evaluated
// as a whole (its head string isn't callable), so we inspect the branch: the
// path leaf is evaluated to a string, and the alias is taken verbatim as a
// bare identifier (a name slot, not a runtime value — evaluating it would
// resolve it as a variable).
func parseImportRequests(ctx core.Context, argv []core.Node, caller string) []importRequest {
	requests := make([]importRequest, 0, len(argv))

	for _, argNode := range argv {
		// ("path/to/lib" alias) -> importRequest{"path/to/lib", "alias"}
		if br, ok := core.AsBranch(argNode); ok {
			if len(br) != 2 {
				ctx.Errorf(core.ErrBadImport, "'%s' cannot parse aliased import request — expected (\"path\" alias)", caller)
				continue
			}
			path := br[0].Evaluate(ctx)
			aliasLeaf, isLeaf := core.AsLeaf(br[1])
			if path.Kind != core.KindStr || !isLeaf || !core.IsIdent(string(aliasLeaf)) {
				ctx.Errorf(core.ErrBadImport, "'%s' cannot parse aliased import request — expected (\"path\" alias) with a bare alias name", caller)
				continue
			}
			requests = append(requests, importRequest{path.Val.(string), string(aliasLeaf)})
			continue
		}

		// "path/to/lib" -> importRequest{"path/to/lib", "lib"} (alias = basename)
		arg := argNode.Evaluate(ctx)
		if arg.Kind == core.KindStr {
			parts := strings.Split(arg.Val.(string), "/")
			requests = append(requests, importRequest{arg.Val.(string), parts[len(parts)-1]})
			continue
		}

		ctx.Errorf(core.ErrBadImport, "'%s' cannot parse import request '%v' — expected a path string or (\"path\" alias)", caller, arg.Val)
	}

	return requests
}

// ParseArgs validates a positional argument list against a type pattern.
// pat entries are Kind* constants; "..." in the trailing position permits a
// variadic tail. Arity is checked before anything is evaluated: a short
// argv must not reach callers (they index the result by pattern position),
// and surplus arguments to a non-variadic pattern are rejected rather than
// silently ignored — a deliberate tightening introduced with the
// diagnostics migration. Returns the evaluated argument values and a bool
// indicating whether the arity and all positional types matched.
func ParseArgs(ctx core.Context, caller string, pat []string, argv []core.Node) ([]any, bool) {
	variadic := len(pat) > 0 && pat[len(pat)-1] == "..."
	required := len(pat)
	if variadic {
		required--
	}

	name := strings.TrimPrefix(caller, "builtins.")

	if len(argv) < required || (!variadic && len(argv) > len(pat)) {
		ctx.Errorf(core.ErrArity, "'%s' expects %d argument(s), got %d", name, required, len(argv))
		return nil, false
	}

	var (
		result  = make([]any, len(argv))
		success = true
	)

	for i := range argv {
		arg := argv[i].Evaluate(ctx)
		result[i] = arg.Val

		if i < required && arg.Kind != pat[i] {
			// Caret the offending argument itself when it's a positioned
			// form (ErrorfAt falls back to the whole call for bare leaves).
			ctx.ErrorfAt(argv[i], core.ErrType,
				"argument '%s' to '%s' at position %d is '%s', but '%s' was expected",
				core.Inspect(argv[i]), name, i, arg.Kind, pat[i])

			success = false
		}
	}

	return result, success
}
