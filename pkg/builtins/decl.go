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
func parseArgList(ctx core.Context, node core.Node, caller string) ([]string, map[string]core.Node, bool) {
	argBranch, ok := core.AsBranch(node)
	if !ok {
		ctx.Errorf(core.ErrBadForm, "'%s' expected an argument list like (a b), got '%s'", caller, core.Inspect(node))
		return nil, nil, false
	}

	argList := make([]string, len(argBranch))
	sawOptional := false
	var defaults map[string]core.Node

	for i, arg := range argBranch {
		if branch, ok := core.AsBranch(arg); ok {
			// (spread name) — rest-arg, last position only.
			if i == len(argBranch)-1 &&
				len(branch) == 2 &&
				branch[0] == core.Leaf("spread") {

				leaf, ok := core.AsLeaf(branch[1])
				if !ok {
					ctx.Errorf(core.ErrBadForm, "'%s' cannot parse rest-argument pattern '%s'", caller, core.Inspect(branch))
					return nil, nil, false
				}
				argList[i] = fmt.Sprint("#", leaf)
				break
			}

			// (optional name) — omittable parameter; defaults to Nil.
			if len(branch) == 2 && branch[0] == core.Leaf("optional") {
				leaf, ok := core.AsLeaf(branch[1])
				if !ok {
					ctx.Errorf(core.ErrBadForm, "'%s' cannot parse optional-argument pattern '%s'", caller, core.Inspect(branch))
					return nil, nil, false
				}
				argList[i] = fmt.Sprint("?", leaf)
				sawOptional = true
				continue
			}

			// RETIRED: `(or name default)` moved to the SIGNATURE —
			// `(optional Type else default)` (Features.md; defaults are part
			// of a callable's declared interface, not its clause).
			if len(branch) == 3 && branch[0] == core.Leaf("or") {
				ctx.Errorf(core.ErrBadForm, "'%s': defaults are declared in the signature — (optional Type else default) — not in the parameter list", caller)
				return nil, nil, false
			}

			// (var name) — a MUTABLE parameter (the effect-tracking receiver
			// `(var self)`, or a future mutable value arg). Binds the name like
			// an ordinary required parameter; the mutability is a STATIC contract
			// the effect checker reads from the source — at runtime an instance
			// is already a mutable pointer, so binding is unchanged. (Effect
			// tracking, Phase 1: parsed + bound, not yet semantically enforced.)
			if len(branch) == 2 && branch[0] == core.Leaf("var") {
				leaf, ok := core.AsLeaf(branch[1])
				if !ok {
					ctx.Errorf(core.ErrBadForm, "'%s' cannot parse mutable-parameter pattern '%s'", caller, core.Inspect(branch))
					return nil, nil, false
				}
				// Only the RECEIVER may be mutable — a method's `self`. A
				// `(var <other>)` value parameter is rejected (Effects.md).
				if string(leaf) != "self" {
					ctx.Errorf(core.ErrBadForm, "'%s': (var %s) is not allowed — only the receiver '(var self)' may be mutable; a value parameter cannot be declared mutable", caller, leaf)
					return nil, nil, false
				}
				if sawOptional {
					ctx.Errorf(core.ErrBadForm, "'%s' parameter '%s' cannot follow an optional parameter", caller, leaf)
					return nil, nil, false
				}
				argList[i] = string(leaf)
				continue
			}

			// RETIRED: `(disc X)` — declare the parameter `(const T)` in the
			// SIGNATURE and dispatch with literal patterns in the clauses.
			if len(branch) == 2 && branch[0] == core.Leaf("disc") {
				ctx.Errorf(core.ErrBadForm, "'%s': (disc X) is retired — declare the parameter (const T) in the signature and dispatch with literal patterns in the clauses", caller)
				return nil, nil, false
			}

			// (const X) — a parse-time-constant SIGNATURE slot (Features.md;
			// the successor to disc). Only trait member signatures reach
			// parseArgList with one — it occupies a positional slot for arity,
			// binding a synthetic key.
			if len(branch) == 2 && branch[0] == core.Leaf("const") {
				argList[i] = fmt.Sprint("@const", i)
				continue
			}

			ctx.Errorf(core.ErrBadForm, "'%s' cannot parse argument pattern '%s'", caller, core.Inspect(arg))
			return nil, nil, false
		}

		leaf, _ := core.AsLeaf(arg)
		if sawOptional {
			ctx.Errorf(core.ErrBadForm, "'%s' required parameter '%s' cannot follow an optional parameter", caller, leaf)
			return nil, nil, false
		}
		argList[i] = string(leaf)
	}

	return argList, defaults, true
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

// defineFunOrMethod implements the 3-argument form of `=`: a function or method
// IMPLEMENTATION (the decl/impl split — `fun`/`method` declare the signature, `=`
// provides the impl). `(= name (params) body)` binds a function; `(= Owner.name
// (params) body)` defines a method on the owner type. It reuses the exact binding
// machinery the `fun`/`method` builtins use (kept additive during the tolerant
// rollout — the fun/method impl forms still work too). argv = [target, params, body].
func defineFunOrMethod(ctx core.Context, argv []core.Node) core.Value {
	// RETIRED (Features.md §1): implementations are `let` clauses now. Keep a
	// pointed error so pre-cutover code gets steered to the new form.
	return ctx.Errorf(core.ErrBadForm, "'=' no longer defines implementations; write (let %s (params) [where guard] = body) after its signature", core.Inspect(argv[0]))
}

// stripPropType unwraps a typed property name slot `(Type target)` — a
// two-element branch whose SECOND child is the real name target (a bare name or
// a `Recv.name` receiver pattern, which itself is a three-element Dot branch) —
// returning that target. The declared Type is ERASED at runtime (read by the
// gradual checker), mirroring typed var/const bindings `(Type x)`. A bare name
// or a `Recv.name` pattern is returned unchanged.
func stripPropType(node core.Node) core.Node {
	if br, ok := core.AsBranch(node); ok && len(br) == 2 {
		return br[1]
	}
	return node
}

// isPropAccessor reports whether node is a `(get …)` / `(set …)` delegate sub-form
// (a branch headed by the keyword) — the property delegate syntax.
func isPropAccessor(node core.Node, kw string) bool {
	br, ok := core.AsBranch(node)
	if !ok || len(br) < 1 {
		return false
	}
	h, ok := core.AsLeaf(br[0])
	return ok && string(h) == kw
}

// bindPropDelegate binds one property accessor `(get (params) body)` /
// `(set (params) body)` to a callable value: a `self`-receiver method (BindMethod)
// for a struct property, or a plain function (BindFun) for a free-standing one — no
// anonymous function ever exists.
func bindPropDelegate(ctx core.Context, node core.Node, kw string, onStruct bool, recvType *core.PhoType) (core.Value, bool) {
	br, isBr := core.AsBranch(node)
	if !isBr || len(br) != 3 {
		ctx.Errorf(core.ErrBadForm, "'property' %s must be a (%s (params) body) form", kw, kw)
		return core.TvNil, false
	}
	if h, ok := core.AsLeaf(br[0]); !ok || string(h) != kw {
		ctx.Errorf(core.ErrBadForm, "'property' expected a (%s …) accessor", kw)
		return core.TvNil, false
	}
	argList, defaults, ok := parseArgList(ctx, br[1], "property "+kw)
	if !ok {
		return core.TvNil, false
	}
	if onStruct && recvType != nil {
		return core.TvFun(core.BindMethod(recvType.Name()+"."+kw, br[2], argList, defaults, ctx)), true
	}
	return core.TvFun(core.BindFun(kw, br[2], argList, defaults, ctx)), true
}

// propertyDelegates computes a property's (getter, setter, hasSetter) from the
// parenthesized accessor sub-forms `(get (params) body)` and optional
// `(set (params) body)` — the ONLY property syntax. The target has already been
// resolved by the caller (onStruct/recv drive method-vs-fun binding: a struct
// property's getter/setter are `self`-methods, a free-standing one's are funs).
func propertyDelegates(ctx core.Context, argv []core.Node, onStruct bool, recv core.Node) (getter, setter core.Value, hasSetter, ok bool) {
	if !isPropAccessor(argv[1], "get") {
		ctx.Errorf(core.ErrBadForm, "'property' takes (target (get (params) body) [(set (params) body)]); the getter must be a (get …) accessor")
		return core.TvNil, core.TvNil, false, false
	}
	if len(argv) != 2 && len(argv) != 3 {
		ctx.Errorf(core.ErrArity, "'property' takes (target (get …) [(set …)]); got %d argument(s)", len(argv))
		return core.TvNil, core.TvNil, false, false
	}
	var recvType *core.PhoType
	if onStruct {
		recvVal := recv.Evaluate(ctx)
		if recvVal.Kind != core.KindType {
			ctx.Errorf(core.ErrType, "'property' receiver must be a type or struct, got kind '%s'", recvVal.Kind)
			return core.TvNil, core.TvNil, false, false
		}
		recvType = recvVal.Val.(*core.PhoType)
	}
	g, gok := bindPropDelegate(ctx, argv[1], "get", onStruct, recvType)
	if !gok {
		return core.TvNil, core.TvNil, false, false
	}
	getter = g
	if len(argv) == 3 {
		s, sok := bindPropDelegate(ctx, argv[2], "set", onStruct, recvType)
		if !sok {
			return core.TvNil, core.TvNil, false, false
		}
		setter, hasSetter = s, true
	}
	return getter, setter, hasSetter, true
}

// structDeclShape reads a struct declaration's name and field names, accepting
// both forms:
//
//	(struct Name f0 f1 …)            — bare untyped fields
//	(struct Name.{ T0 F0 T1 F1 … })  — typed fields (Type name)
//
// The typed form reaches the runtime as a single branch argument
// `(Name T0 "F0" T1 "F1" …)` (the `.{}` sugar quotes the field NAMES and leaves
// the types as ordinary expressions, so names sit at the EVEN slots). Field
// TYPES are static-only — at runtime a struct is just its field names — so they
// are read past here; the linter's checker consumes them.
func structDeclShape(ctx core.Context, argv []core.Node) (name string, fields []string, ok bool) {
	if len(argv) == 1 {
		if br, isBranch := core.AsBranch(argv[0]); isBranch {
			head, isLeaf := core.AsLeaf(br[0])
			if !isLeaf {
				ctx.Errorf(core.ErrBadForm, "'struct' name must be a bare identifier; got '%s'", core.Inspect(br[0]))
				return "", nil, false
			}
			for i := 2; i < len(br); i += 2 {
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
	// Generic typed-field form `(struct Name { T0 f0 T1 f1 … })` — the `{}` brace
	// lowers to a `(Map T0 f0 T1 f1 …)` literal (key = type, value = field name).
	// Phase 1 generics erases the types and keeps the field NAMES, which are bare
	// identifiers at the value positions (not quoted strings like `.{}`).
	if len(argv) == 2 {
		if br, isBranch := core.AsBranch(argv[1]); isBranch && len(br) >= 1 {
			if head, isLeaf := core.AsLeaf(br[0]); isLeaf && string(head) == core.Map {
				name, isName := declName(ctx, argv[0], "struct", "name")
				if !isName {
					return "", nil, false
				}
				for i := 2; i < len(br); i += 2 {
					fl, isFieldLeaf := core.AsLeaf(br[i])
					if !isFieldLeaf {
						ctx.Errorf(core.ErrBadForm, "generic struct field name must be an identifier, got '%s'", core.Inspect(br[i]))
						return "", nil, false
					}
					fields = append(fields, string(fl))
				}
				return name, fields, true
			}
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

// templateParamName returns the name of one `(template …)` type parameter node:
// a bare leaf `P` (unbound), or the bound form `(Bound P)` whose parameter is
// the LAST element (the rest is the bound, erased in Phase 1). Empty for a node
// that names no parameter.
func templateParamName(n core.Node) string {
	if leaf, ok := core.AsLeaf(n); ok {
		return string(leaf)
	}
	if br, ok := core.AsBranch(n); ok && len(br) >= 2 {
		if leaf, ok := core.AsLeaf(br[len(br)-1]); ok {
			return string(leaf)
		}
	}
	return ""
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
	_, recvType, ok := staticReceiver(ctx, recv, "static method")
	if !ok {
		return core.TvNil
	}
	// A `static method` form is a SIGNATURE (Features.md §1): its params are
	// all real arguments (the receiver type is implicit — `Self` in the sig
	// resolves to it), flat with a `->` before the return type. The
	// implementation is the adjacent `(let Recv.Name params… = body)` clause,
	// which routes to StaticMethods via the "Recv/Name" registry key.
	params, ret, ok := splitArrow(argv[1:])
	if !ok || !isFunSig(core.Branch(params), ret) {
		return ctx.Errorf(core.ErrBadForm, "'static method %s.%s' declares a signature only: (static method %s.%s Type… -> Result); the implementation is (let %s.%s params… = body)", core.Inspect(recv), name, core.Inspect(recv), name, core.Inspect(recv), name)
	}
	return withSelfType(ctx, core.TvType(recvType), func(sctx core.Context) core.Value {
		return registerSig(sctx, core.Inspect(recv)+"/"+name, name, core.Branch(params), ret, true, "static method")
	})
}

// staticProperty declares a type-level property `(static property Recv.Name get
// getter [set setter])` — read (and optionally written) through the TYPE value,
// the getter/setter receiving the type as their receiver.
func staticProperty(ctx core.Context, argv []core.Node) core.Value {
	if len(argv) < 2 {
		return ctx.Errorf(core.ErrArity, "'static property' takes (Receiver.Name (get (params) body) [(set (params) body)]); got %d", len(argv))
	}
	recv, name, named, ok := methodTarget(ctx, stripPropType(argv[0]))
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
	// Same delegate machinery as an instance property: the `(get …)`/`(set …)`
	// bodies bind as `self`-methods, with `self` the TYPE value pushed by the
	// static-property dot read/write path.
	getter, setter, hasSetter, ok := propertyDelegates(ctx, argv, true, recv)
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
			return false // capitalized VALUE literals, not types (the nil TYPE is None)
		}
		return s != "" && s[0] >= 'A' && s[0] <= 'Z'
	}
	if br, ok := core.AsBranch(node); ok && len(br) >= 1 {
		if head, ok := core.AsLeaf(br[0]); ok {
			// A function TYPE `(fun (P…) R)` shares the head of a function value
			// but in a type position reads as a type — so a method sig whose
			// param is a function type (e.g. `(method I.bind (Self (fun (I) O)) O)`)
			// is recognized and erased, not bound as an impl.
			return typeConnectives[string(head)] || string(head) == "fun"
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
		if !isSigParamNode(p) {
			return false
		}
	}
	// The return slot must be a real TYPE — `None` for nil-returning, `Boolean`
	// for bool. The value literals none/true/false are NOT types (nor is the
	// dropped Nil/True/False). Empty and non-empty param lists both use the
	// strict check: `(fun f () None)` is a sig; `(fun f () none)` is a nullary
	// impl returning the nil VALUE.
	return isTypeNode(ret)
}

// isSigParamNode is isTypeNode extended for a signature param slot: it also
// accepts a `(var/spread/optional/const <type>)` modifier wrapping a type —
// including the defaulted optional `(optional Type else DEFAULT)` — so a
// signature can declare a mutable `(var Self)` receiver, variadic/optional
// params, and parse-time-constant `(const T)` slots. The inner must still be
// a type (capitalized), so an implementation's lowercase `(var self)`/
// `(spread xs)` param is NOT mistaken for a signature.
func isSigParamNode(node core.Node) bool {
	if br, ok := core.AsBranch(node); ok {
		if head, ok := core.AsLeaf(br[0]); ok {
			switch string(head) {
			case "var", "spread", "const", "disc":
				return len(br) == 2 && isTypeNode(br[1])
			case "optional":
				if len(br) == 2 {
					return isTypeNode(br[1])
				}
				// (optional Type else DEFAULT)
				if len(br) == 4 {
					kw, ok := core.AsLeaf(br[2])
					return ok && string(kw) == "else" && isTypeNode(br[1])
				}
				return false
			}
		}
	}
	return isTypeNode(node)
}

// splitArrow splits a flat signature tail into its parameter-type nodes and the
// single return-type node, separated by a top-level `->` marker:
//
//	(fun add Integer Integer -> Integer)  ->  params [Integer Integer], ret Integer
//	(fun cwd -> String)                   ->  params [],                 ret String
//
// ok=false when there is no top-level `->`, or it isn't followed by exactly one
// return node. A `->` nested inside a `(…)`/`[…]` (a function or map type) is not
// a top-level child here, so it never interferes.
func splitArrow(nodes []core.Node) (params []core.Node, ret core.Node, ok bool) {
	for i, n := range nodes {
		if lf, isLeaf := core.AsLeaf(n); isLeaf && string(lf) == "->" {
			if i != len(nodes)-2 {
				return nil, nil, false
			}
			return nodes[:i], nodes[i+1], true
		}
	}
	return nil, nil, false
}

// declBuiltins returns the declaration / binding / assignment builtins:
// var, const, fun, method, struct, =, block.
func declBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		// (template P (Bound Q) …) — declares type parameters for the following
		// declaration. Phase 1 generics ERASES types at runtime: each parameter
		// is bound to the top type Unknown so the following generic declaration
		// resolves — a generic method whose receiver is a type parameter becomes
		// a UNIVERSAL (Unknown) method, callable on any value, and a generic
		// struct's field types collapse to Unknown (struct fields are untyped at
		// runtime anyway). Bounds are erased. Bound idempotently: the same
		// parameter name recurs across templates and must not redeclare.
		"template": global(func(ctx core.Context, argv []core.Node) core.Value {
			for _, p := range argv {
				name := templateParamName(p)
				if name == "" {
					continue
				}
				if _, exists := ctx.Resolve(name); !exists {
					ctx.Declare(name, core.TvType(core.TypeUnknown), true)
				}
			}
			return core.TvNil
		}),

		"fun": global(func(ctx core.Context, argv []core.Node) core.Value {
			// `fun` declares a type SIGNATURE only: (fun name Types… -> Result),
			// e.g. (fun add Integer Integer -> Integer) or the 0-arg (fun cwd ->
			// String). Parameter TYPES are flat (no parens); `->` separates them
			// from the return type. It registers an overload the adjacent
			// `(let name params… = body)` clauses attach to (Features.md §1/§9). An
			// inline callable VALUE is a lambda.
			if len(argv) < 2 {
				return ctx.Errorf(core.ErrBadForm, "'fun' declares a signature: (fun name Types… -> Result); to make an inline callable, use a lambda: (lambda … -> …)")
			}

			funName, ok := declName(ctx, argv[0], "fun", "name")
			if !ok {
				return core.TvNil
			}
			params, ret, ok := splitArrow(argv[1:])
			if !ok {
				return ctx.Errorf(core.ErrBadForm, "'fun %s' is a signature: write (fun %s Type… -> ReturnType); the implementation is (let %s params… = body)", funName, funName, funName)
			}
			paramsBranch := core.Branch(params)
			if !isFunSig(paramsBranch, ret) {
				return ctx.Errorf(core.ErrBadForm, "'fun %s' declares a signature only; parameters and the return must be types: (fun %s Type… -> ReturnType)", funName, funName)
			}
			return registerSig(ctx, funName, funName, paramsBranch, ret, false, "fun")
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

			argList, defaults, ok := parseArgList(ctx, argv[2], "macro")
			if !ok {
				return core.TvNil
			}

			if !ctx.Declare(name, core.TvMacro(core.BindFun(name, argv[3], argList, defaults, ctx)), true) {
				ctx.Errorf(core.ErrRedeclare, "cannot declare macro '%s': name already in use", name)
			}

			return core.TvNil
		}),

		"method": global(func(ctx core.Context, argv []core.Node) core.Value {
			// `method` declares a type SIGNATURE only:
			//   (method Recv.Name Self Type… -> Result), e.g.
			//   (method List.sum Self -> Integer). The receiver + parameter TYPES
			//   are flat (no parens); `->` separates them from the return type. It
			//   registers an overload the adjacent `(let Recv.Name self … = body)`
			//   clauses attach to. `Self` is bound to the receiver type while the
			//   signature evaluates. Recv.Name is a PATTERN — the dot is never
			//   evaluated as a member access.
			if len(argv) < 1 {
				return ctx.Errorf(core.ErrArity, "'method' requires a 'Recv.Name' signature: (method Recv.Name Self Type… -> Result)")
			}

			recv, methodName, named, ok := methodTarget(ctx, argv[0])
			if !ok {
				return core.TvNil
			}
			if !named {
				return ctx.Errorf(core.ErrBadForm, "'method' needs a 'Recv.Name': declare (method Recv.Name Self Type… -> Result); for an inline callable with a receiver, use a lambda: (lambda Recv self … -> body)")
			}

			params, ret, ok := splitArrow(argv[1:])
			if !ok {
				return ctx.Errorf(core.ErrBadForm, "'method %s.%s' is a signature: write (method %s.%s Self Type… -> Result); the implementation is (let %s.%s self … = body)", core.Inspect(recv), methodName, core.Inspect(recv), methodName, core.Inspect(recv), methodName)
			}
			paramsBranch := core.Branch(params)
			if !isFunSig(paramsBranch, ret) {
				return ctx.Errorf(core.ErrBadForm, "'method %s.%s' declares a signature only; the receiver, parameters, and return must be types: (method %s.%s Self Type… -> Result)", core.Inspect(recv), methodName, core.Inspect(recv), methodName)
			}
			recvVal := recv.Evaluate(ctx)
			if recvVal.Kind != core.KindType {
				return ctx.Errorf(core.ErrType, "'method' receiver must be a type or struct, got kind '%s'", recvVal.Kind)
			}
			return withSelfType(ctx, recvVal, func(sctx core.Context) core.Value {
				return registerSig(sctx, core.Inspect(recv)+"."+methodName, methodName, paramsBranch, ret, false, "method")
			})
		}),

		// (property <Receiver.>Name (get (params) body) [(set (params) body)]) —
		// a computed field/variable backed by a getter and optional setter. With
		// a Receiver.Name the getter/setter are `self`-methods registered on the
		// struct (read via inst.Name, write via (= inst.Name v)); with a bare Name
		// they are funs bound free-standing in the env. The parenthesized
		// `(get …)`/`(set …)` accessors are the only accepted form.
		"property": global(func(ctx core.Context, argv []core.Node) core.Value {
			if len(argv) < 2 {
				return ctx.Errorf(core.ErrArity, "'property' takes (target (get (params) body) [(set (params) body)]); got %d argument(s)", len(argv))
			}

			recv, fieldName, onStruct, ok := methodTarget(ctx, stripPropType(argv[0]))
			if !ok {
				return core.TvNil
			}

			getter, setter, hasSetter, ok := propertyDelegates(ctx, argv, onStruct, recv)
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
			name, _ := core.AsLeaf(recv)
			if !ctx.Declare(string(name), core.TvProperty(getter, setter, hasSetter), true) {
				return ctx.Errorf(core.ErrRedeclare, "'%s' is already declared in this scope", string(name))
			}
			return core.TvNil
		}),

		// (static method Recv.Name (args) body) / (static property Recv.Name
		// (get (params) body) [(set (params) body)]) — TYPE-level members, reached
		// through the struct's type value (`Recv.Name`) rather than an instance.
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
			return core.TvFun(core.BindFun("<block>", syntax.Derepr(argv[0]), []string{"?it"}, nil, ctx))
		}),

		// only two valid syntax types:
		//   (= 'ident newVal)             -->   (= "ident" newVal)
		//   (= collection.index newVal)   -->   (= (core.Dot collection index) newVal)
		"=": global(func(ctx core.Context, argv []core.Node) core.Value {
			// `=` is arity-overloaded by the decl/impl split: a 3-arg
			// `(= name (params) body)` / `(= Owner.name (params) body)` is a function
			// or method IMPLEMENTATION; the 2-arg `(= target value)` is reassignment.
			// No 4-child `=` ever existed (this guard used to hard-require 2), so the
			// arity is an unambiguous discriminator.
			if len(argv) == 3 {
				return defineFunOrMethod(ctx, argv)
			}
			if len(argv) != 2 {
				return ctx.Errorf(core.ErrArity, "'=' requires 2 arguments (target value — reassignment) or 3 (name (params) body — a fun/method implementation); got %d", len(argv))
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
				instance := collection.Val.(*core.Instance)

				// A `.[i]` target on a struct whose type overloads `[]=`
				// dispatches to the write operator (Features.md §7): the operands
				// are the bracket's index and the assignment's right-hand side.
				if bracket, ok := core.AsBranch(br[2]); ok && len(bracket) >= 1 && bracket[0] == core.Leaf(core.Slice) && !isSliceForm(bracket) {
					if _, found := instance.Struct.Methods["[]="]; found {
						idx, ok := assignIndex(ctx, br[2])
						if !ok {
							return core.TvNil
						}
						if v, dispatched := dispatchOperator(ctx, "[]=", collection, []core.Node{idx, argv[1]}); dispatched {
							return v
						}
					}
				}

				// The field name is the literal identifier after the dot —
				// same as the read path in the Dot accessor — never an
				// evaluated expression.
				lf, ok := core.AsLeaf(br[2])
				if !ok {
					return ctx.Errorf(core.ErrBadAssign, "cannot assign to dynamic field expression '%s'", core.Inspect(br[2]))
				}
				field := string(lf)

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

		// (let name = value [name = value]*)      — immutable bindings.
		// (let var name = value [name = value]*)  — mutable module/local state.
		// The canonical declaration form: `let` binds constants, `let var` binds
		// mutable state (the same semantics const/var carry). The optional `var`
		// modifier and the `=` markers are structural punctuation stripped here;
		// names and values reuse the var/const binding logic.
		"let": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (let [Owner.]name p1 p2 … [where guard] = body) — an implementation
			// CLAUSE (Features.md §1/§2): flat parameters + optional guard, attached
			// to the adjacent signature. Value bindings (`(let x = v)`,
			// `(let var x = v)`, typed, multi) stay below. A param-less
			// `(let name = body)` is a 0-arg function impl when `name` has a
			// signature, else a value binding (isZeroArgClause). Canonicalize an
			// operator-impl target first so `(let Recv.[] …)` / `(let Recv.[]= …)`
			// become clauses named "[]"/"[]=" (Features.md §7).
			argv = desugarOperatorTarget(argv)
			if isClauseForm(argv) || isZeroArgClause(ctx, argv) {
				return defineClause(ctx, argv)
			}
			declareLet(ctx, argv)
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
			// (struct Name.{ T0 F0 … }); structDeclShape reads either.
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

// isBareLeaf reports whether a node is the bare word `word` (the `var` modifier
// or an `=` marker in a `let` form), not a value to evaluate.
func isBareLeaf(n core.Node, word string) bool {
	leaf, ok := core.AsLeaf(n)
	return ok && string(leaf) == word
}

// declareLet implements the first-class `let` / `let var` declaration: an
// optional leading `var` modifier followed by one or more `target = value`
// segments. A target is a bare name, a typed `(Type name)`, or a destructuring
// pattern (`[a b …]`, `Type.{ … }`) — see bindLetTarget. `var` (leading, or a
// per-binder `(var …)`) makes the binding reassignable; a plain binder is const.
func declareLet(ctx core.Context, argv []core.Node) {
	i := 0
	defaultMutable := false
	if i < len(argv) && isBareLeaf(argv[i], "var") {
		defaultMutable = true
		i++
	}
	for i < len(argv) {
		// The retired ungrouped typed form `Type name = value` (two bare leaves
		// before `=`): point at the grouped `(Type name)` replacement.
		if i+3 < len(argv) && !isBareLeaf(argv[i+1], "=") && isBareLeaf(argv[i+2], "=") {
			ctx.Errorf(core.ErrBadForm, "a typed 'let' binding is written '(Type name) = value', not 'Type name = value'; got '%s %s'", core.Inspect(argv[i]), core.Inspect(argv[i+1]))
			return
		}
		if i+2 >= len(argv) || !isBareLeaf(argv[i+1], "=") {
			ctx.Errorf(core.ErrArity, "'let' binding expects 'target = value'; missing '=' near %s", core.Inspect(argv[i]))
			return
		}
		target, valueNode := argv[i], argv[i+2]
		i += 3
		if !bindLetTarget(ctx, target, valueNode.Evaluate(ctx), defaultMutable) {
			return
		}
	}
}

// bindLetTarget binds one `let` target to v. A bare-identifier target binds
// directly — any identifier here is a binder, never a pattern literal, so a
// Capitalized name binds too. A branch target is a pattern: `(Type name)` (typed
// bind — the type is erased), `(var …)`, a `[…]` list destructure, or a
// `Type.{ … }` struct destructure. defaultMutable (the leading `var`) makes
// every binder in the target reassignable; a per-binder `(var …)` does the same
// for just that one. Reports and returns false on a malformed target, a match
// failure, or a builtin-shadowing name.
func bindLetTarget(ctx core.Context, target core.Node, v core.Value, defaultMutable bool) bool {
	if _, isBranch := core.AsBranch(target); !isBranch {
		name, ok := declName(ctx, target, "let", "name")
		if !ok {
			return false
		}
		return declareBinding(ctx, name, v, !defaultMutable)
	}

	// A single typed/mutable binder — (name), (var name), (Type name),
	// (var Type name) — binds one name with its type erased. Handled here (not
	// via the pattern engine) so the annotation may be a COMPOUND type
	// expression like (Or Number String), which a runtime type-test pattern
	// could not name.
	if name, mutable, ok := simpleLetBinder(target); ok {
		return declareBinding(ctx, name, v, !(defaultMutable || mutable))
	}

	pat, ok := parsePattern(ctx, target, newBinderSet())
	if !ok {
		return false
	}
	// A `let` has no alternative branch, so a scalar type annotation cannot
	// drive dispatch — it is erased (gradual), leaving the destructure and
	// mutability as the runtime-meaningful parts.
	eraseScalarTypeTests(pat)

	binds := map[string]core.Value{}
	if !matchPattern(ctx, pat, v, binds, false) {
		ctx.Errorf(core.ErrType, "value %s does not match pattern %s", core.Inspect(core.Lit(v)), pat.src)
		return false
	}
	for _, b := range patternBinders(pat) {
		if !declareBinding(ctx, b.name, binds[b.name], !(defaultMutable || b.mutable)) {
			return false
		}
	}
	return true
}

// simpleLetBinder recognizes a `let` target that binds exactly one name with
// its type (if any) erased: (name), (var name), (Type name), or
// (var Type name). The Type slot may be any expression — including a compound
// (Or A B) — since it is a static annotation, never evaluated here. Returns
// ok=false for a list/struct destructure or anything else (the caller then
// routes to the pattern engine).
func simpleLetBinder(target core.Node) (name string, mutable bool, ok bool) {
	br, isBr := core.AsBranch(target)
	if !isBr || len(br) == 0 {
		return "", false, false
	}
	elems := br
	if h, isLeaf := core.AsLeaf(br[0]); isLeaf && string(h) == "var" {
		mutable = true
		elems = br[1:]
	}
	switch len(elems) {
	case 1: // (name) capture / (var name)
		if lf, isLeaf := core.AsLeaf(elems[0]); isLeaf && isBinderName(string(lf)) {
			return string(lf), mutable, true
		}
	case 2: // (Type name) / (var Type name) — the Type slot is erased
		if lf, isLeaf := core.AsLeaf(elems[1]); isLeaf && isBinderName(string(lf)) {
			return string(lf), mutable, true
		}
	}
	return "", false, false
}

// declareBinding rebinds name to v (const unless isConst is false), reporting
// the builtin-shadow error the way declareLet always has.
func declareBinding(ctx core.Context, name string, v core.Value, isConst bool) bool {
	if !ctx.Rebind(name, v, isConst) {
		ctx.Errorf(core.ErrRedeclare, "'let' cannot shadow the builtin '%s'", name)
		return false
	}
	return true
}
