package core

import (
	"fmt"
	"os"
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

// TvAtom wraps the interned atom for name. Two TvAtom calls with the same
// name carry the identical *Atom pointer, so they compare equal by identity.
func TvAtom(name string) Tval {
	return Tval{Intern(name), KindAtom}
}

func TvBool(b bool) Tval {
	return Tval{b, KindBool}
}

func TvFun(fun tfun) Tval {
	return Tval{fun, KindFun}
}

// TvProperty wraps a getter/optional-setter pair as a KindProperty value.
// A free-standing property is bound under its name in the env; reading the
// name calls the getter, assigning to it calls the setter.
func TvProperty(getter, setter Tval, hasSetter bool) Tval {
	return Tval{tproperty{Getter: getter, Setter: setter, HasSetter: hasSetter}, KindProperty}
}

// TvMacro wraps a macro's body as a callable, tagged KindMacro so it's
// distinct from a plain function: the evaluator refuses to call it directly
// (a macro must be invoked with the `!` sugar, which lowers to the Macrocall
// builtin), and Macrocall accepts only KindMacro. The wrapped tfun is the
// macro body bound exactly like a fun — it receives the quoted call
// arguments and returns the code Macrocall then resumes.
func TvMacro(fun tfun) Tval {
	return Tval{fun, KindMacro}
}

func TvInstance(structDataPtr *tstruct, fieldMap map[string]Tval, privilege bool) Tval {
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

// TvUnknown reflectively converts an arbitrary Go value into a tval. Used
// primarily by the Go-interop layer to wrap return values from Go-side calls.
//
// For a Go function value, the returned tval is a tfun whose body evaluates
// the call's argv under the caller's ctx and re-wraps the result.
func TvUnknown(data any) Tval {
	// A value that is already a Pho value passes through unchanged — e.g. a
	// goop method that returns core.Value, or []core.Value (handled element by
	// element via the reflect.Slice case below, which re-enters here per
	// element). Without this, the reflect path would try to re-wrap a Tval
	// struct and fail.
	if tv, ok := data.(Tval); ok {
		return tv
	}

	switch data.(type) {
	case nil:
		return TvNil
	case int:
		return TvNum(float64(data.(int)))
	case int8:
		return TvNum(float64(data.(int8)))
	case int16:
		return TvNum(float64(data.(int16)))
	case uint16:
		return TvNum(float64(data.(uint16)))
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

	// A Go value of a kind the interop layer doesn't know how to wrap —
	// a host-boundary conversion failure with no Pho source position, so
	// it goes to stderr in main's `pho:` register rather than through the
	// positioned diagnostic renderer.
	fmt.Fprintf(os.Stderr, "pho: cannot convert Go value of kind '%s' to a Pho value\n", reflect.TypeOf(data).Kind())
	return TvNil
}

// BuildCallArgs converts Pho values into reflect arguments for a call to a
// Go function/method of type fnType. Arity and type mismatches return an
// error instead of letting reflect.Call panic the host:
//
//   - a parameter declared as core.Tval receives the value with its Kind
//     tag intact (Go-side helpers that want to render Pho values use this);
//   - Nil becomes the parameter type's zero value;
//   - Pho nums (float64) convert to any Go numeric parameter type;
//   - variadic tails are filled element by element.
func BuildCallArgs(fnType reflect.Type, args []Tval) ([]reflect.Value, error) {
	numIn := fnType.NumIn()
	if fnType.IsVariadic() {
		if len(args) < numIn-1 {
			return nil, fmt.Errorf("expected at least %d arguments, got %d", numIn-1, len(args))
		}
	} else if len(args) != numIn {
		return nil, fmt.Errorf("expected %d arguments, got %d", numIn, len(args))
	}

	tvalType := reflect.TypeOf(Tval{})

	out := make([]reflect.Value, len(args))
	for i, arg := range args {
		var paramType reflect.Type
		if fnType.IsVariadic() && i >= numIn-1 {
			paramType = fnType.In(numIn - 1).Elem()
		} else {
			paramType = fnType.In(i)
		}

		if paramType == tvalType {
			out[i] = reflect.ValueOf(arg)
			continue
		}

		if arg.Val == nil {
			out[i] = reflect.Zero(paramType)
			continue
		}

		v := reflect.ValueOf(arg.Val)
		switch {
		case v.Type().AssignableTo(paramType):
			out[i] = v
		case isNumericKind(v.Kind()) && isNumericKind(paramType.Kind()):
			out[i] = v.Convert(paramType)
		default:
			return nil, fmt.Errorf("argument %d: cannot use kind '%s' as Go type '%v'", i, arg.Kind, paramType)
		}
	}
	return out, nil
}

func isNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// CallDirect invokes a reflect.Value-wrapped Go function with the provided
// tval arguments and returns the (single) result as Go any. A panic in the
// Go function is contained — surfaced as a host error returning nil rather
// than crashing the interpreter, matching goop.Call.
func CallDirect(funcData reflect.Value, args []Tval) (result any) {
	argValues, err := BuildCallArgs(funcData.Type(), args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pho: %v\n", err)
		return nil
	}

	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "pho: go call panicked: %v\n", r)
			result = nil
		}
	}()

	out := funcData.Call(argValues)
	if len(out) == 0 {
		return nil
	}
	return out[0].Interface()
}
