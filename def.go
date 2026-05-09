package main

import (
	"fmt"
	"reflect"
	"unicode"
)

/////////////////////////////
//    TYPE DECLARATIONS    //
/////////////////////////////

type tval struct {
	Val        any
	Kind       string
	SourcePkg  *tpackage
	SourceFile *tfile
}

type tStackEntry struct {
	Val        tval
	IsConstant bool
}

type tfun func([]ttnode) tval

type ttnode interface {
	Evaluate() tval
}

type ttbranch []ttnode

type ttleaf string

type tcontext struct {
	Captures      map[string]func(tval, bool) tval
	MaxStackDepth int
}

type tenv struct {
	Globals  *map[string]tStackEntry
	Stack    []map[string]tStackEntry
	CtxStack []tcontext
	Structs  map[string]*tstruct // Maps the address of the 'new' function returned by 'struct'
	// to the struct's underlying representation. Used to add methods.
	InstStack     []tval // all 'tinstance's
	ImportContext *[]tpackage
}

type tpackage struct {
	Environment tenv
	Files map[string]tfile
}

type tfile struct {
	FileName    string
	Imports  map[string]tpackage
	Tree        ttnode
	Exports     map[string]tval
}

type tstruct struct {
	Fields  []string
	Methods map[string]tfun
	Origin  *tenv
}

type tinstance struct {
	Struct     *tstruct
	Fields     map[string]tval
	Privileged bool
}

type tmethod struct {
	Struct *tstruct
	Fun    tfun
}

type tconstructor struct {
	StructName  string
	StructData  *tstruct
	Constructor tfun
}

////////////////////////
//    DECLARATIONS    //
////////////////////////

const (
	KindNum         = "num"
	KindArray       = "array"
	KindDict        = "dict"
	KindStr         = "str"
	KindChr         = "chr"
	KindBool        = "bool"
	KindNil         = "nil"
	KindFun         = "fun"
	KindInstance    = "instance"
	KindMethod      = "method"
	KindPackage     = "package"
	KindGoPackage   = "gopackage"
	KindConstructor = "constructor"
)

var (
	TvNil = tval{nil, KindNil, "global"}
)

func TvNum(n float64) tval {
	return tval{n, KindNum, activePackage, activeFile}
}

func TvSlice(tvs []tval) tval {
	return tval{&tvs, KindArray, activePackage, activeFile}
}

func TvDict(tvd map[tval]tval) tval {
	return tval{&tvd, KindDict, activePackage, activeFile}
}

func TvStr(s string) tval {
	return tval{s, KindStr, activePackage, activeFile}
}

func TvChr(c rune) tval {
	return tval{c, KindChr, activePackage, activeFile}
}

func TvBool(b bool) tval {
	return tval{b, KindBool, activePackage, activeFile}
}

func TvFun(fun tfun) tval {
	return tval{fun, KindFun, activePackage, activeFile}
}

func TvInstance(structDataPtr *tstruct, fieldMap map[string]tval, privilege bool, isConstant bool) tval {
	instance := tinstance{structDataPtr, fieldMap, privilege}

	return tval{
		&instance,
		KindInstance,
		activePackage,
		activeFile,
	}
}

func TvMethod(structDataPtr *tstruct, fun tfun) tval {
	return tval{
		tmethod{structDataPtr, fun},
		KindMethod,
		activePackage,
		activeFile,
	}
}

func TvPackage(sources [][]byte, names []string) tval {
	env := NewEnv()

	prevEnv := activeEnv
	activeEnv = &env

	PushFrame()

	files := make([]tfile, len(sources))
	for i, source := range sources {
		tree := Parse(Lex(string(source)))

		files[i] = tfile{
			FileName: names[i],
			Tree: tree,
			Imports: []tpackage{},
			Exports: map[string]tval{},
		}

		activeEnv.ImportContext = &files[i].Imports
	}

	exports := make(map[string]tval)
	for name, v := range activeEnv.Stack[0] {
		value := v.Val

		if !unicode.IsUpper(rune(name[0])) {
			continue
		}

		if value.Kind != KindFun && value.Kind != KindMethod && value.Kind != KindConstructor {
			fmt.Println("(ERR): Cannot export symbol '" + name + "' of type '" + value.Kind + "'; only functions may be exported @ internal 'TvPackage'.")
			continue
		}

		exports[name] = value
	}

	activeEnv = prevEnv

	pkg := tpackage{
		Files: files
	}

	return tval{pkg, KindPackage, activePackage, activeFile}
}

func TvGoPackage(goModule *LithpModule) tval {
	return tval{goModule, KindGoPackage, activePackage, activeFile}
}

func TvConstructor(structName string, structData *tstruct, constructor tfun) tval {
	return tval{tconstructor{structName, structData, constructor}, KindConstructor, activePackage, activeFile}
}

func TvUnknown(data any) tval {
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
			return TvFun(func(argv []ttnode) tval {
				argTVals := make([]tval, len(argv))
				for i, argNode := range argv {
					argTVals[i] = argNode.Evaluate()
				}

				return TvUnknown(CallDirect(value, argTVals))
			})
		case reflect.Array:
			array := make([]tval, value.Len())

			for i := 0; i < value.Len(); i++ {
				array[i] = TvUnknown(value.Index(i).Interface())
			}

			return TvSlice(array)
		case reflect.Slice:
			slice := make([]tval, value.Len())

			for i := 0; i < value.Len(); i++ {
				slice[i] = TvUnknown(value.Index(i).Interface())
			}

			return TvSlice(slice)
		case reflect.Map:
			var (
				dict = make(map[tval]tval, value.Len())
				keys = value.MapKeys()
			)

			for _, key := range keys {
				dict[TvUnknown(key.Interface())] = TvUnknown(value.MapIndex(key).Interface())
			}

			return TvDict(dict)
		}
	}

	fmt.Println("(ERR): Unknown type '" + reflect.TypeOf(data).Kind().String() + "' passed @ 'internal/def.go:TvUnknown'.")
	return TvNil
}
