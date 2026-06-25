package builtins

import (
	"fmt"
	"math"

	"pho/pkg/core"
	"pho/pkg/goop"
)

// dotBuiltins returns a single entry: the mangled core.Dot accessor.
//
// The `a.b` surface syntax is rewritten by the parser into (core.Dot a b) where
// `core.Dot` is the randomized internal name from mangle.go. This builtin
// dispatches on the kind of the left-hand-side:
//
//	dict     — key lookup
//	array    — integer index, or [a:b] / [:b] / [a:] / [:] slice forms
//	str      — index into rune, or the same slice forms
//	instance — field access (with privacy check) or method dispatch
//	package  — uppercase export lookup
//	gopackage— Go-side method binding (returned as a wrapper core.Fun)
//	num      — fractional-decimal hack (e.g. 12 . 5 -> 12.5)
func dotBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		core.Dot: global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "the dot accessor requires exactly 2 operands; got %d", len(argv))
			}

			col := argv[0].Evaluate(ctx)

			// A bare-identifier RHS is a member access (method/property) on the
			// value, resolved import-scoped (see core/member.go). Kinds with their
			// own identifier-RHS handling — struct instances (fields), packages —
			// fall through to the switch. A number keeps the fractional-decimal
			// hack only for a NUMERIC RHS; an identifier RHS (e.g. 1.Double) is a
			// member access.
			if name, ok := memberName(argv[1]); ok && wantsMemberLookup(col.Kind) {
				return memberAccess(ctx, col, name)
			}

			switch col.Kind {
			case core.KindDict:
				dict := *col.Val.(*map[core.Value]core.Value)

				br, ok := asBracket(ctx, argv[1])
				if !ok {
					return core.TvNil
				}
				if isSliceForm(br) {
					return ctx.Errorf(core.ErrBadForm, "cannot slice a dict; use a single key 'coll.[key]'")
				}
				keyNode, ok := singleIndex(ctx, br)
				if !ok {
					return core.TvNil
				}

				key := keyNode.Evaluate(ctx)
				if !scalarKey(ctx, key, "internal.dot") {
					return core.TvNil
				}

				val, found := dict[key]
				if found {
					return val
				}

				return core.TvNil
			case core.KindArray:
				array := *col.Val.(*[]core.Value)

				br, ok := asBracket(ctx, argv[1])
				if !ok {
					return core.TvNil
				}

				if isSliceForm(br) {
					lhs, rhs, ok := sliceBounds(ctx, br, len(array))
					if !ok {
						return core.TvNil
					}

					// Copy the view so writing through the slice (= s.[i] v)
					// doesn't mutate the parent's backing array — matching
					// append/drop's copy semantics.
					return core.TvSlice(append([]core.Value{}, array[lhs:rhs]...))
				}

				idxNode, ok := singleIndex(ctx, br)
				if !ok {
					return core.TvNil
				}

				idx, ok := asNum(ctx, idxNode.Evaluate(ctx), "internal.dot")
				if !ok {
					return core.TvNil
				}

				if i, ok := numIndex(idx, len(array)); ok {
					return array[i]
				}

				return core.TvNil
			case core.KindStr:
				str := col.Val.(string)

				br, ok := asBracket(ctx, argv[1])
				if !ok {
					return core.TvNil
				}

				// Strings index and slice by rune (see strLen/strRuneAt),
				// consistent with `for` over a string.
				if isSliceForm(br) {
					lhs, rhs, ok := sliceBounds(ctx, br, strLen(str))
					if !ok {
						return core.TvNil
					}

					return core.TvStr(strRuneSlice(str, lhs, rhs))
				}

				idxNode, ok := singleIndex(ctx, br)
				if !ok {
					return core.TvNil
				}

				idx, ok := asNum(ctx, idxNode.Evaluate(ctx), "internal.dot")
				if !ok {
					return core.TvNil
				}

				if i, ok := numIndex(idx, strLen(str)); ok {
					if r, rok := strRuneAt(str, i); rok {
						return core.TvChr(r)
					}
				}

				return core.TvNil
			case core.KindInstance:
				inst := col.Val.(*core.Instance)

				if lf, ok := core.AsLeaf(argv[1]); ok {
					ident := string(lf)

					if !core.IsIdent(ident) {
						return ctx.Errorf(core.ErrField, "cannot index struct instance with non-identifier key '%s'", ident)
					}

					// `#`-prefixed fields and methods are private: visible only
					// while one of the instance's own methods is running.
					if ident[0] == '#' && !inst.Privileged {
						return ctx.Errorf(core.ErrField, "cannot index struct instance with private key '%s'", ident)
					}

					if val, found := inst.Fields[ident]; found {
						return val
					}

					// A computed field (property): read through its getter, an
					// anonymous method. Push the instance as self, then call the
					// zero-arg getter; the defer pops self after it returns.
					if prop, found := inst.Struct.Properties[ident]; found {
						env := ctx.Env
						env.InstStack = append([]core.Value{col}, env.InstStack...)
						defer func() { env.InstStack = env.InstStack[1:] }()
						return prop.Getter.Val.(core.Fun)(ctx, nil)
					}

					method, found := inst.Struct.Methods[ident]
					if !found {
						// Not an own field/property/method — fall back to the
						// import-scoped extension + universal (Unknown) member
						// tables, so a struct instance can use universal methods
						// like `inst.Is?` exactly as any other value does.
						return memberAccess(ctx, col, ident)
					}

					return core.TvFun(func(ctx core.Context, argv []core.Node) core.Value {
						env := ctx.Env
						env.InstStack = append([]core.Value{col}, env.InstStack...)
						defer func() {
							env.InstStack = env.InstStack[1:]
						}()
						return method(ctx, argv)
					})
				}

				return ctx.Errorf(core.ErrField, "structs are accessed by field name: write 'x.field', not 'x.%s'", core.Inspect(argv[1]))

			case core.KindPackage:
				// (math.Add 1 2) -> ((read math Add) 1 2)
				pkg := col.Val.(*core.Package)

				member, ok := core.AsLeaf(argv[1])
				if !ok {
					return ctx.Errorf(core.ErrField, "package accessors must be unqualified identifiers: expected identifier, got call '%s'", core.Inspect(argv[1]))
				}

				if val, found := pkg.Exports[string(member)]; found {
					if val.Kind == core.KindFun {
						return core.TvFun(val.Val.(core.Fun))
					}

					// A struct-type export is a KindType value; it falls through
					// to the live-binding path below and is returned as-is, so an
					// importer can use it as a type AND construct through it
					// (`pkg.Reader.{ … }` — eval.go's call path constructs a
					// KindType via its registered constructor).

					// A non-callable export — an exported var, const, or
					// free-standing property. Read the LIVE binding from the
					// package's own top frame so an importer sees the current
					// value even after the module mutates its own var
					// internally; fall back to the value captured at export
					// time. An exported property delegates to its getter (run
					// in the module's scope, where its captured names live), so
					// `pkg.Prop` yields the computed value, not the delegate.
					if len(pkg.Env.Stack) > 0 {
						if live, ok := pkg.Env.Stack[0][string(member)]; ok {
							if live.Val.Kind == core.KindProperty {
								return core.ReadProperty(ctx, live.Val)
							}
							return live.Val
						}
					}
					if val.Kind == core.KindProperty {
						return core.ReadProperty(ctx, val)
					}
					return val
				}

				return ctx.Errorf(core.ErrField, "package '%s' has no exported member '%s'", pkg.Path, string(member))
			case core.KindGoPackage:
				// (math.Add 1 2) -> ((read math Add) 1 2)
				gopkg := col.Val.(*goop.PhoModule)

				member, ok := core.AsLeaf(argv[1])
				if !ok {
					return ctx.Errorf(core.ErrField, "go package accessors must be unqualified identifiers: expected identifier, got call '%s'", core.Inspect(argv[1]))
				}

				funcName := string(member)

				return core.TvFun(func(ctx core.Context, callArgv []core.Node) core.Value {
					args, ok := core.DistributeSpreadExpressions(ctx, callArgv)
					if !ok {
						return core.TvNil
					}

					ctx.PushCallFrame("go:" + gopkg.Name + "." + funcName)
					defer ctx.PopCallFrame()
					res, err := goop.Call(gopkg, funcName, args)
					if err != nil {
						return ctx.Errorf(core.ErrGoCall, "%s", err.Error())
					}
					return core.TvUnknown(res)
				})
			case core.KindNum:
				rhs := argv[1].Evaluate(ctx)

				if rhs.Kind != core.KindNum {
					return ctx.Errorf(core.ErrType, "cannot apply '.' to a number and a value of kind '%s'", rhs.Kind)
				}

				// The lexer splits `1.05` into `1` `.` `05`, so the
				// fractional digit count must come from the literal token
				// text — evaluating `05` to 5 loses the leading zero.
				digits := 0
				if lf, ok := core.AsLeaf(argv[1]); ok {
					digits = len(lf)
				} else {
					digits = len(fmt.Sprint(rhs.Val.(float64)))
				}

				var (
					lhs     = col.Val.(float64)
					decimal = rhs.Val.(float64) / math.Pow(10, float64(digits))
				)

				// For a negative integer-part like `-5.5` (or `-0.5`, where
				// lhs is negative zero), the fractional part subtracts.
				if math.Signbit(lhs) {
					return core.TvNum(lhs - decimal)
				}
				return core.TvNum(lhs + decimal)
			}

			return ctx.Errorf(core.ErrType, "cannot index a value of type '%s'", col.Kind)
		}),
	}
}

// asBracket returns the (slice …) branch that lowering produces for the
// `coll.[…]` bracket form. Dynamic indexing must use brackets, so a bare
// RHS (`coll.name`, the field-access shape) is rejected with a diagnostic
// that shows the intended `coll.[name]` rewrite.
func asBracket(ctx core.Context, rhs core.Node) (core.Branch, bool) {
	br, ok := core.AsBranch(rhs)
	if !ok || len(br) == 0 || br[0] != core.Leaf(core.Slice) {
		ctx.Errorf(core.ErrField, "index a collection with brackets: write 'coll.[%s]', not 'coll.%s'", core.Inspect(rhs), core.Inspect(rhs))
		return nil, false
	}
	return br, true
}

// isSliceForm reports whether a bracket branch uses the colon slice syntax
// (`[:b]`, `[a:]`, `[a:b]`, `[:]`) rather than a single index `[i]`. The
// colon only ever appears as a top-level leaf in slice syntax.
func isSliceForm(br core.Branch) bool {
	for _, n := range br[1:] {
		if lf, ok := core.AsLeaf(n); ok && lf == core.Leaf(":") {
			return true
		}
	}
	return false
}

// singleIndex returns the lone index/key expression of a non-slice bracket
// `coll.[i]`. The caller has already established (via isSliceForm) that br
// is not a colon slice form.
func singleIndex(ctx core.Context, br core.Branch) (core.Node, bool) {
	if len(br) != 2 {
		ctx.Errorf(core.ErrBadForm, "'coll.[i]' takes exactly one index expression")
		return nil, false
	}
	return br[1], true
}

// assignIndex extracts the single index/key expression from the bracket of
// a dot-index assignment target `(= coll.[i] v)`. It rejects field syntax
// (a bare RHS) and slice forms — there is no assigning to a slice window.
func assignIndex(ctx core.Context, rhs core.Node) (core.Node, bool) {
	br, ok := asBracket(ctx, rhs)
	if !ok {
		return nil, false
	}
	if isSliceForm(br) {
		ctx.Errorf(core.ErrBadAssign, "cannot assign to a slice; assign to a single index 'coll.[i]'")
		return nil, false
	}
	return singleIndex(ctx, br)
}

// sliceBounds evaluates a `.[a : b]` slice form against a collection of the
// given length, supporting the [:b], [a:b], [a:], and [:] shapes. Returns
// ok=false (after reporting a diagnostic) for malformed syntax, non-numeric
// bounds, or an out-of-range/inverted window.
func sliceBounds(ctx core.Context, br core.Branch, length int) (int, int, bool) {
	var (
		lhs int
		rhs int
	)

	evalBound := func(node core.Node) (int, bool) {
		n, ok := asNum(ctx, node.Evaluate(ctx), "internal.dot.slice")
		if !ok {
			return 0, false
		}
		// int(NaN)/int(±Inf) silently become 0 and would pass the range check
		// below, slicing from a bogus bound; reject a non-finite bound instead.
		if math.IsNaN(n) || math.IsInf(n, 0) {
			ctx.Errorf(core.ErrIndexRange, "slice bound must be a finite number, got %v", n)
			return 0, false
		}
		return int(n), true
	}

	switch {
	// myList.[: b]
	case len(br) == 3 && br[1] == core.Leaf(":"):
		b, ok := evalBound(br[2])
		if !ok {
			return 0, 0, false
		}
		lhs, rhs = 0, b
	// myList.[a : b]
	case len(br) == 4 && br[2] == core.Leaf(":"):
		a, ok1 := evalBound(br[1])
		b, ok2 := evalBound(br[3])
		if !ok1 || !ok2 {
			return 0, 0, false
		}
		lhs, rhs = a, b
	// myList.[a :]
	case len(br) == 3 && br[2] == core.Leaf(":"):
		a, ok := evalBound(br[1])
		if !ok {
			return 0, 0, false
		}
		lhs, rhs = a, length
	// myList.[:]
	case len(br) == 2 && br[1] == core.Leaf(":"):
		lhs, rhs = 0, length
	default:
		ctx.Errorf(core.ErrBadForm, "invalid slicing syntax")
		return 0, 0, false
	}

	if lhs < 0 || rhs > length || lhs > rhs {
		ctx.Errorf(core.ErrIndexRange, "slice bounds [%d : %d] out of range for length %d", lhs, rhs, length)
		return 0, 0, false
	}

	return lhs, rhs, true
}

// memberName returns the identifier text of a bare-identifier RHS (a member
// access like `x.Size`), or ok=false for a bracket form `x.[i]` or a numeric
// RHS (the fractional-decimal hack on a number).
func memberName(node core.Node) (string, bool) {
	lf, ok := core.AsLeaf(node)
	if !ok {
		return "", false
	}
	s := string(lf)
	if !core.IsIdent(s) {
		return "", false
	}
	return s, true
}

// wantsMemberLookup reports whether a value of the given kind resolves a
// bare-identifier RHS as a method/property member. Struct instances, packages,
// and go-packages keep their bespoke identifier handling in the dot switch.
func wantsMemberLookup(kind string) bool {
	switch kind {
	case core.KindNum, core.KindStr, core.KindArray, core.KindDict,
		core.KindBool, core.KindChr, core.KindAtom, core.KindFun,
		core.KindNil, core.KindType, core.KindMacro, core.KindMethod:
		return true
	}
	return false
}

// memberAccess resolves a method/property `name` on col through the import
// scope and returns the property value (read via its getter) or the bound
// method — a Fun that pushes col as the receiver on the instance stack at call
// time, exactly like the struct-instance method wrapper.
func memberAccess(ctx core.Context, col core.Value, name string) core.Value {
	// A struct TYPE value resolves STATIC members (declared via `static
	// method`/`static property`) from the struct itself — keyed per struct, not
	// through the shared prim:type extension table every type value would share.
	// The type value is pushed as the receiver, so the member's `Self` is it.
	if col.Kind == core.KindType {
		if sdata, isStruct := core.StructOf(col.Val.(*core.PhoType)); isStruct {
			if prop, found := sdata.StaticProperties[name]; found {
				env := ctx.Env
				env.InstStack = append([]core.Value{col}, env.InstStack...)
				defer func() { env.InstStack = env.InstStack[1:] }()
				return prop.Getter.Val.(core.Fun)(ctx, nil)
			}
			if method, found := sdata.StaticMethods[name]; found {
				return core.TvFun(func(ctx core.Context, argv []core.Node) core.Value {
					env := ctx.Env
					env.InstStack = append([]core.Value{col}, env.InstStack...)
					defer func() { env.InstStack = env.InstStack[1:] }()
					return method(ctx, argv)
				})
			}
		}
	}

	res := ctx.ResolveMember(core.TypeKeyOf(col), name)
	if res.Clash {
		return ctx.Errorf(core.ErrField, "member '%s' on type '%s' is ambiguous: more than one module in scope defines it", name, core.TvTypeOf(col).Name())
	}
	if !res.Found {
		// Auto-inject a Trait default: if some registered trait that col
		// satisfies provides a default for this member, dispatch it (col as
		// self). Two satisfied traits defaulting the same name is ambiguous.
		if fn, isProp, found, clash := core.TraitDefaultMember(ctx, col, name); found {
			if clash {
				return ctx.Errorf(core.ErrField, "member '%s' on type '%s' is ambiguous: two traits provide a default", name, core.TvTypeOf(col).Name())
			}
			if isProp {
				env := ctx.Env
				env.InstStack = append([]core.Value{col}, env.InstStack...)
				defer func() { env.InstStack = env.InstStack[1:] }()
				return fn(ctx, nil)
			}
			return core.TvFun(func(ctx core.Context, argv []core.Node) core.Value {
				env := ctx.Env
				env.InstStack = append([]core.Value{col}, env.InstStack...)
				defer func() { env.InstStack = env.InstStack[1:] }()
				return fn(ctx, argv)
			})
		}
		return ctx.Errorf(core.ErrField, "'%s' is not defined on type '%s'", name, core.TvTypeOf(col).Name())
	}

	if res.IsProperty {
		env := ctx.Env
		env.InstStack = append([]core.Value{col}, env.InstStack...)
		defer func() { env.InstStack = env.InstStack[1:] }()
		return res.Property.Getter.Val.(core.Fun)(ctx, nil)
	}

	method := res.Method
	return core.TvFun(func(ctx core.Context, argv []core.Node) core.Value {
		env := ctx.Env
		env.InstStack = append([]core.Value{col}, env.InstStack...)
		defer func() {
			env.InstStack = env.InstStack[1:]
		}()
		return method(ctx, argv)
	})
}
