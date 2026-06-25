package builtins

import (
	"fmt"
	"slices"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// parseArgList converts a parameter list into the []string shape
// BindFun/BindMethod expect: plain names, with optional parameters encoded
// "?name" and a trailing rest-arg encoded "#name". The grammar is
//
//	required* (optional name)* (spread name)?
//
// so optionals come after all required params and before any trailing
// spread; a required (plain) parameter after an optional is rejected.
// Returns ok=false (after reporting a diagnostic through ctx) on any
// malformed pattern.
func parseArgList(ctx core.Context, node core.Node, caller string) ([]string, bool) {
	argBranch, ok := core.AsBranch(node)
	if !ok {
		ctx.Errorf(core.ErrBadForm, "'%s' expected an argument list like (a b), got '%s'", caller, core.Inspect(node))
		return nil, false
	}

	argList := make([]string, len(argBranch))
	sawOptional := false

	for i, arg := range argBranch {
		if branch, ok := core.AsBranch(arg); ok {
			// (spread name) — rest-arg, last position only.
			if i == len(argBranch)-1 &&
				len(branch) == 2 &&
				branch[0] == core.Leaf("spread") {

				leaf, ok := core.AsLeaf(branch[1])
				if !ok {
					ctx.Errorf(core.ErrBadForm, "'%s' cannot parse rest-argument pattern '%s'", caller, core.Inspect(branch))
					return nil, false
				}
				argList[i] = fmt.Sprint("#", leaf)
				break
			}

			// (optional name) — omittable parameter; defaults to Nil.
			if len(branch) == 2 && branch[0] == core.Leaf("optional") {
				leaf, ok := core.AsLeaf(branch[1])
				if !ok {
					ctx.Errorf(core.ErrBadForm, "'%s' cannot parse optional-argument pattern '%s'", caller, core.Inspect(branch))
					return nil, false
				}
				argList[i] = fmt.Sprint("?", leaf)
				sawOptional = true
				continue
			}

			ctx.Errorf(core.ErrBadForm, "'%s' cannot parse argument pattern '%s'", caller, core.Inspect(arg))
			return nil, false
		}

		leaf, _ := core.AsLeaf(arg)
		if sawOptional {
			ctx.Errorf(core.ErrBadForm, "'%s' required parameter '%s' cannot follow an optional parameter", caller, leaf)
			return nil, false
		}
		argList[i] = string(leaf)
	}

	return argList, true
}

// declName reads a literal declaration name from a bare identifier node.
// Post-cutover a declaration name is a plain leaf — `(fun add …)`, never
// `(fun 'add …)` and never a dynamic expression — so a non-leaf is an error.
func declName(ctx core.Context, node core.Node, caller, what string) (string, bool) {
	leaf, ok := core.AsLeaf(node)
	if !ok {
		ctx.Errorf(core.ErrBadForm, "'%s' %s must be a bare identifier; got '%s'", caller, what, core.Inspect(node))
		return "", false
	}
	return string(leaf), true
}

// declBindName reads a var/const binding name. It accepts a bare identifier
// `x` or the typed form `(Type x)` — a two-element branch whose second child is
// the name. The declared Type is ERASED at runtime (Phase 1 of the inline
// type-signature plan): the interpreter just binds the name; the type is read
// by the gradual checker, not evaluated here.
func declBindName(ctx core.Context, node core.Node, caller string) (string, bool) {
	if br, isBranch := core.AsBranch(node); isBranch {
		if len(br) == 2 {
			if leaf, ok := core.AsLeaf(br[1]); ok {
				return string(leaf), true
			}
		}
		ctx.Errorf(core.ErrBadForm, "'%s' name must be a bare identifier or a typed '(Type name)' form; got '%s'", caller, core.Inspect(node))
		return "", false
	}
	return declName(ctx, node, caller, "name")
}

// methodTarget destructures the first argument of a method declaration — a
// PATTERN, not code. The NAMED form `Receiver.Name` lowers to (Dot Receiver
// Name): named=true, with the receiver node and the bare method name. The
// ANONYMOUS form is a bare `Receiver` leaf: named=false, name "". Either way
// the receiver node is returned for the caller to evaluate to a struct; the
// dot is never evaluated as a member access.
func methodTarget(ctx core.Context, node core.Node) (recv core.Node, name string, named, ok bool) {
	if br, isBranch := core.AsBranch(node); isBranch {
		if len(br) == 3 {
			if head, isLeaf := core.AsLeaf(br[0]); isLeaf && string(head) == core.Dot {
				nameLeaf, isLeaf := core.AsLeaf(br[2])
				if !isLeaf {
					ctx.Errorf(core.ErrBadForm, "the method name after the dot must be a bare identifier")
					return nil, "", false, false
				}
				return br[1], string(nameLeaf), true, true
			}
		}
		ctx.Errorf(core.ErrBadForm, "'method' wants a 'Receiver.Name' pattern or a bare 'Receiver' as its first argument")
		return nil, "", false, false
	}
	if _, isLeaf := core.AsLeaf(node); isLeaf {
		return node, "", false, true // anonymous: bare receiver leaf
	}
	ctx.Errorf(core.ErrBadForm, "'method' wants a 'Receiver.Name' pattern or a bare 'Receiver' as its first argument")
	return nil, "", false, false
}

// isExportedMember reports whether a member name is exported (capitalized) and
// therefore visible to importers of the declaring module.
func isExportedMember(name string) bool {
	return len(name) > 0 && name[0] != '#'
}

// structDeclShape reads a struct declaration's name and field names, accepting
// both forms:
//
//	(struct Name f0 f1 …)            — bare untyped fields
//	(struct Name.{ F0 T0 F1 T1 … })  — typed fields
//
// The typed form reaches the runtime as a single branch argument
// `(Name "F0" T0 "F1" T1 …)` (the `.{}` sugar quotes the field names and leaves
// the types as ordinary expressions). Field TYPES are static-only — at runtime
// a struct is just its field names — so they are read past here; the linter's
// checker consumes them.
func structDeclShape(ctx core.Context, argv []core.Node) (name string, fields []string, ok bool) {
	if len(argv) == 1 {
		if br, isBranch := core.AsBranch(argv[0]); isBranch {
			head, isLeaf := core.AsLeaf(br[0])
			if !isLeaf {
				ctx.Errorf(core.ErrBadForm, "'struct' name must be a bare identifier; got '%s'", core.Inspect(br[0]))
				return "", nil, false
			}
			for i := 1; i < len(br); i += 2 {
				fv := br[i].Evaluate(ctx)
				fn, isStr := fv.Val.(string)
				if !isStr {
					ctx.Errorf(core.ErrBadForm, "struct field name must be a string, got kind '%s'", fv.Kind)
					return "", nil, false
				}
				fields = append(fields, fn)
			}
			return string(head), fields, true
		}
	}
	name, isName := declName(ctx, argv[0], "struct", "name")
	if !isName {
		return "", nil, false
	}
	for _, fieldNode := range argv[1:] {
		fl, isLeaf := core.AsLeaf(fieldNode)
		if !isLeaf {
			ctx.Errorf(core.ErrBadForm, "struct field names must be identifiers, got '%s'", core.Inspect(fieldNode))
			return "", nil, false
		}
		fields = append(fields, string(fl))
	}
	return name, fields, true
}

// staticReceiver resolves a `static method`/`static property` receiver node to
// the struct it names. Statics live on STRUCT types only (a primitive carries
// its members through the import-scoped extension table). The static tables are
// lazily initialized so a struct built before this change still works.
func staticReceiver(ctx core.Context, recv core.Node, caller string) (*core.Struct, *core.PhoType, bool) {
	recvVal := recv.Evaluate(ctx)
	if recvVal.Kind != core.KindType {
		ctx.Errorf(core.ErrType, "'%s' receiver must be a struct type, got kind '%s'", caller, recvVal.Kind)
		return nil, nil, false
	}
	recvType := recvVal.Val.(*core.PhoType)
	sdata, isStruct := core.StructOf(recvType)
	if !isStruct {
		ctx.Errorf(core.ErrType, "'%s' receiver must be a struct, got '%s'", caller, recvType.Name())
		return nil, nil, false
	}
	if sdata.StaticMethods == nil {
		sdata.StaticMethods = map[string]core.Fun{}
	}
	if sdata.StaticProperties == nil {
		sdata.StaticProperties = map[string]core.Property{}
	}
	return sdata, recvType, true
}

// staticMethod declares a type-level method `(static method Recv.Name (args)
// body)` — callable as `Recv.Name args` on the TYPE itself. `Self` in the body
// is the receiver type value, bound (like an instance method's self) from the
// instance stack the dot access pushes, so `Self.{ … }` constructs an instance.
func staticMethod(ctx core.Context, argv []core.Node) core.Value {
	if len(argv) != 3 {
		return ctx.Errorf(core.ErrArity, "'static method' requires Receiver.Name, args, body; got %d", len(argv))
	}
	recv, name, named, ok := methodTarget(ctx, argv[0])
	if !ok {
		return core.TvNil
	}
	if !named {
		return ctx.Errorf(core.ErrBadForm, "'static method' needs a 'Receiver.Name' pattern")
	}
	sdata, recvType, ok := staticReceiver(ctx, recv, "static method")
	if !ok {
		return core.TvNil
	}
	params, ok := parseArgList(ctx, argv[1], "static method")
	if !ok {
		return core.TvNil
	}
	// Prepend `self` as the receiver parameter — bound from the instance stack
	// (the dot access pushes the type value), so `self` in the body is the type.
	argList := append([]string{"self"}, params...)
	sdata.StaticMethods[name] = core.BindMethod(recvType.Name()+"."+name, argv[2], argList, ctx)
	return core.TvNil
}

// staticProperty declares a type-level property `(static property Recv.Name get
// getter [set setter])` — read (and optionally written) through the TYPE value,
// the getter/setter receiving the type as their receiver.
func staticProperty(ctx core.Context, argv []core.Node) core.Value {
	if len(argv) != 3 && len(argv) != 5 {
		return ctx.Errorf(core.ErrArity, "'static property' takes (Name get getter) or (Name get getter set setter); got %d", len(argv))
	}
	if kw, ok := core.AsLeaf(argv[1]); !ok || string(kw) != "get" {
		return ctx.Errorf(core.ErrBadForm, "'static property' expects the keyword 'get' before the getter")
	}
	getter := argv[2].Evaluate(ctx)
	if getter.Kind != core.KindFun {
		return ctx.Errorf(core.ErrType, "static property getter must be a function or anonymous method, got kind '%s'", getter.Kind)
	}
	var setter core.Value
	hasSetter := false
	if len(argv) == 5 {
		if kw, ok := core.AsLeaf(argv[3]); !ok || string(kw) != "set" {
			return ctx.Errorf(core.ErrBadForm, "'static property' expects the keyword 'set' before the setter")
		}
		setter = argv[4].Evaluate(ctx)
		if setter.Kind != core.KindFun {
			return ctx.Errorf(core.ErrType, "static property setter must be a function or anonymous method, got kind '%s'", setter.Kind)
		}
		hasSetter = true
	}
	recv, name, named, ok := methodTarget(ctx, argv[0])
	if !ok {
		return core.TvNil
	}
	if !named {
		return ctx.Errorf(core.ErrBadForm, "'static property' needs a 'Receiver.Name' pattern")
	}
	sdata, _, ok := staticReceiver(ctx, recv, "static property")
	if !ok {
		return core.TvNil
	}
	sdata.StaticProperties[name] = core.Property{Getter: getter, Setter: setter, HasSetter: hasSetter}
	return core.TvNil
}

// typeConnectives are the heads of the parenthesized type-FORMS — the only
// `(…)` shapes read as a type. A `(…)` with any other (capitalized) head is a
// call/construction, e.g. `(Helper)` or `(Point.{ … })`, NOT a type.
var typeConnectives = map[string]bool{
	"Or": true, "And": true, "Not": true, "Diff": true,
	"List": true, "Map": true, "Fun": true, "Struct": true, "Trait": true,
}

// isTypeNode reports whether node reads as a TYPE expression rather than a
// name/value: a Capitalized leaf (Number/Self/a struct name) or a type-form
// `(Or …)`/`(List …)` (a `(…)` headed by a type connective). Lowercase leaves,
// lowercase-headed forms (`(spread x)`/`(+ a b)`), and capitalized CALLS
// (`(Helper)`) are not types. This is the casing heuristic that tells a
// fun/method SIGNATURE from its IMPLEMENTATION (Doc/PlanV1/TypeSignatures.md §3).
func isTypeNode(node core.Node) bool {
	if leaf, ok := core.AsLeaf(node); ok {
		s := string(leaf)
		if s == "Nil" || s == "True" || s == "False" {
			return false // capitalized VALUE literals, not types (the nil TYPE is NilT)
		}
		return s != "" && s[0] >= 'A' && s[0] <= 'Z'
	}
	if br, ok := core.AsBranch(node); ok && len(br) >= 1 {
		if head, ok := core.AsLeaf(br[0]); ok {
			return typeConnectives[string(head)]
		}
	}
	return false
}

// isFunSig reports whether `(params) ret` is a function/method type SIGNATURE:
// every element of the parenthesized param list is a type node and the return
// slot is too (an empty param list counts — the 0-arg case, §3). Signatures are
// recognized and runtime-erased in Phase 1; the gradual checker reads them in
// Phase 3.
func isFunSig(params, ret core.Node) bool {
	br, ok := core.AsBranch(params)
	if !ok {
		return false
	}
	for _, p := range br {
		if !isTypeNode(p) {
			return false
		}
	}
	// Non-empty all-type params already mark this a signature, so the return is
	// a type: admit Nil/True/False in return position as NilT/Boolean (relaxed).
	// An empty param list is ambiguous with a nullary impl returning a value, so
	// it keeps the strict check — `(fun f () Nil)` stays an impl returning nil.
	if len(br) > 0 {
		return isReturnTypeNode(ret)
	}
	return isTypeNode(ret)
}

// isReturnTypeNode is isTypeNode relaxed for the RETURN slot of a form whose
// params already mark it a signature: the capitalized value literals
// Nil/True/False are admitted as their types (NilT / Boolean).
func isReturnTypeNode(node core.Node) bool {
	if leaf, ok := core.AsLeaf(node); ok {
		switch string(leaf) {
		case "Nil", "True", "False":
			return true
		}
	}
	return isTypeNode(node)
}

// declBuiltins returns the declaration / binding / assignment builtins:
// var, const, fun, method, struct, =, block.
func declBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"fun": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (fun (args) body) — anonymous
			if len(argv) == 2 {
				argList, ok := parseArgList(ctx, argv[0], "fun")
				if !ok {
					return core.TvNil
				}

				return core.TvFun(core.BindFun("", argv[1], argList, ctx))
			}

			// (fun name (args) body)
			if len(argv) != 3 {
				return ctx.Errorf(core.ErrArity, "'fun' expects 2 arguments (args, body) or 3 (name, args, body); got %d", len(argv))
			}

			funName, ok := declName(ctx, argv[0], "fun", "name")
			if !ok {
				return core.TvNil
			}

			// A type SIGNATURE `(fun name (T…) R)` is recognized and ERASED at
			// runtime — the gradual checker reads it (Phase 3), the interpreter
			// binds nothing (the implementation form, with named params and a
			// body, creates the function). See TypeSignatures.md §3.
			if isFunSig(argv[1], argv[2]) {
				return core.TvNil
			}

			argList, ok := parseArgList(ctx, argv[1], "fun")
			if !ok {
				return core.TvNil
			}

			if !ctx.Declare(funName, core.TvFun(core.BindFun(funName, argv[2], argList, ctx)), true) {
				ctx.Errorf(core.ErrRedeclare, "cannot declare function '%s': name already in use", funName)
			}

			return core.TvNil
		}),

		// (macro ~name (params) body) — declares a macro. The leading `~`
		// prefix sigil is required (it parses as its own leaf at argv[0]) and
		// marks the declaration a macro; the macro is registered under the
		// bare name and must be invoked with the `~` call sugar — (~name
		// arg ...). Its body receives the call's QUOTED arguments and returns
		// the code that the macro-call site then resumes (see Macrocall).
		"macro": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) < 2 {
				return ctx.Errorf(core.ErrArity, "'macro' expects (macro ~name (params) body); got %d arguments", len(argv))
			}

			// The `~` prefix is required and parses as its own leaf before the
			// name. Checking it before arity means the common "forgot the ~"
			// mistake gets a pointed message rather than an arity count.
			if tilde, ok := core.AsLeaf(argv[0]); !ok || string(tilde) != "~" {
				return ctx.Errorf(core.ErrBadForm, "macro must be declared with a '~' before its name: (macro ~name (params) body)")
			}

			name, ok := declName(ctx, argv[1], "macro", "name")
			if !ok {
				return core.TvNil
			}

			if len(argv) != 4 {
				return ctx.Errorf(core.ErrArity, "'macro' expects (macro ~name (params) body); got %d arguments", len(argv))
			}

			argList, ok := parseArgList(ctx, argv[2], "macro")
			if !ok {
				return core.TvNil
			}

			if !ctx.Declare(name, core.TvMacro(core.BindFun(name, argv[3], argList, ctx)), true) {
				ctx.Errorf(core.ErrRedeclare, "cannot declare macro '%s': name already in use", name)
			}

			return core.TvNil
		}),

		"method": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (method Receiver.Name (self m) (+ self.n m)) — NAMED: registers
			//   the method on the struct, returns Nil.
			// (method Receiver (self) self.x)            — ANONYMOUS: returns
			//   the bound method as a value (for `property` get/set delegates).
			//
			// Receiver[.Name] is a PATTERN matched structurally — the dot is
			// never evaluated as a member access.
			if len(argv) != 3 {
				return ctx.Errorf(core.ErrArity, "'method' requires 3 arguments (Receiver[.Name], args, body); got %d", len(argv))
			}

			recv, methodName, named, ok := methodTarget(ctx, argv[0])
			if !ok {
				return core.TvNil
			}

			// A method type SIGNATURE `(method Recv.Name (Self …) Ret)` is
			// recognized and ERASED at runtime — the gradual checker reads it;
			// the implementation form (with `self` and a body) binds the
			// method. See TypeSignatures.md §3.
			if named && isFunSig(argv[1], argv[2]) {
				return core.TvNil
			}

			recvVal := recv.Evaluate(ctx)
			if recvVal.Kind != core.KindType {
				return ctx.Errorf(core.ErrType, "'method' receiver must be a type or struct, got kind '%s'", recvVal.Kind)
			}
			recvType := recvVal.Val.(*core.PhoType)

			argList, ok := parseArgList(ctx, argv[1], "method")
			if !ok {
				return core.TvNil
			}

			if len(argList) == 0 {
				return ctx.Errorf(core.ErrBadForm, "method needs at least a receiver argument (self)")
			}
			// The receiver (first param) must be a plain name: parseArgList
			// encodes `(optional x)`/`(spread x)` as "?x"/"#x", and BindMethod
			// binds the instance under that raw key — so an optional/spread
			// receiver leaves `self` unbound in the body. Reject it up front.
			if m := argList[0]; len(m) > 0 && (m[0] == '?' || m[0] == '#') {
				return ctx.Errorf(core.ErrBadForm, "method receiver cannot be optional or spread; use a plain name like (self)")
			}

			label := recvType.Name() + ".<anonymous>"
			if named {
				label = recvType.Name() + "." + methodName
			}
			// BindMethod binds the first param (self) from the instance stack
			// at call time; the rest are the user-supplied parameters.
			bound := core.BindMethod(label, argv[2], argList, ctx)

			if !named {
				// Anonymous: hand the method back as a callable value. It still
				// expects its receiver on the instance stack — `property` (or a
				// Dot wrapper) supplies it.
				return core.TvFun(bound)
			}

			// A STRUCT type keeps its methods on the struct itself, reached
			// globally through any instance (the KindInstance dot path) — they
			// are NOT env names, so a method may share its name with a plain
			// function (pctl.Stdout fun vs Process.Stdout method) without
			// colliding. A PRIMITIVE (or universal "unknown") type stores the
			// method in this package's import-scoped extension table, where the
			// dot accessor resolves it for any value of that type.
			if sdata, isStruct := core.StructOf(recvType); isStruct {
				sdata.Methods[methodName] = bound
				return core.TvNil
			}

			typeKey := recvType.TypeKey()
			if typeKey == "" {
				// A finite union receiver (e.g. Collection = String|List|Map):
				// attach the method to every concrete member, so one declaration
				// covers them all and dispatches on any value of those kinds.
				keys := recvType.MemberKeys()
				if len(keys) == 0 {
					return ctx.Errorf(core.ErrType, "'method' cannot attach to the type '%s'", recvType.Name())
				}
				for _, k := range keys {
					if !ctx.AddMethod(k, methodName, bound, false, isExportedMember(methodName)) {
						return ctx.Errorf(core.ErrRedeclare, "method '%s' for a member of '%s' is already declared in this module", methodName, recvType.Name())
					}
				}
				return core.TvNil
			}
			// Primitive/universal extensions are non-privileged: no private state.
			if !ctx.AddMethod(typeKey, methodName, bound, false, isExportedMember(methodName)) {
				return ctx.Errorf(core.ErrRedeclare, "method '%s.%s' is already declared in this module", recvType.Name(), methodName)
			}
			return core.TvNil
		}),

		// (property <Receiver.>Name get getter [set setter]) — a computed
		// field/variable backed by a getter and optional setter. With a
		// Receiver.Name the getter/setter are anonymous methods registered on
		// the struct (read via inst.Name, write via (= inst.Name v)); with a
		// bare Name they are anonymous funs bound free-standing in the env.
		// `get`/`set` are positional keywords.
		"property": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 3 && len(argv) != 5 {
				return ctx.Errorf(core.ErrArity, "'property' takes (Name get getter) or (Name get getter set setter); got %d argument(s)", len(argv))
			}
			if kw, ok := core.AsLeaf(argv[1]); !ok || string(kw) != "get" {
				return ctx.Errorf(core.ErrBadForm, "'property' expects the keyword 'get' before the getter")
			}
			getter := argv[2].Evaluate(ctx)
			if getter.Kind != core.KindFun {
				return ctx.Errorf(core.ErrType, "property getter must be a function or anonymous method, got kind '%s'", getter.Kind)
			}

			var setter core.Value
			hasSetter := false
			if len(argv) == 5 {
				if kw, ok := core.AsLeaf(argv[3]); !ok || string(kw) != "set" {
					return ctx.Errorf(core.ErrBadForm, "'property' expects the keyword 'set' before the setter")
				}
				setter = argv[4].Evaluate(ctx)
				if setter.Kind != core.KindFun {
					return ctx.Errorf(core.ErrType, "property setter must be a function or anonymous method, got kind '%s'", setter.Kind)
				}
				hasSetter = true
			}

			recv, fieldName, onStruct, ok := methodTarget(ctx, argv[0])
			if !ok {
				return core.TvNil
			}

			if onStruct {
				recvVal := recv.Evaluate(ctx)
				if recvVal.Kind != core.KindType {
					return ctx.Errorf(core.ErrType, "'property' receiver must be a type or struct, got kind '%s'", recvVal.Kind)
				}
				recvType := recvVal.Val.(*core.PhoType)

				// A PRIMITIVE (or universal) type stores the property in this
				// package's import-scoped extension table; a STRUCT type keeps it
				// on the struct (the KindInstance dot / `=` path), unchanged.
				if _, isStruct := core.StructOf(recvType); !isStruct {
					prop := core.Property{Getter: getter, Setter: setter, HasSetter: hasSetter}
					typeKey := recvType.TypeKey()
					if typeKey == "" {
						// A finite union receiver (e.g. Collection): attach to each
						// concrete member type at once.
						keys := recvType.MemberKeys()
						if len(keys) == 0 {
							return ctx.Errorf(core.ErrType, "'property' cannot attach to the type '%s'", recvType.Name())
						}
						for _, k := range keys {
							if !ctx.AddProperty(k, fieldName, prop, false, isExportedMember(fieldName)) {
								return ctx.Errorf(core.ErrRedeclare, "property '%s' for a member of '%s' is already declared in this module", fieldName, recvType.Name())
							}
						}
						return core.TvNil
					}
					if !ctx.AddProperty(typeKey, fieldName, prop, false, isExportedMember(fieldName)) {
						return ctx.Errorf(core.ErrRedeclare, "property '%s.%s' is already declared in this module", recvType.Name(), fieldName)
					}
					return core.TvNil
				}

				sdata, _, ok := receiverStruct(ctx, recvVal, "property")
				if !ok {
					return core.TvNil
				}
				if sdata.Properties == nil {
					sdata.Properties = map[string]core.Property{}
				}
				sdata.Properties[fieldName] = core.Property{Getter: getter, Setter: setter, HasSetter: hasSetter}
				return core.TvNil
			}

			// Free-standing: bind the bare name to a KindProperty value so the
			// leaf evaluator (read) and `=` (write) delegate to get/set.
			name, _ := core.AsLeaf(argv[0])
			if !ctx.Declare(string(name), core.TvProperty(getter, setter, hasSetter), true) {
				return ctx.Errorf(core.ErrRedeclare, "'%s' is already declared in this scope", string(name))
			}
			return core.TvNil
		}),

		// (static method Recv.Name (args) body) / (static property Recv.Name get
		// getter [set setter]) — TYPE-level members, reached through the struct's
		// type value (`Recv.Name`) rather than an instance.
		"static": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) < 1 {
				return ctx.Errorf(core.ErrArity, "'static' requires 'method' or 'property' and a declaration")
			}
			kw, ok := core.AsLeaf(argv[0])
			if !ok {
				return ctx.Errorf(core.ErrBadForm, "'static' expects 'method' or 'property' as its first word")
			}
			switch string(kw) {
			case "method":
				return staticMethod(ctx, argv[1:])
			case "property":
				return staticProperty(ctx, argv[1:])
			default:
				return ctx.Errorf(core.ErrBadForm, "'static' expects 'method' or 'property', got '%s'", string(kw))
			}
		}),

		"block": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (block '(+ it 1)) — a &block evaluated as a value. Bind it as a
			// one-argument function whose single implicit parameter is `it`, so
			// `&(+ it 1)` is a ready-made mapper. The parameter is optional, so a
			// literal block like `&Nil` still works when called with no argument
			// (it then sees `it` as Nil). The closure captures its definition
			// scope like any fun.
			if len(argv) != 1 {
				return ctx.Errorf(core.ErrArity, "'block' takes exactly 1 argument; got %d", len(argv))
			}
			return core.TvFun(core.BindFun("<block>", syntax.Derepr(argv[0]), []string{"?it"}, ctx))
		}),

		// only two valid syntax types:
		//   (= 'ident newVal)             -->   (= "ident" newVal)
		//   (= collection.index newVal)   -->   (= (core.Dot collection index) newVal)
		"=": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'=' requires exactly 2 arguments (target, value); got %d", len(argv))
			}

			if leaf, ok := core.AsLeaf(argv[0]); ok {
				// Post-cutover the target is a bare identifier — (= x 5) — so
				// the leaf's text is the name directly, no evaluation. A
				// computed target goes through the dot/index branch below.
				name := string(leaf)

				// A free-standing property: assigning calls its setter, not Set.
				if cur, found := ctx.Resolve(name); found && cur.Kind == core.KindProperty {
					prop := cur.Val.(core.Property)
					if !prop.HasSetter {
						return ctx.Errorf(core.ErrConstAssign, "property '%s' has no setter (read-only)", name)
					}
					prop.Setter.Val.(core.Fun)(ctx, []core.Node{core.Lit(argv[1].Evaluate(ctx))})
					return core.TvNil
				}

				switch ctx.Set(name, argv[1].Evaluate(ctx)) {
				case core.SetMissing:
					return ctx.Errorf(core.ErrBadAssign,
						"cannot assign to '%s': no such binding — declare it first with (var %s ...)", name, name)
				case core.SetConst:
					return ctx.Errorf(core.ErrConstAssign, "cannot assign to constant '%s'", name)
				}
				return core.TvNil
			}

			br, ok := core.AsBranch(argv[0])
			if !ok || len(br) != 3 || br[0] != core.Leaf(core.Dot) {
				return ctx.Errorf(core.ErrBadAssign, "cannot parse assignment target '%s'; expected an identifier or dot syntax", core.Inspect(argv[0]))
			}

			collection := br[1].Evaluate(ctx)

			switch collection.Kind {
			case core.KindInstance:
				// The field name is the literal identifier after the dot —
				// same as the read path in the Dot accessor — never an
				// evaluated expression.
				lf, ok := core.AsLeaf(br[2])
				if !ok {
					return ctx.Errorf(core.ErrBadAssign, "cannot assign to dynamic field expression '%s'", core.Inspect(br[2]))
				}
				field := string(lf)

				instance := collection.Val.(*core.Instance)

				// A computed field (property): assigning calls its setter (an
				// anonymous method) with the instance as self.
				if prop, found := instance.Struct.Properties[field]; found {
					if field[0] == '#' && !instance.Privileged {
						return ctx.Errorf(core.ErrField, "cannot set private property '%s' from outside the struct's methods", field)
					}
					if !prop.HasSetter {
						return ctx.Errorf(core.ErrConstAssign, "property '%s' has no setter (read-only)", field)
					}
					env := ctx.Env
					env.InstStack = append([]core.Value{collection}, env.InstStack...)
					defer func() { env.InstStack = env.InstStack[1:] }()
					prop.Setter.Val.(core.Fun)(ctx, []core.Node{core.Lit(argv[1].Evaluate(ctx))})
					return core.TvNil
				}

				if _, found := instance.Fields[field]; !found {
					return ctx.Errorf(core.ErrField, "struct instance has no field '%s'", field)
				}

				// Mirror the read path's privacy rule: `#`-prefixed fields are
				// only writable from inside the instance's own methods.
				if field[0] == '#' && !instance.Privileged {
					return ctx.Errorf(core.ErrField, "cannot set private field '%s' from outside the struct's methods", field)
				}

				instance.Fields[field] = argv[1].Evaluate(ctx)
				return core.TvNil

			case core.KindDict:
				dict := *collection.Val.(*map[core.Value]core.Value)
				keyNode, ok := assignIndex(ctx, br[2])
				if !ok {
					return core.TvNil
				}
				key := keyNode.Evaluate(ctx)
				if !scalarKey(ctx, key, "=") {
					return core.TvNil
				}
				dict[key] = argv[1].Evaluate(ctx)
				return core.TvNil

			case core.KindArray:
				array := *collection.Val.(*[]core.Value)
				idxNode, ok := assignIndex(ctx, br[2])
				if !ok {
					return core.TvNil
				}
				idx, ok := asNum(ctx, idxNode.Evaluate(ctx), "=")
				if !ok {
					return core.TvNil
				}
				if int(idx) < 0 || int(idx) >= len(array) {
					return ctx.Errorf(core.ErrIndexRange, "assignment index %d out of range for array of length %d", int(idx), len(array))
				}
				array[int(idx)] = argv[1].Evaluate(ctx)
				return core.TvNil

			case core.KindPackage:
				// A module's bindings are read-only from outside it: an
				// importer may read `pkg.Name` but never assign to it. Only
				// the declaring module can mutate its own var (with a bare
				// `(= Name v)` inside that module).
				pkg := collection.Val.(*core.Package)
				member := core.Inspect(br[2])
				if lf, ok := core.AsLeaf(br[2]); ok {
					member = string(lf)
				}
				return ctx.Errorf(core.ErrBadAssign,
					"cannot assign to '%s' in module '%s': a module's bindings are read-only from outside it", member, pkg.Path)
			}

			return ctx.Errorf(core.ErrBadAssign, "cannot assign into a value of kind '%s'", collection.Kind)
		}),

		"var": global(func(ctx core.Context, argv []core.Node) core.Value {
			// `var` is allowed anywhere: function/method bodies, .pho
			// programs, and the top level of a .phl library. A top-level
			// library var is module-level state — mutable from within the
			// module, but read-only from outside it (the `=` builtin
			// rejects `(= pkg.Name v)`; a capitalized one is exported).
			declarePairs(ctx, argv, false, "var")
			return core.TvNil
		}),

		"const": global(func(ctx core.Context, argv []core.Node) core.Value {
			declarePairs(ctx, argv, true, "const")
			return core.TvNil
		}),

		// (type Name T) — a named type alias: binds Name to the type value T
		// (any type expression — a connective, a parametric, a literal singleton
		// like (Or "GET" "POST"), or another type name). Name then works
		// anywhere a type does: (x.Is? Name), (Or Name …), --@ (~type Name). It
		// is a constant binding, so a Capitalized name exports like any const.
		// (Aliases are non-recursive: Name is not yet in scope while T is
		// evaluated — recursive types are a later addition.)
		"type": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'type' requires a name and a type: (type Name T); got %d arguments", len(argv))
			}
			name, ok := declName(ctx, argv[0], "type", "name")
			if !ok {
				return core.TvNil
			}
			t, ok := asType(ctx, argv[1].Evaluate(ctx), "type")
			if !ok {
				return core.TvNil // asType already reported the diagnostic
			}
			if !ctx.Rebind(name, core.TvType(t), true) {
				return ctx.Errorf(core.ErrRedeclare, "'type' cannot shadow the builtin '%s'", name)
			}
			return core.TvNil
		}),

		// (struct myStruct PublicField privateField)
		// (var myInst (myStruct { 'PublicField 1
		//							'privateField 2 }))   -- init keys are values: still quoted
		"struct": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (struct Name f0 f1 …) — bare fields — or the typed-field form
			// (struct Name.{ F0 T0 … }); structDeclShape reads either.
			if len(argv) < 1 {
				return ctx.Errorf(core.ErrArity, "'struct' requires at least a name; got %d argument(s)", len(argv))
			}

			structName, fields, ok := structDeclShape(ctx, argv)
			if !ok {
				return core.TvNil
			}

			env := ctx.Env

			structData := core.Struct{
				Fields:           fields,
				Methods:          map[string]core.Fun{},
				Properties:       map[string]core.Property{},
				StaticMethods:    map[string]core.Fun{},
				StaticProperties: map[string]core.Property{},
				Origin:           env,
			}

			newFun := core.Fun(func(ctx core.Context, argv []core.Node) core.Value {
				// Record the constructor call for stack traces. Deferred pop
				// (unlike user functions) — a constructor can't (return), and
				// its only foreign-panic source is initializer evaluation,
				// where a missing frame in the rare panic trace is acceptable.
				ctx.PushCallFrame(structName + ".new")
				defer ctx.PopCallFrame()

				fieldMap := make(map[string]core.Value)

				// Every declared field exists on every instance; fields the
				// initializer doesn't mention default to Nil.
				for _, fieldName := range fields {
					fieldMap[fieldName] = core.TvNil
				}

				// Initializers arrive as alternating field-name / value
				// arguments, produced by the `T.{ field value … }` sugar (see
				// syntax/positioned.go). The retired `(T { … })` form passed a
				// single dict and now lands here as an odd argument count —
				// reject it with a pointer at the current syntax.
				if len(argv)%2 != 0 {
					return ctx.Errorf(core.ErrArity,
						"construct a '%s' with %s.{ field value … }; got %d initializer argument(s)",
						structName, structName, len(argv))
				}

				for i := 0; i < len(argv); i += 2 {
					keyVal := argv[i].Evaluate(ctx)
					keyStr, success := keyVal.Val.(string)
					if !success {
						return ctx.Errorf(core.ErrType, "struct field name must be a string, got kind '%s'", keyVal.Kind)
					}

					if !slices.Contains(fields, keyStr) {
						return ctx.Errorf(core.ErrField, "passed invalid key '%s' to '%s' initializer", keyStr, structName)
					}

					fieldMap[keyStr] = argv[i+1].Evaluate(ctx)
				}

				return core.TvInstance(&structData, fieldMap, false)
			})

			// it's a hacky solution at best, but a solution nonetheless
			ctx.Env.Structs[structName] = &structData

			// The struct's name evaluates to a KindType value (a single-struct
			// type) carrying its constructor in the nominal registry, so the
			// type is first-class: usable in typeof/Is?/annotations and still
			// constructible (eval.go calls a KindType through its constructor).
			originPath := ""
			if ctx.Package != nil {
				originPath = ctx.Package.Path
			}
			styp := core.RegisterStruct(&structData, structName, originPath, newFun)
			if !ctx.Declare(structName, core.TvType(styp), true) {
				ctx.Errorf(core.ErrRedeclare, "cannot declare struct '%s': name already in use", structName)
			}

			return core.TvNil
		}),
	}
}

// receiverStruct resolves a `method`/`property` receiver value to the struct
// it names. The receiver evaluates to a KindType value (a struct's name is a
// first-class type after Stage A2); this extracts the underlying *Struct and
// its display name, reporting a diagnostic for a non-type or a non-struct type
// (e.g. a primitive like `Number`, whose methods arrive with the object model).
func receiverStruct(ctx core.Context, v core.Value, caller string) (*core.Struct, string, bool) {
	if v.Kind != core.KindType {
		ctx.Errorf(core.ErrType, "'%s' receiver must be a struct, got kind '%s'", caller, v.Kind)
		return nil, "", false
	}
	t := v.Val.(*core.PhoType)
	st, ok := core.StructOf(t)
	if !ok {
		ctx.Errorf(core.ErrType, "'%s' receiver must be a struct type, got '%s'", caller, t.Name())
		return nil, "", false
	}
	return st, t.Name(), true
}

// declarePairs implements the shared body of `var` and `const`: an even
// list of ('name value) pairs, each installed via Declare so collisions
// with ANY visible binding (locals, captures, globals) are rejected.
func declarePairs(ctx core.Context, argv []core.Node, isConst bool, caller string) {
	if len(argv)%2 != 0 {
		ctx.Errorf(core.ErrArity, "'%s' cannot parse a declaration list of odd length %d; must be name/value pairs", caller, len(argv))
		return
	}

	for i := 0; i < len(argv)-1; i += 2 {
		// A binding name is a bare identifier `(var x 5)` or the typed form
		// `(var (Number x) 5)` — the latter binds `x` and erases the type at
		// runtime. The value in the next slot is still evaluated.
		name, ok := declBindName(ctx, argv[i], caller)
		if !ok {
			return
		}

		// Rebind, not Declare: var/const may re-bind a name in the same
		// scope. Only a builtin clash is rejected.
		if !ctx.Rebind(name, argv[i+1].Evaluate(ctx), isConst) {
			ctx.Errorf(core.ErrRedeclare, "'%s' cannot shadow the builtin '%s'", caller, name)
			return
		}
	}
}
