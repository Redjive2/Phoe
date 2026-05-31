package builtins

import (
	"fmt"
	"strings"

	"pho/pkg/core"
)

// global wraps a builtin function as a constant StackEntry suitable for
// installation into the global environment.
func global(fn func(ctx core.Context, argv []core.Node) core.Value) core.StackEntry {
	return core.StackEntry{Val: core.TvFun(fn), IsConstant: true}
}

// asBool extracts a bool from a Value, printing an error and returning
// (false, false) if the value isn't a bool. caller is the builtin name for
// error messages.
func asBool(v core.Value, caller string) (bool, bool) {
	b, ok := v.Val.(bool)
	if !ok {
		fmt.Println("(ERR) expected 'bool', got '" + v.Kind + "' @ 'builtins." + caller + "'.")
		return false, false
	}
	return b, true
}

// asNum extracts a float64 from a Value, printing an error and returning
// (0, false) if the value isn't a num. caller is the builtin name for
// error messages.
func asNum(v core.Value, caller string) (float64, bool) {
	n, ok := v.Val.(float64)
	if !ok {
		fmt.Println("(ERR) expected 'num', got '" + v.Kind + "' @ 'builtins." + caller + "'.")
		return 0, false
	}
	return n, true
}

// tvalEqual is structural equality on Values. Arrays and dicts are compared
// element-wise; scalars use Go's == on the underlying Val. Note: dict keys
// are looked up via Go's == (since they live in a Go map), so dicts whose
// keys are themselves arrays/dicts won't compare reliably.
func tvalEqual(a, b core.Value) bool {
	if a.Kind != b.Kind {
		return false
	}

	switch a.Kind {
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
		return a.Val == b.Val
	}
}

type importRequest struct {
	PackagePath string
	Alias       string
}

// parseImportRequests handles both bare-string and aliased-tuple import args.
// Caller is the builtin name, used in error messages.
func parseImportRequests(ctx core.Context, argv []core.Node, caller string) []importRequest {
	requests := make([]importRequest, 0, len(argv))

	for _, argNode := range argv {
		arg := argNode.Evaluate(ctx)

		// "path/to/lib" -> importRequest{"path/to/lib", "lib"}
		if arg.Kind == core.KindStr {
			parts := strings.Split(arg.Val.(string), "/")
			requests = append(requests, importRequest{arg.Val.(string), parts[len(parts)-1]})
			continue
		}

		// ["path/to/lib" 'alias] -> importRequest{"path/to/lib", "alias"}
		if arg.Kind == core.KindArray {
			argArray := *arg.Val.(*[]core.Value)

			if len(argArray) != 2 || argArray[0].Kind != core.KindStr || argArray[1].Kind != core.KindStr {
				fmt.Println("(ERR): cannot parse invalid aliased import request '" + fmt.Sprint(arg.Val) + "' passed @ 'builtins." + caller + "'")
				continue
			}

			requests = append(requests, importRequest{argArray[0].Val.(string), argArray[1].Val.(string)})
			continue
		}

		fmt.Println("(ERR): cannot parse import request '" + fmt.Sprint(arg.Val) + "' passed @ 'builtins." + caller + "'")
	}

	return requests
}

// ParseArgs validates a positional argument list against a type pattern.
// pat entries are Kind* constants; "..." in the trailing position permits a
// variadic tail. Returns the evaluated argument values and a bool indicating
// whether all positional types matched.
func ParseArgs(ctx core.Context, caller string, pat []string, argv []core.Node) ([]any, bool) {
	var (
		result  = make([]any, len(argv))
		success = true
	)

	for i := range argv {
		arg := argv[i].Evaluate(ctx)

		if i < len(pat) {
			result[i] = arg.Val

			if arg.Kind != pat[i] && pat[i] != "..." {
				fmt.Println(
					"(ERR): arg '" + core.Inspect(argv[i]) + "' at position '" + fmt.Sprint(i) +
						"' is of type '" + arg.Kind + "' but type '" + pat[i] +
						"' was expected @ '" + caller + "'.")

				success = false
			}
		}
	}

	if pat[len(pat)-1] == "..." {
		for i := len(pat) - 1; i < len(argv); i++ {
			result[i] = argv[i].Evaluate(ctx).Val
		}
	}

	return result, success
}
