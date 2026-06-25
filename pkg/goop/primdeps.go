package goop

import (
	"unicode/utf8"

	"pho/pkg/core"
)

// Primitive value operations, exposed to the auto-loaded built-in Pho module
// (pkg/builtins/pho/) as thin bindings — e.g. (method List.Size (self)
// (prim.Size self)). They are methods on stdDependencies, which the stdlib
// (and the built-in module) already reach via goimport "stdDependencies", so
// they need no extra module registration.
//
// Each takes the receiver value with its Kind tag intact (BuildCallArgs passes
// a core.Value parameter through unconverted) and switches on kind. The
// built-in module binds each op only on the kinds it handles, so a wrong kind
// here indicates a misconfigured binding rather than user error — reported via
// hostErr with a benign zero return.
//
// These replace the removed `len` / `keyof` builtins, surfaced as `.Size` /
// `.Keys` (see Doc/PlanV1/ObjectModel.md §4.5 / §8).

// Size returns the element count of a list or dict, or the rune count of a
// string. Backs `.Size`.
func (state *stdDependencies) PrimSize(v core.Value) float64 {
	switch v.Kind {
	case core.KindArray:
		return float64(len(*v.Val.(*[]core.Value)))
	case core.KindDict:
		return float64(len(*v.Val.(*map[core.Value]core.Value)))
	case core.KindStr:
		return float64(utf8.RuneCountInString(v.Val.(string)))
	}
	hostErr("Size: cannot measure a value of kind '%s'", v.Kind)
	return 0
}

// Keys returns a list's indices (0 … Size-1) or a dict's keys. Backs `.Keys`.
//
// INTEGRATION NOTE: the return is a slice of Pho values. The goop return path
// (dot.go's gopackage case → core.TvUnknown) does not yet pass core.Value
// through — TvUnknown needs a one-line passthrough at its top:
//
//	if tv, ok := data.(Tval); ok { return tv }
//
// which also makes the reflect.Slice case wrap each []core.Value element
// correctly. That belongs to the Stage A/B core work (pkg/core/tval.go); until
// it lands, `.Size` is fully live but `.Keys` is pending that single change.
func (state *stdDependencies) PrimKeys(v core.Value) []core.Value {
	switch v.Kind {
	case core.KindArray:
		arr := *v.Val.(*[]core.Value)
		keys := make([]core.Value, len(arr))
		for i := range arr {
			keys[i] = core.TvNum(float64(i))
		}
		return keys
	case core.KindStr:
		str := v.Val.(string)
		keys := make([]core.Value, len(str))
		for i := range str {
			keys[i] = core.TvNum(float64(i))
		}
		return keys
	case core.KindDict:
		dict := *v.Val.(*map[core.Value]core.Value)
		keys := make([]core.Value, 0, len(dict))
		for k := range dict {
			keys = append(keys, k)
		}
		return keys
	}
	hostErr("Keys: cannot take the keys of a value of kind '%s'", v.Kind)
	return nil
}
