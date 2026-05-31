package core

import (
	"fmt"
	"reflect"
)

// Tv* constructors. Pure: each just wraps the data with its Kind tag.

func TvNum(n float64) Tval {
	return Tval{n, KindNum}
}

func TvSlice(tvs []Tval) Tval {
	return Tval{&tvs, KindArray}
}

func TvDict(tvd map[Tval]Tval) Tval {
	return Tval{&tvd, KindDict}
}

func TvStr(s string) Tval {
	return Tval{s, KindStr}
}

func TvChr(c rune) Tval {
	return Tval{c, KindChr}
}

func TvBool(b bool) Tval {
	return Tval{b, KindBool}
}

func TvFun(fun tfun) Tval {
	return Tval{fun, KindFun}
}

func TvInstance(structDataPtr *tstruct, fieldMap map[string]Tval, privilege bool, isConstant bool) Tval {
	instance := tinstance{structDataPtr, fieldMap, privilege}
	return Tval{&instance, KindInstance}
}

func TvMethod(structDataPtr *tstruct, fun tfun) Tval {
	return Tval{tmethod{structDataPtr, fun}, KindMethod}
}

func TvPackage(pkg *tpackage) Tval {
	return Tval{pkg, KindPackage}
}

// TvGoPackage wraps a Go-side module (typically *goop.PhoModule, though core
// stays unaware of that type). The caller is responsible for passing the
// expected pointer; the wrapping `any` is just to keep core/goop decoupled.
func TvGoPackage(goModule any) Tval {
	return Tval{goModule, KindGoPackage}
}

func TvConstructor(structName string, structData *tstruct, constructor tfun) Tval {
	return Tval{tconstructor{structName, structData, constructor}, KindConstructor}
}

// TvUnknown reflectively converts an arbitrary Go value into a tval. Used
// primarily by the Go-interop layer to wrap return values from Go-side calls.
//
// For a Go function value, the returned tval is a tfun whose body evaluates
// the call's argv under the caller's ctx and re-wraps the result.
func TvUnknown(data any) Tval {
	switch data.(type) {
	case nil:
		return TvNil
	case int:
		return TvNum(float64(data.(int)))
	case int8:
		return TvNum(float64(data.(int8)))
	case int16:
		return TvNum(float64(data.(int16)))
	case int32:
		return TvNum(float64(data.(int32)))
	case int64:
		return TvNum(float64(data.(int64)))
	case uint:
		return TvNum(float64(data.(uint)))
	case uint32:
		return TvNum(float64(data.(uint32)))
	case uint64:
		return TvNum(float64(data.(uint64)))
	case float32:
		return TvNum(float64(data.(float32)))
	case float64:
		return TvNum(data.(float64))
	case uintptr:
		return TvNum(float64(data.(uintptr)))
	case string:
		return TvStr(data.(string))
	case byte:
		return TvNum(float64(data.(byte)))
	case bool:
		return TvBool(data.(bool))
	default:
		value := reflect.ValueOf(data)

		switch value.Kind() {
		case reflect.Func:
			return TvFun(func(ctx Context, argv []ttnode) Tval {
				argTVals := make([]Tval, len(argv))
				for i, argNode := range argv {
					argTVals[i] = argNode.Evaluate(ctx)
				}

				return TvUnknown(CallDirect(value, argTVals))
			})
		case reflect.Array:
			array := make([]Tval, value.Len())

			for i := 0; i < value.Len(); i++ {
				array[i] = TvUnknown(value.Index(i).Interface())
			}

			return TvSlice(array)
		case reflect.Slice:
			slice := make([]Tval, value.Len())

			for i := 0; i < value.Len(); i++ {
				slice[i] = TvUnknown(value.Index(i).Interface())
			}

			return TvSlice(slice)
		case reflect.Map:
			var (
				dict = make(map[Tval]Tval, value.Len())
				keys = value.MapKeys()
			)

			for _, key := range keys {
				dict[TvUnknown(key.Interface())] = TvUnknown(value.MapIndex(key).Interface())
			}

			return TvDict(dict)
		}
	}

	fmt.Println("(ERR): Unknown type '" + reflect.TypeOf(data).Kind().String() + "' passed @ 'core.TvUnknown'.")
	return TvNil
}

// CallDirect invokes a reflect.Value-wrapped Go function with the provided
// tval arguments and returns the (single) result as Go any.
func CallDirect(funcData reflect.Value, args []Tval) any {
	argValues := make([]reflect.Value, len(args))

	for i, arg := range args {
		argValues[i] = reflect.ValueOf(arg.Val)
	}

	result := funcData.Call(argValues)

	return result[0].Interface()
}
