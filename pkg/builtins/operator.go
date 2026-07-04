package builtins

import "pho/pkg/core"

// Operator overloading (Features.md §7).
//
// `(operator Recv.OP (Self …) Ret)` declares an operator overload on a type — a
// SIGNATURE the adjacent `(let Recv.OP (self …) = body)` clauses attach to, just
// like a method whose name is an operator symbol. Usage stays in the language's
// normal positions: the prefix call `(+ a b)` and the index forms `a.[i]` /
// `(= a.[i] v)` dispatch to the overload when the first operand's type overloads
// OP. Overloads live in the struct's own method table under the operator name
// ("+", "[]", "[]=", …), so all the existing method dispatch/write-back applies.

// overloadableOperators is the set of operator names a type may overload.
var overloadableOperators = map[string]bool{
	"+": true, "-": true, "*": true, "/": true, "mod": true,
	"<": true, "<=": true, ">": true, ">=": true, "==": true, "~=": true,
	"[]": true, "[]=": true,
}

func isOverloadableOperator(name string) bool { return overloadableOperators[name] }

// arithOverloadNames are the prefix operators wrapped with withOverload in
// register.go. The index operators [] / []= are dispatched from the index
// read/write paths instead, so they are not listed here.
var arithOverloadNames = []string{"+", "-", "*", "/", "mod", "<", "<=", ">", ">=", "==", "~="}

// desugarOperatorTarget canonicalizes an operator declaration/impl target so the
// method machinery sees a plain `(Dot Recv name)` with a leaf operator name.
// Symbol/word operators (`Recv.+`) already parse that way. The index operators
// need fixing up because `[]` lexes as an empty slice literal, not a name:
//
//	Recv.[]   (Dot Recv (Slice))                         → (Dot Recv "[]")
//	Recv.[]=  (Dot Recv (Slice)) then a bare `=` sibling → (Dot Recv "[]="), `=` dropped
//
// The trailing `=` of `[]=` lexes as its own token (it collides with the impl's
// `=` marker), so it is consumed here. Non-index forms — a plain binding, a
// `Recv.+` target, or a real index like `.[0]` (a non-empty Slice) — pass
// through untouched, so it is safe to call on every `let`/`operator` form.
func desugarOperatorTarget(argv []core.Node) []core.Node {
	if len(argv) == 0 {
		return argv
	}
	dot, ok := core.AsBranch(argv[0])
	if !ok || len(dot) != 3 {
		return argv
	}
	if head, ok := core.AsLeaf(dot[0]); !ok || string(head) != core.Dot {
		return argv
	}
	rhs, ok := core.AsBranch(dot[2])
	if !ok || len(rhs) != 1 {
		return argv // a name leaf (`+`) or a real index (`.[i]`) — nothing to do
	}
	if h, ok := core.AsLeaf(rhs[0]); !ok || string(h) != core.Slice {
		return argv
	}
	name := "[]"
	rest := argv[1:]
	if len(argv) >= 2 {
		if eq, ok := core.AsLeaf(argv[1]); ok && string(eq) == "=" {
			name, rest = "[]=", argv[2:]
		}
	}
	target := core.Branch{dot[0], dot[1], core.Leaf(name)}
	return append([]core.Node{target}, rest...)
}

// operatorBuiltins registers the `operator` declaration form.
func operatorBuiltins() map[string]core.StackEntry {
	return map[string]core.StackEntry{
		"operator": global(func(ctx core.Context, argv []core.Node) core.Value {
			// (operator Recv.OP Self Type… -> Ret) — an operator SIGNATURE. Mirrors
			// a named `method` sig, but the name is an operator symbol and the
			// index forms `[]`/`[]=` are canonicalized first. The adjacent
			// `(let Recv.OP self … = body)` clauses attach to it and land in the
			// struct's method table under OP.
			argv = desugarOperatorTarget(argv)
			if len(argv) < 1 {
				return ctx.Errorf(core.ErrArity, "'operator' requires a 'Recv.OP' signature: (operator Recv.OP Self Type… -> Result)")
			}
			recv, opName, named, ok := methodTarget(ctx, argv[0])
			if !ok {
				return core.TvNil
			}
			if !named {
				return ctx.Errorf(core.ErrBadForm, "'operator' needs a 'Recv.OP' target")
			}
			if !isOverloadableOperator(opName) {
				return ctx.Errorf(core.ErrBadForm, "'%s' is not an overloadable operator (one of + - * / mod < <= > >= == ~= [] []=)", opName)
			}
			params, ret, ok := splitArrow(argv[1:])
			if !ok || !isFunSig(core.Branch(params), ret) {
				return ctx.Errorf(core.ErrBadForm, "'operator %s.%s' declares a signature only: (operator %s.%s Self Type… -> Result); the implementation is (let %s.%s self … = body)", core.Inspect(recv), opName, core.Inspect(recv), opName, core.Inspect(recv), opName)
			}
			recvVal := recv.Evaluate(ctx)
			if recvVal.Kind != core.KindType {
				return ctx.Errorf(core.ErrType, "'operator' receiver must be a type or struct, got kind '%s'", recvVal.Kind)
			}
			return withSelfType(ctx, recvVal, func(sctx core.Context) core.Value {
				return registerSig(sctx, core.Inspect(recv)+"."+opName, opName, core.Branch(params), ret, false, "operator")
			})
		}),
	}
}

// withOverload wraps a builtin prefix operator so that when its first operand is
// a struct instance whose type overloads `op`, the call dispatches to that
// overload instead of the primitive. The first operand is evaluated exactly once
// — if no overload applies its value is handed to the base builtin as a literal,
// so a side-effecting operand is not evaluated twice.
func withOverload(op string, base core.Fun) core.Fun {
	return func(ctx core.Context, argv []core.Node) core.Value {
		if len(argv) == 0 {
			return base(ctx, argv)
		}
		recv := argv[0].Evaluate(ctx)
		if v, dispatched := dispatchOperator(ctx, op, recv, argv[1:]); dispatched {
			return v
		}
		return base(ctx, append([]core.Node{core.Lit(recv)}, argv[1:]...))
	}
}

// dispatchOperator invokes recv's `op` overload with the given operand nodes,
// pushing recv as the receiver, when recv is a struct instance whose type
// overloads op. Returns (result, true) on dispatch, (nil, false) otherwise.
func dispatchOperator(ctx core.Context, op string, recv core.Value, operands []core.Node) (core.Value, bool) {
	if recv.Kind != core.KindInstance {
		return core.TvNil, false
	}
	method, found := recv.Val.(*core.Instance).Struct.Methods[op]
	if !found {
		return core.TvNil, false
	}
	env := ctx.Env
	env.InstStack = append([]core.Value{recv}, env.InstStack...)
	defer func() { env.InstStack = env.InstStack[1:] }()
	return method(ctx, operands), true
}
