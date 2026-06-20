package builtins

import (
	"fmt"
	"slices"
	"unicode"

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

			argList, ok := parseArgList(ctx, argv[1], "fun")
			if !ok {
				return core.TvNil
			}

			if !ctx.Declare(funName, core.TvFun(core.BindFun(funName, argv[2], argList, ctx)), true) {
				ctx.Errorf(core.ErrRedeclare, "cannot declare function '%s': name already in use", funName)
			}

			return core.TvNil
		}),

		// (macro name! (params) body) — declares a macro. The trailing `!`
		// is required (it parses as its own leaf at argv[1]) and marks the
		// declaration a macro; the macro is registered under the bare name
		// and must be invoked with the `!` call sugar — (name! arg ...). Its
		// body receives the call's QUOTED arguments and returns the code that
		// the macro-call site then resumes (see the Macrocall builtin).
		"macro": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) < 2 {
				return ctx.Errorf(core.ErrArity, "'macro' expects (macro name! (params) body); got %d arguments", len(argv))
			}

			name, ok := declName(ctx, argv[0], "macro", "name")
			if !ok {
				return core.TvNil
			}

			// The `!` is required and parses as its own leaf right after the
			// name. Checking it before arity means the common "forgot the !"
			// mistake gets a pointed message rather than an arity count.
			if bang, ok := core.AsLeaf(argv[1]); !ok || string(bang) != "!" {
				return ctx.Errorf(core.ErrBadForm, "macro '%s' must be declared with a '!' after its name: (macro %s! (params) body)", name, name)
			}

			if len(argv) != 4 {
				return ctx.Errorf(core.ErrArity, "'macro' expects (macro name! (params) body); got %d arguments", len(argv))
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

			sdata, sname, ok := receiverStruct(ctx, recv.Evaluate(ctx), "method")
			if !ok {
				return core.TvNil
			}

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

			label := sname + ".<anonymous>"
			if named {
				label = sname + "." + methodName
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

			// Named methods live on the struct itself; the Dot accessor looks
			// them up in Methods when field lookup misses. They are NOT
			// declared as env names, so a method may share its name with a
			// plain function (pctl.Stdout fun vs Process.Stdout method) without
			// colliding.
			sdata.Methods[methodName] = bound
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
				sdata, _, ok := receiverStruct(ctx, recv.Evaluate(ctx), "property")
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
					if unicode.IsLower(rune(field[0])) && !instance.Privileged {
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

				// Mirror the read path's privacy rule: lowercase fields are
				// only writable from inside the instance's own methods.
				if unicode.IsLower(rune(field[0])) && !instance.Privileged {
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

		// (struct myStruct PublicField privateField)
		// (var myInst (myStruct { 'PublicField 1
		//							'privateField 2 }))   -- init keys are values: still quoted
		"struct": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (struct Name f0 f1 …) — the name then the bare field identifiers
			// as trailing arguments.
			if len(argv) < 1 {
				return ctx.Errorf(core.ErrArity, "'struct' requires at least a name; got %d argument(s)", len(argv))
			}

			structName, ok := declName(ctx, argv[0], "struct", "name")
			if !ok {
				return core.TvNil
			}

			fieldNodes := argv[1:]
			fields := make([]string, len(fieldNodes))

			for i, fieldNode := range fieldNodes {
				name, ok := core.AsLeaf(fieldNode)
				if !ok {
					return ctx.Errorf(core.ErrBadForm, "struct field names must be identifiers, got '%s'", core.Inspect(fieldNode))
				}
				fields[i] = string(name)
			}

			env := ctx.Env

			structData := core.Struct{
				Fields:     fields,
				Methods:    map[string]core.Fun{},
				Properties: map[string]core.Property{},
				Origin:     env,
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
		// Post-cutover a binding name is a bare identifier: (var x 5), never
		// (var 'x 5) and never a dynamic expression. Read the leaf's text
		// directly; the value in the next slot is still evaluated.
		name, ok := declName(ctx, argv[i], caller, "name")
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
