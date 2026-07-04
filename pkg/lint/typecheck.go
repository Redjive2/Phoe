package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"pho/pkg/annot"
	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/diag"
)

// Gradual type checking (Stage E). Declared types come from `--@` annotations:
// `(~type T)` on a var/const, `(~sig P… -> R)` on a function. The checker is
// flow-sensitive — inside `(if (x.Is? T) …)` the binding x is narrowed in each
// arm (occurrence typing). Everything routes through provableMismatch, the one
// gate enforcing the gradual guarantee: a diagnostic fires only when BOTH sides
// are Dynamic-free and not subtypes, so un-annotated code (and anything the
// checker can't pin down) is never flagged.

// funSig is a harvested function signature — one OVERLOAD of a callable
// (Features.md §9: a name may carry several signatures, distinguished by arity
// and parameter types).
type funSig struct {
	Params []*core.PhoType
	Result *core.PhoType
	// Min/Max bound the accepted ARGUMENT count: Min counts the required
	// params; Max is len(Params), or -1 when a trailing `(spread T)` accepts
	// any surplus. Annotation-harvested sigs are exact (Min = Max = arity).
	Min, Max int
	// Const marks the parse-time-constant `(const T)` slots (Features.md §1);
	// nil when the sig has none. Read by the clause checker.
	Const []bool
}

// exactSig builds a funSig with a fixed arity (no optional/spread slots) — the
// shape the annotation harvest produces.
func exactSig(params []*core.PhoType, result *core.PhoType) *funSig {
	return &funSig{Params: params, Result: result, Min: len(params), Max: len(params)}
}

// admitsArity reports whether the sig accepts n call arguments.
func (s *funSig) admitsArity(n int) bool {
	return n >= s.Min && (s.Max < 0 || n <= s.Max)
}

// sigIndex maps a callable name to its signature OVERLOADS, in source order.
type sigIndex map[string][]*funSig

// add appends one overload.
func (s sigIndex) add(name string, sig *funSig) { s[name] = append(s[name], sig) }

// one returns the sole signature of name — nil when the name is absent OR
// overloaded, so single-sig call paths stay precise and overloads stay gradual.
func (s sigIndex) one(name string) *funSig {
	if list := s[name]; len(list) == 1 {
		return list[0]
	}
	return nil
}

// forArity returns the first overload of name whose arity window admits n
// arguments, or nil. Declaration order mirrors the runtime's "clauses attach to
// the latest preceding signature" reading closely enough for typing.
func (s sigIndex) forArity(name string, n int) *funSig {
	for _, sig := range s[name] {
		if sig.admitsArity(n) {
			return sig
		}
	}
	return nil
}

// commonResult returns the result type every overload of name shares, or nil
// when the name is absent or the overloads disagree (the result then stays
// gradual — no false positive).
func (s sigIndex) commonResult(name string) *core.PhoType {
	list := s[name]
	if len(list) == 0 || list[0].Result == nil {
		return nil
	}
	r := list[0].Result
	for _, sig := range list[1:] {
		if sig.Result != r {
			return nil
		}
	}
	return r
}

// flowEnv maps a binding name to its currently-known type (narrowed within a
// branch). A name absent from the env is Dynamic.
type flowEnv map[string]*core.PhoType

func (f flowEnv) typeOf(name string) *core.PhoType {
	if t, ok := f[name]; ok {
		return t
	}
	return core.TypeDynamic
}

// narrowed returns a copy of f with name bound to t.
func (f flowEnv) narrowed(name string, t *core.PhoType) flowEnv {
	out := make(flowEnv, len(f)+1)
	for k, v := range f {
		out[k] = v
	}
	out[name] = t
	return out
}

func provableMismatch(actual, expected *core.PhoType) bool {
	if actual == nil || expected == nil || actual.IsGradual() {
		return false
	}
	// A generic type variable expected: the value must fit SOME instantiation of
	// the parameter — a subtype of its bound. It is a provable mismatch only when
	// the value is DISJOINT from the bound (no instantiation could accept it).
	// Disjointness, not subtype: a value narrower than the bound (e.g. `5` for a
	// Number-bounded var) is a perfectly good instantiation. An unbounded
	// parameter (bound = Unknown) is disjoint from nothing, so it never fires.
	if bound, ok := core.TypeVarBound(expected); ok {
		return !bound.IsGradual() && !bound.IsEmpty() && actual.And(bound).IsEmpty()
	}
	if expected.IsGradual() {
		return false
	}
	return !core.Subtype(actual, expected)
}

// litType parses one source-text token as a LITERAL singleton type — an atom
// (`:ok`), number (`5`), string (`"GET"`, quotes included), or bool
// (`True`/`False`). ok=false means it is not a literal (a bare identifier — a
// type name or variable). The same surface text reaches the checker from both
// the annotation harvest (de-quoted source) and AST leaves, so one parser
// serves resolveAnnotType, resolveTypeNode, and inferType.
func litType(s string) (*core.PhoType, bool) {
	switch {
	case len(s) > 1 && s[0] == ':':
		return core.AtomSingleton(s[1:]), true
	case core.IsStrLit(s):
		return core.StrSingleton(core.UnescapeStringLit(core.StrLitBody(s))), true
	case s == "True" || s == "False" || s == "true" || s == "false":
		return core.BoolSingleton(s == "True" || s == "true"), true
	case s == "none" || s == "Nil":
		return core.TypeNil, true
	case intLiteralRe.MatchString(s):
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return core.NumSingleton(n), true
		}
	}
	return nil, false
}

// typeEnv maps a user-declared alias name (from `(type Name T)`) to its
// resolved type, so the checker resolves named types alongside the builtins. A
// nil env is fine — only builtins resolve.
type typeEnv map[string]*core.PhoType

// resolveName resolves a bare type NAME: a user alias first, then a builtin.
// (Users cannot name a type after a builtin — Rebind rejects that — so the two
// namespaces are disjoint; the order only matters for forward references.)
func resolveName(name string, env typeEnv) (*core.PhoType, bool) {
	if t, ok := env[name]; ok {
		return t, true
	}
	return core.TypeByName(name)
}

// collectTypeAliases gathers `(type Name T)` declarations into a typeEnv, in
// source order so a later alias may reference an earlier one (they are
// non-recursive). An unresolvable body resolves to Dynamic — gradual-safe.
func collectTypeAliases(tree []ast.PNode) typeEnv {
	env := typeEnv{}
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok {
			continue
		}
		switch {
		case d.Head == "type" && d.Name != "" && d.Body != nil:
			env[d.Name] = resolveTypeNode(d.Body, env)
		case d.Head == "trait" && d.Name != "":
			// A named trait `(trait Name …)` resolves like `(type Name (Trait …))`.
			if br, ok := form.(*ast.PBranch); ok {
				env[d.Name] = resolveTraitNode(br, env)
			}
		}
	}
	return env
}

// collectTemplateVars binds each `(template …)` type parameter to a fresh core
// type variable (its bound resolved against env) so a generic struct/method's
// uses of the parameter resolve to a VARIABLE — checked by its bound and
// substituted at instantiation — rather than a bare gradual placeholder. Runs
// after collectTypeAliases so a bound may name a user alias. File-scoped for now
// (a parameter is visible file-wide); since variables are gradual, the scoping
// imprecision never causes a false positive.
func (w *walker) collectTemplateVars(tree []ast.PNode, env typeEnv) {
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || d.Head != "template" {
			continue
		}
		for _, p := range d.TemplateParams {
			var bound *core.PhoType
			if p.Bound != nil {
				bound = resolveTypeNode(p.Bound, env)
			}
			tv := core.TypeVar(p.Name, bound)
			env[p.Name] = tv
			// A struct bound: record its NAME so bound METHOD access can reach the
			// nominal struct's method table (the bound record carries only fields).
			if leaf, ok := p.Bound.(*ast.PLeaf); ok {
				if _, isStruct := w.fileScope.LookupStruct(leaf.Value); isStruct {
					if w.tvarBoundStruct == nil {
						w.tvarBoundStruct = map[*core.PhoType]string{}
					}
					w.tvarBoundStruct[tv] = leaf.Value
				}
			}
		}
	}
}

// resolveAnnotType maps a harvested annotation value to a type. A bare name is
// a string ("Number" or a user alias); a connective form is a (possibly nested)
// array (`(Or Number String)` → ["Or" "Number" "String"]). Unknown names/forms
// are Dynamic so they never produce a false positive.
func resolveAnnotType(v core.Value, env typeEnv) *core.PhoType {
	switch val := v.Val.(type) {
	case string:
		// The harvest delivers every scalar as its source TEXT: a literal
		// (`:ok`, `5`, `"GET"`, `True`) is a singleton; a bare name is a type.
		if lt, ok := litType(val); ok {
			return lt
		}
		if t, ok := resolveName(val, env); ok {
			return t
		}
	case *[]core.Value:
		return resolveAnnotArray(*val, env)
	}
	return core.TypeDynamic
}

func resolveAnnotArray(arr []core.Value, env typeEnv) *core.PhoType {
	if len(arr) == 0 {
		return core.TypeDynamic
	}
	head, ok := arr[0].Val.(string)
	if !ok {
		return core.TypeDynamic
	}
	switch head {
	case "Or":
		t := core.TypeNone
		for _, el := range arr[1:] {
			t = t.Or(resolveAnnotType(el, env))
		}
		return t
	case "And":
		t := core.TypeUnknown
		for _, el := range arr[1:] {
			t = t.And(resolveAnnotType(el, env))
		}
		return t
	case "Not":
		if len(arr) == 2 {
			return resolveAnnotType(arr[1], env).Not()
		}
	case "List":
		if len(arr) == 2 {
			return core.ListType(resolveAnnotType(arr[1], env))
		}
	case "Map":
		if len(arr) == 3 {
			return core.MapType(resolveAnnotType(arr[1], env), resolveAnnotType(arr[2], env))
		}
	case "Fun":
		if len(arr) == 3 {
			return core.ArrowType(resolveTypeList(arr[1], env), resolveAnnotType(arr[2], env))
		}
	case "Struct":
		// `Struct.{ T X U Y }` harvests as ["Struct" T "\"X\"" U "\"Y\""]; the
		// field names are quoted string source-text (the parser stringifies the
		// brace names), interleaved as Type name pairs.
		if len(arr)%2 == 1 {
			fields := map[string]*core.PhoType{}
			for i := 1; i+1 < len(arr); i += 2 {
				if name, ok := unquoteField(arr[i+1].Val); ok {
					fields[name] = resolveAnnotType(arr[i], env)
				}
			}
			return core.RecordType(fields)
		}
	}
	return core.TypeDynamic
}

// unquoteField reads a record field name from a `"X"` source-text string.
func unquoteField(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || !core.IsStrLit(s) {
		return "", false
	}
	return core.UnescapeStringLit(core.StrLitBody(s)), true
}

// resolveTypeNode resolves a type expression written in code (a guard's type
// argument): a literal, a bare name (builtin or user alias), or an Or/And/Not/
// List/Map/Fun connective branch.
func resolveTypeNode(n ast.PNode, env typeEnv) *core.PhoType {
	switch node := n.(type) {
	case *ast.PLeaf:
		if lt, ok := litType(node.Value); ok { // :ok / 5 / "GET" / True in a type position
			return lt
		}
		if t, ok := resolveName(node.Value, env); ok {
			return t
		}
	case *ast.PBranch:
		if node.Open == "(" && len(node.Children) >= 1 {
			if head, ok := node.Children[0].(*ast.PLeaf); ok {
				switch head.Value {
				case "Or":
					t := core.TypeNone
					for _, c := range node.Children[1:] {
						t = t.Or(resolveTypeNode(c, env))
					}
					return t
				case "And":
					t := core.TypeUnknown
					for _, c := range node.Children[1:] {
						t = t.And(resolveTypeNode(c, env))
					}
					return t
				case "Not":
					if len(node.Children) == 2 {
						return resolveTypeNode(node.Children[1], env).Not()
					}
				case "List":
					if len(node.Children) == 2 {
						return core.ListType(resolveTypeNode(node.Children[1], env))
					}
				case "Map":
					if len(node.Children) == 3 {
						return core.MapType(resolveTypeNode(node.Children[1], env), resolveTypeNode(node.Children[2], env))
					}
				case "Fun":
					if len(node.Children) == 3 {
						return core.ArrowType(resolveTypeNodeList(node.Children[1], env), resolveTypeNode(node.Children[2], env))
					}
				case "fun":
					// A function TYPE `(fun (P…) R)` — lowercase, PAREN params (the
					// value-level function syntax reused in a type position, e.g. a
					// generic sig `(method I.bind (Self (fun (I) O)) O)`).
					if len(node.Children) == 3 {
						var ptypes []*core.PhoType
						if pl, ok := node.Children[1].(*ast.PBranch); ok {
							for _, p := range pl.Children {
								ptypes = append(ptypes, resolveTypeNode(p, env))
							}
						}
						return core.ArrowType(ptypes, resolveTypeNode(node.Children[2], env))
					}
				case "Struct":
					// `Struct.{ T X U Y }` parses to (Struct T "X" U "Y") — Type
					// name pairs; the field-name keys are stringified by the parser.
					if len(node.Children)%2 == 1 {
						fields := map[string]*core.PhoType{}
						for i := 1; i+1 < len(node.Children); i += 2 {
							if lf, ok := node.Children[i+1].(*ast.PLeaf); ok {
								if name, ok := unquoteField(lf.Value); ok {
									fields[name] = resolveTypeNode(node.Children[i], env)
								}
							}
						}
						return core.RecordType(fields)
					}
				case "Trait":
					return resolveTraitNode(node, env)
				}
			}
		}
	}
	return core.TypeDynamic
}

// traitFormParts splits a `(Trait …)` or `(trait Name …)` form into its
// optional extends-list and member nodes — skipping the name for the lowercase
// named form. Mirrors the runtime's splitTraitArgs: a leading parenthesized
// group whose head is NOT a member keyword is the extends-list.
func traitFormParts(node *ast.PBranch) (extends *ast.PBranch, members []ast.PNode) {
	if len(node.Children) < 1 {
		return nil, nil
	}
	rest := node.Children[1:]
	if headIdent(node) == "trait" && len(rest) > 0 {
		rest = rest[1:] // skip the name
	}
	if len(rest) > 0 {
		if ext, ok := rest[0].(*ast.PBranch); ok && ext.Open == "(" && !isTraitMemberNode(ext) {
			return ext, rest[1:]
		}
	}
	return nil, rest
}

// isTraitMemberNode reports whether a parenthesized form is a trait MEMBER
// (method/property/static) rather than the extends-list.
func isTraitMemberNode(br *ast.PBranch) bool {
	switch headIdent(br) {
	case "method", "property", "static", "=":
		return true
	}
	return false
}

// resolveTraitNode builds a real trait type from a `(Trait …)` / `(trait Name
// …)` AST form: the requirement names + arities (defaults/bodies are irrelevant
// to the type). Extended traits are resolved by name and flattened in. `static`
// members parse but aren't yet part of the enforced requirement set.
func resolveTraitNode(node *ast.PBranch, env typeEnv) *core.PhoType {
	info := &core.TraitInfo{
		Methods:    map[string]core.TraitMethod{},
		Properties: map[string]core.TraitProperty{},
	}
	extends, members := traitFormParts(node)
	if extends != nil {
		for _, ref := range extends.Children {
			if lf, ok := ref.(*ast.PLeaf); ok {
				if t, ok := resolveName(lf.Value, env); ok {
					if ti, isTrait := core.TraitOf(t); isTrait {
						for k, v := range ti.Methods {
							info.Methods[k] = v
						}
						for k, v := range ti.Properties {
							info.Properties[k] = v
						}
					}
				}
			}
		}
	}
	for _, sub := range members {
		sb, ok := sub.(*ast.PBranch)
		if !ok || len(sb.Children) < 2 {
			continue
		}
		name := traitMemberName(sb)
		if name == "" {
			continue
		}
		switch headIdent(sb) {
		case "method":
			arity := 0
			var params []*core.PhoType
			if len(sb.Children) >= 3 {
				if args, ok := sb.Children[2].(*ast.PBranch); ok && len(args.Children) > 0 {
					arity = len(args.Children) - 1 // exclude self
					for _, p := range args.Children[1:] {
						params = append(params, resolveTypeNode(p, env))
					}
				}
			}
			// A trait method sig `(method self.m (self …) R)` carries its result
			// type R (Children[3]) — read so a bound trait method call types.
			var result *core.PhoType
			if len(sb.Children) >= 4 {
				result = resolveTypeNode(sb.Children[3], env)
			}
			info.Methods[name] = core.TraitMethod{Arity: arity, Params: params, Result: result}
		case "property":
			p := core.TraitProperty{}
			// A typed property `(property (Type Self.name) get)` carries its value
			// type, so a bound trait property access `b.name` types as Type.
			if tb, ok := sb.Children[1].(*ast.PBranch); ok && len(tb.Children) == 2 {
				p.Type = resolveTypeNode(tb.Children[0], env)
			}
			for _, c := range sb.Children[2:] {
				if lf, ok := c.(*ast.PLeaf); ok {
					switch lf.Value {
					case "get":
						p.Get = true
					case "set":
						p.Set = true
					}
				}
			}
			info.Properties[name] = p
		}
	}
	return core.TraitType(info)
}

// structMissingTraitMembers reports which of a trait's required members a struct
// fails to provide — using the linter's collected member surface (methods on
// si.Methods, fields and computed members on si.Fields). A required method is
// also satisfied by a universal method; a property by a field OR struct
// property of that name. decided=false when the struct is unknown (gradual).
func (w *walker) structMissingTraitMembers(scope *Scope, structName string, info *core.TraitInfo) (missing []string, decided bool) {
	si, ok := scope.LookupStruct(structName)
	if !ok {
		return nil, false
	}
	for name, m := range info.Methods {
		if m.Default != nil {
			continue
		}
		if _, ok := si.Methods[name]; ok || isUniversalMember(name) {
			continue
		}
		missing = append(missing, name)
	}
	for name, p := range info.Properties {
		if p.GetDefault != nil && p.SetDefault != nil {
			continue
		}
		_, isField := si.Fields[name]
		_, isMethod := si.Methods[name]
		if !isField && !isMethod {
			missing = append(missing, name)
		}
	}
	return missing, true
}

// checkTraitArg checks a value flowing into a trait-typed slot. If the value is
// a known struct that doesn't satisfy the trait, it flags the missing members;
// if it is a struct that satisfies (or an unknown/gradual value), it is clean;
// otherwise it falls back to the generic mismatch check (so a non-struct
// concrete value — a number, etc. — is still flagged). Returns true when it
// fully handled the slot (so the caller skips the generic check).
func (w *walker) checkTraitArg(arg ast.PNode, info *core.TraitInfo, label string) bool {
	sh := w.inferShape(w.checkScope, arg)
	if sh.Kind != ShapeInstance || sh.Owner == "" {
		return false // not a known struct — let the generic check decide
	}
	missing, decided := w.structMissingTraitMembers(w.checkScope, sh.Owner, info)
	if !decided {
		return true // unknown struct ⇒ gradual, no diagnostic
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		w.emit(Diagnostic{
			File: w.file, Span: arg.GetSpan(), Severity: SeverityError, Code: diag.ErrType,
			Message: fmt.Sprintf("'%s' does not satisfy %s — missing %s",
				sh.Owner, label, strings.Join(missing, ", ")),
		})
	}
	return true
}

// checkTraitArgOK is checkTraitArg's SILENT twin, for overload probing: it
// reports whether the trait slot handled-and-accepted the argument (a known
// struct with every member, or an undecidable struct — gradual) without
// emitting anything. false falls the caller through to the generic mismatch
// probe, mirroring checkTraitArg's false.
func (w *walker) checkTraitArgOK(arg ast.PNode, info *core.TraitInfo) bool {
	sh := w.inferShape(w.checkScope, arg)
	if sh.Kind != ShapeInstance || sh.Owner == "" {
		return false // not a known struct — the generic probe decides
	}
	missing, decided := w.structMissingTraitMembers(w.checkScope, sh.Owner, info)
	return !decided || len(missing) == 0
}

// traitMemberName extracts the member name from a `Self.Name` receiver pattern,
// unwrapping a typed property wrapper `(Type Self.Name)` first.
func traitMemberName(sb *ast.PBranch) string {
	node := sb.Children[1]
	if tb, ok := node.(*ast.PBranch); ok && len(tb.Children) == 2 {
		node = tb.Children[1] // typed property `(Type Self.Name)` → the receiver pattern
	}
	if dot, ok := node.(*ast.PDot); ok {
		if rhs, ok := dot.RHS.(*ast.PLeaf); ok {
			return rhs.Value
		}
	}
	return ""
}

// resolveTypeNodeList resolves a `[T1 T2 …]` list literal of type expressions.
func resolveTypeNodeList(n ast.PNode, env typeEnv) []*core.PhoType {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "[" {
		return nil
	}
	out := make([]*core.PhoType, 0, len(br.Children))
	for _, c := range br.Children {
		out = append(out, resolveTypeNode(c, env))
	}
	return out
}

func resolveTypeList(v core.Value, env typeEnv) []*core.PhoType {
	ptr, ok := v.Val.(*[]core.Value)
	if !ok {
		return nil
	}
	out := make([]*core.PhoType, 0, len(*ptr))
	for _, el := range *ptr {
		out = append(out, resolveAnnotType(el, env))
	}
	return out
}

func firstResolved(v core.Value, env typeEnv) *core.PhoType {
	if ptr, ok := v.Val.(*[]core.Value); ok && len(*ptr) > 0 {
		return resolveAnnotType((*ptr)[0], env)
	}
	return core.TypeDynamic
}

// memberOwners expands a method/property receiver type NAME into the type
// surfaces the member registers on. A finite primitive union (e.g.
// Collection = String|List|Map) expands to its member type names — mirroring
// the runtime's union-receiver expansion (core.MemberKeys) so the linter
// resolves a Collection member accessed on a concrete list/string/map. Any
// other name is itself.
func memberOwners(owner string) []string {
	if t, ok := core.TypeByName(owner); ok {
		if names := core.MemberTypeNames(t); names != nil {
			return names
		}
	}
	return []string{owner}
}

var intLiteralRe = regexp.MustCompile(`^-?[0-9]+$`)

// inferType infers the type of an expression node under a flow env. Literals
// get their precise type; an identifier gets its (possibly narrowed) flow type;
// a call to an annotated function gets that function's result type; everything
// else is Dynamic.
func inferType(n ast.PNode, sigs sigIndex, flow flowEnv) *core.PhoType {
	switch node := n.(type) {
	case *ast.PLeaf:
		v := node.Value
		if v == "Nil" || v == "none" {
			return core.TypeNil
		}
		// A literal infers its precise singleton (:ok / 5 / "GET" / True); a
		// singleton is always a subtype of its base type, so this only adds
		// precision — it never introduces a false positive.
		if lt, ok := litType(v); ok {
			return lt
		}
		return flow.typeOf(v)
	case *ast.PBranch:
		if node.Open == "[" {
			elem := core.TypeNone
			for _, c := range node.Children {
				elem = elem.Or(inferType(c, sigs, flow))
			}
			return core.ListType(elem)
		}
		if node.Open == "{" {
			k, v := core.TypeNone, core.TypeNone
			for i, c := range node.Children {
				if i%2 == 0 {
					k = k.Or(inferType(c, sigs, flow))
				} else {
					v = v.Or(inferType(c, sigs, flow))
				}
			}
			return core.MapType(k, v)
		}
		if node.Open == "(" && len(node.Children) >= 1 {
			if head, ok := node.Children[0].(*ast.PLeaf); ok {
				// Prefer the overload the call's ARITY selects; overloads that
				// all agree on a result still type an arity-unmatched call.
				if sig := sigs.forArity(head.Value, len(node.Children)-1); sig != nil && sig.Result != nil {
					return sig.Result
				}
				if r := sigs.commonResult(head.Value); r != nil {
					return r
				}
			}
		}
	}
	return core.TypeDynamic
}

// harvestFieldTypes records every local typed struct's field types onto its
// structInfo.FieldTypes, resolving each field's declared-type expression under
// the alias env. Untyped (bare) fields are skipped. Imported structs aren't
// covered yet — like method signatures, cross-package field types arrive once
// PackageStructs grows a type harvest of its own.
func (w *walker) harvestFieldTypes(scope *Scope, tree []ast.PNode, env typeEnv) {
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || d.Head != "struct" || d.Name == "" {
			continue
		}
		si, ok := scope.LookupStruct(d.Name)
		if !ok {
			continue
		}
		for _, f := range d.Fields {
			if f.Type == nil {
				continue
			}
			if si.FieldTypes == nil {
				si.FieldTypes = map[string]*core.PhoType{}
			}
			si.FieldTypes[f.Name] = resolveTypeNode(f.Type, env)
		}
	}
}

// harvestPropertyTypes records every local struct-attached typed property's
// declared value type onto its owner's structInfo.PropertyTypes, resolving the
// `(property (Type Recv.name) …)` type under the alias env. Untyped properties,
// free-standing ones (no owner), and primitive/union receivers are skipped — a
// struct property mirrors a field for member-access typing (`inst.prop`).
func (w *walker) harvestPropertyTypes(scope *Scope, tree []ast.PNode, env typeEnv) {
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || d.Head != "property" || d.Owner == "" || d.Name == "" || d.PropType == nil {
			continue
		}
		si, ok := scope.LookupStruct(d.Owner)
		if !ok {
			continue
		}
		if si.PropertyTypes == nil {
			si.PropertyTypes = map[string]*core.PhoType{}
		}
		si.PropertyTypes[d.Name] = resolveTypeNode(d.PropType, env)
	}
}

// harvestFieldShapes records, for each local struct's typed fields, the struct a
// field is declared as (FieldStructOwner). Unlike FieldTypes (consumed by the
// gradual checker in checkTypes), this feeds SHAPE inference during the main
// reference walk, so it must run after collect (all structs known) and before
// that walk. No type env is needed — it resolves struct names through the scope.
func (w *walker) harvestFieldShapes(scope *Scope, tree []ast.PNode) {
	resolve := w.localFieldResolve(scope)
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || d.Head != "struct" || d.Name == "" {
			continue
		}
		si, ok := scope.LookupStruct(d.Name)
		if !ok {
			continue
		}
		for _, f := range d.Fields {
			if f.Type == nil {
				continue
			}
			if sh, ok := fieldStructShape(f.Type, resolve); ok {
				if si.FieldStructOwner == nil {
					si.FieldStructOwner = map[string]Shape{}
				}
				si.FieldStructOwner[f.Name] = sh
			}
		}
	}
}

// fieldStructShape resolves a field's declared type to the instance shape that
// member access through the field navigates to: a struct name (via resolve), or
// the single struct of a nullable union `(Or Node Nil)`. ok=false when the type
// isn't, or doesn't contain exactly one, struct — keeping ambiguous shapes out
// of inference.
func fieldStructShape(typeNode ast.PNode, resolve func(ast.PNode) (Shape, bool)) (Shape, bool) {
	if br, ok := typeNode.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) >= 2 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok && head.Value == "Or" {
			var found Shape
			has := false
			for _, c := range br.Children[1:] {
				if sh, ok := fieldStructShape(c, resolve); ok {
					if has {
						return Shape{}, false // more than one struct ⇒ ambiguous
					}
					found, has = sh, true
				}
			}
			return found, has
		}
	}
	return resolve(typeNode)
}

// localFieldResolve resolves a field type node to an instance shape in the LOCAL
// context: a bare local struct (`Node`), or a `pkg.Struct` qualified reference
// to an exported struct of an imported package (OwnerPkg set so navigation
// crosses the import boundary).
func (w *walker) localFieldResolve(scope *Scope) func(ast.PNode) (Shape, bool) {
	return func(n ast.PNode) (Shape, bool) {
		switch t := n.(type) {
		case *ast.PLeaf:
			if def, _, ok := scope.Lookup(t.Value); ok && def.Kind == DefStruct {
				return Shape{Kind: ShapeInstance, Owner: t.Value}, true
			}
		case *ast.PDot:
			alias, aok := t.LHS.(*ast.PLeaf)
			member, mok := t.RHS.(*ast.PLeaf)
			if aok && mok {
				if def, _, found := scope.Lookup(alias.Value); found && def.Kind == DefImport && def.Path != "" {
					if _, ok := w.structsFor(def.Path)[member.Value]; ok {
						return Shape{Kind: ShapeInstance, Owner: member.Value, OwnerPkg: def.Path}, true
					}
				}
			}
		}
		return Shape{}, false
	}
}

// exprType is inferType extended with struct member-access typing: `inst.F` on a
// typed struct instance yields field F's declared type (FieldTypes). The free
// inferType can't do this — it has no struct tables — so the walker resolves the
// receiver's shape over the file scope and reads the owner's FieldTypes.
func (w *walker) exprType(n ast.PNode, sigs sigIndex, flow flowEnv) *core.PhoType {
	if dot, ok := n.(*ast.PDot); ok {
		if rhs, ok := dot.RHS.(*ast.PLeaf); ok {
			// Per-instance type arguments: if the receiver has a record type (an
			// instantiated generic instance carries its type args as concrete field
			// types), resolve the field from it — it overrides the struct's shared
			// declared FieldTypes, which for a generic field is only the bare var.
			if lt := w.exprType(dot.LHS, sigs, flow); lt != nil {
				if ft := core.FieldsOf(lt); ft != nil {
					if t, found := ft[rhs.Value]; found {
						return t
					}
				}
				// A value typed as a BOUNDED type variable may use its bound's
				// members: resolve a field through the bound's record shape (so a
				// generic `(template (Shape B))` param's `b.field` types as the
				// field's declared type). Method members need the nominal struct's
				// table, which the structural bound doesn't carry — see notes.
				if bound, ok := core.TypeVarBound(lt); ok {
					if ft := core.FieldsOf(bound); ft != nil {
						if t, found := ft[rhs.Value]; found {
							return t
						}
					}
					// A TRAIT bound: resolve a required property's declared type.
					if info, isTrait := core.TraitOf(bound); isTrait {
						if p, found := info.Properties[rhs.Value]; found && p.Type != nil {
							return p.Type
						}
					}
				}
			}
			sh := w.inferShape(w.checkScope, dot.LHS)
			if si, ok := w.resolveStruct(w.checkScope, sh); ok {
				if t, found := si.FieldTypes[rhs.Value]; found {
					return t
				}
				if t, found := si.PropertyTypes[rhs.Value]; found {
					return t
				}
			}
		}
	}
	// A method call `(x.M args…)` has M's declared RESULT type, so it propagates
	// into a const / argument like any other call.
	if br, ok := n.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) >= 1 {
		if dot, ok := br.Children[0].(*ast.PDot); ok {
			if rhs, ok := dot.RHS.(*ast.PLeaf); ok {
				sh := w.inferShape(w.checkScope, dot.LHS)
				owner := sh.Owner
				if owner == "" {
					// A bounded type-variable receiver `(b.method …)` resolves the
					// method through its bound: a STRUCT bound via the nominal
					// struct's method table, a TRAIT bound via the trait's required
					// methods (which carry the declared result type).
					if lt := w.exprType(dot.LHS, sigs, flow); lt != nil {
						if bn, ok := w.tvarBoundStruct[lt]; ok {
							owner = bn
						}
						if bound, ok := core.TypeVarBound(lt); ok {
							if info, isTrait := core.TraitOf(bound); isTrait {
								if m, found := info.Methods[rhs.Value]; found && m.Result != nil {
									return m.Result
								}
							}
						}
					}
				}
				if sig := w.methodSigFor(w.checkScope, owner, rhs.Value); sig != nil && sig.Result != nil {
					if r, ok := w.instantiateResultVar(sig.Result, sig.Params, br.Children[1:], sigs, flow); ok {
						return r
					}
					return sig.Result
				}
			}
		}
	}
	// A generic function call whose RESULT is a type PARAMETER that also appears
	// among the PARAMETERS instantiates to the matching argument's type — the
	// parametric-polymorphism win: `(fun id (I) I)` → `(id 5)` : Number. The sig's
	// param and result resolve to the SAME interned type variable, so a pointer
	// match locates the argument that fixes the result.
	if br, ok := n.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) >= 2 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			for _, sig := range sigs[head.Value] {
				if r, ok := w.instantiateResultVar(sig.Result, sig.Params, br.Children[1:], sigs, flow); ok {
					return r
				}
			}
		}
	}
	// A `(fun (params) body)` literal has the arrow type params→body-type, so a
	// higher-order generic call can extract its result (the `bind`/`apply`
	// pattern). Its params are unannotated (gradual); the result is the body's
	// inferred type.
	if br, ok := n.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) == 3 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok && head.Value == "fun" {
			if params, ok := br.Children[1].(*ast.PBranch); ok && params.Open == "(" {
				ptypes := make([]*core.PhoType, len(params.Children))
				for i := range ptypes {
					ptypes[i] = core.TypeDynamic
				}
				return core.ArrowType(ptypes, w.exprType(br.Children[2], sigs, flow))
			}
		}
	}
	// A struct construction `(Struct "field" value …)` has an INSTANTIATED record
	// type: its generic fields resolved to the constructed values' types, so
	// `c.field` resolves to the type argument. (A non-generic struct yields the
	// same record its declared field types would.)
	if br, ok := n.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) >= 1 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok && looksLikeIdentifier(head.Value) {
			if def, _, found := w.checkScope.Lookup(head.Value); found && def.Kind == DefStruct {
				if si, ok := w.checkScope.LookupStruct(head.Value); ok {
					if rec := w.instanceRecordType(br, si, sigs, flow); rec != nil {
						return rec
					}
				}
			}
		}
	}

	t := inferType(n, sigs, flow)
	// A struct VARIABLE/expression whose precise type the literal-driven
	// inferType can't see (Dynamic) gets its struct record, so a struct-shaped
	// argument is checked against a declared struct/record/primitive type.
	if t == nil || t.IsGradual() {
		if rec := w.shapeRecordType(w.inferShape(w.checkScope, n)); rec != nil {
			return rec
		}
	}
	return t
}

// instantiateResultVar instantiates a call's result when the callee's RESULT is
// a type variable, from the arguments in the positions that fix it: a parameter
// that IS the result variable contributes its argument's type (parametric
// result — `id`), and a function-typed parameter `(fun (…) O)` whose result IS
// the result variable contributes its argument function's result type (the
// higher-order `(fun apply (I (fun (I) O)) O)` / `bind` pattern). The
// contributions are joined. Returns (result, false) when nothing fixes it.
func (w *walker) instantiateResultVar(result *core.PhoType, params []*core.PhoType, args []ast.PNode, sigs sigIndex, flow flowEnv) (*core.PhoType, bool) {
	if result == nil || !core.IsTypeVar(result) {
		return nil, false
	}
	res, matched := core.TypeNone, false
	for i, p := range params {
		if i >= len(args) {
			break
		}
		switch {
		case p == result:
			if at := w.exprType(args[i], sigs, flow); at != nil {
				res, matched = res.Or(at), true
			}
		default:
			// A function-typed parameter whose result is the variable: take the
			// argument function's result type.
			if ar, ok := core.ArrowResult(p); ok && ar == result {
				if aar, ok := core.ArrowResult(w.exprType(args[i], sigs, flow)); ok && aar != nil {
					res, matched = res.Or(aar), true
				}
			}
		}
	}
	return res, matched
}

// shapeToType maps an inferred runtime SHAPE to the broad set-theoretic type of
// every value with that shape — Number for a num, List for any list, Struct for
// any struct instance, etc. It is deliberately COARSE: a list-shaped value's
// element type, a num-shaped value's exact value, and a struct instance's
// identity are all unknown here. Returns nil for an unknown shape. Because it
// drops refinement, callers must compare it by DISJOINTNESS (could ANY value of
// this shape inhabit the target?), never by subtyping — see typeMismatch.
func shapeToType(sh Shape) *core.PhoType {
	switch sh.Kind {
	case ShapeNum:
		return core.TypeNumber
	case ShapeString:
		return core.TypeString
	case ShapeArray:
		return core.TypeList
	case ShapeBool:
		return core.TypeBoolean
	case ShapeDict:
		return core.TypeDict
	case ShapeNil:
		return core.TypeNil
	case ShapeAtom:
		return core.TypeAtom
	case ShapeChar:
		return core.TypeChar
	case ShapeFun:
		return core.TypeFunction
	case ShapeInstance:
		return core.TypeStruct
	}
	return nil
}

// structRecord returns a struct's PRECISE type — an open record built from its
// declared field types — or nil when the struct isn't fully + precisely typed.
// A struct yields a record only if EVERY field has a non-gradual declared type:
// an untyped or struct-typed field resolves to Dynamic, and recordSubtype on a
// required gradual field would be unsound (a Dynamic value-field is not a
// subtype of a concrete expected one). Such structs stay coarse (TypeStruct via
// the bridge). The record is STRUCTURAL — two structs with identical fields
// share it (Pho's runtime is duck-typed, so this matches member dispatch) — and
// cached on the structInfo.
func (w *walker) structRecord(si *structInfo) *core.PhoType {
	if si == nil {
		return nil
	}
	if si.recordBuilt {
		return si.recordType
	}
	si.recordBuilt = true
	if len(si.FieldTypes) != len(si.Fields) {
		return nil // at least one field is untyped
	}
	fields := make(map[string]*core.PhoType, len(si.FieldTypes))
	for name, t := range si.FieldTypes {
		if t == nil || t.IsGradual() {
			return nil // a struct-typed/Dynamic field — keep this struct coarse
		}
		fields[name] = t
	}
	si.recordType = core.RecordType(fields)
	return si.recordType
}

// instanceRecordType builds the INSTANTIATED record type of a struct
// construction `(Struct "field" value …)`: each declared field type, with a
// generic (type-variable) field resolved to its constructed value's type — the
// per-construction type argument. So `(const c Box.{ v 5 })` gives `c` the record
// `{ v: Number }` and `c.v` resolves to Number downstream, not to the bare
// variable. A generic field is instantiated to the value's coarse (base) shape
// type — Number, not the singleton `5` — so a later field write isn't
// false-flagged. Returns nil unless every field resolves precisely (a gradual
// field would make the record unsound), keeping such instances coarse.
func (w *walker) instanceRecordType(br *ast.PBranch, si *structInfo, sigs sigIndex, flow flowEnv) *core.PhoType {
	if si == nil || len(si.FieldTypes) != len(si.Fields) {
		return nil // not fully typed
	}
	fields := make(map[string]*core.PhoType, len(si.Fields))
	for name, t := range si.FieldTypes {
		fields[name] = t // declared type (concrete field, or an un-instantiated var)
	}
	for i := 1; i+1 < len(br.Children); i += 2 {
		nameLeaf, ok := br.Children[i].(*ast.PLeaf)
		if !ok {
			continue
		}
		fname, ok := unquoteField(nameLeaf.Value)
		if !ok {
			continue
		}
		if core.IsTypeVar(fields[fname]) {
			vt := shapeToType(w.inferShape(w.checkScope, br.Children[i+1]))
			if vt == nil {
				vt = w.exprType(br.Children[i+1], sigs, flow)
			}
			fields[fname] = vt
		}
	}
	for _, t := range fields {
		if t == nil || t.IsGradual() {
			return nil // an un-inferrable field — keep the instance coarse
		}
	}
	return core.RecordType(fields)
}

// shapeRecordType is the precise type of a struct-instance shape: the owner
// struct's record (nil for a non-instance shape, an unknown struct, or one that
// isn't fully typed). Lets exprType give a struct variable a real type.
func (w *walker) shapeRecordType(sh Shape) *core.PhoType {
	if sh.Kind != ShapeInstance {
		return nil
	}
	si, ok := w.resolveStruct(w.checkScope, sh)
	if !ok {
		return nil
	}
	return w.structRecord(si)
}

// addStructTypes binds each fully-typed local struct's name to its record in the
// type env, so a struct name in an annotation (`--@ (~sig (Point) …)`, a field
// type) resolves to the struct's shape instead of Dynamic. A user `(type Name
// …)` alias of the same name takes precedence. Must run AFTER harvestFieldTypes
// (so the records exist) but BEFORE signatures/declared types are resolved.
func (w *walker) addStructTypes(env typeEnv, tree []ast.PNode) {
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || d.Head != "struct" || d.Name == "" {
			continue
		}
		if _, taken := env[d.Name]; taken {
			continue
		}
		if si, ok := w.checkScope.LookupStruct(d.Name); ok {
			if rec := w.structRecord(si); rec != nil {
				env[d.Name] = rec
			}
		}
	}
}

// typeMismatch reports whether `node`'s value provably cannot inhabit
// `expected`, and the actual type to name in the diagnostic. It first tries the
// PRECISE type (exprType, compared by subtyping in provableMismatch). When that
// is gradual but the value's SHAPE is known, it falls back to the shape↔type
// bridge: the shape's broad type compared by DISJOINTNESS — fire only when NO
// value of that shape could inhabit expected. Disjointness (not subtyping) is
// what keeps the gradual guarantee here: because the shape omits refinement (a
// list's element type, a number's exact value, a struct's identity), a value
// that the shape can't pin down can still satisfy `expected`, so we must never
// fire merely because the coarse shape type isn't a subtype. This lets a
// struct/list/num variable be caught against an incompatible declared type with
// no annotation, while a `5`/`(List Number)`/record-typed slot stays silent.
func (w *walker) typeMismatch(node ast.PNode, expected *core.PhoType, sigs sigIndex, flow flowEnv) (*core.PhoType, bool) {
	actual := w.exprType(node, sigs, flow)
	if provableMismatch(actual, expected) {
		return actual, true
	}
	if (actual == nil || actual.IsGradual()) && expected != nil && !expected.IsGradual() {
		if st := shapeToType(w.inferShape(w.checkScope, node)); st != nil && st.And(expected).IsEmpty() {
			return st, true
		}
	}
	return actual, false
}

// narrowGuard recognizes a type-test guard over a bound name — `(x.Is? T)` —
// and returns the name and the tested type. There is no prefix `(Is? x T)`
// form: membership is a method only, so the guard is always the dot form.
func narrowGuard(cond ast.PNode, env typeEnv) (name string, t *core.PhoType, ok bool) {
	br, isBr := cond.(*ast.PBranch)
	if !isBr || br.Open != "(" || len(br.Children) != 2 {
		return "", nil, false
	}
	// (x.Is? T): head is the dot x.Is?, single argument T.
	if dot, isDot := br.Children[0].(*ast.PDot); isDot {
		if rhs, ok := dot.RHS.(*ast.PLeaf); ok && rhs.Value == "is?" {
			if lhs, ok := dot.LHS.(*ast.PLeaf); ok {
				return lhs.Value, resolveTypeNode(br.Children[1], env), true
			}
		}
	}
	return "", nil, false
}

// checkTypes is the gradual checker pass: harvest `--@` annotations, then check
// var/const initializers and call arguments under a flow env that narrows on
// type-test guards. Runs its own (memoized, shared) annotation evaluation.
func (w *walker) checkTypes(tree []ast.PNode) {
	// Shape inference defaults to the file scope; checkFunBody swaps it to a body
	// scope while checking inside a function.
	w.checkScope = w.fileScope
	ensured := false
	ensure := func() {
		if !ensured {
			annot.EnsureDefault(resolveImportPath(w.file, "std/annot"))
			ensured = true
		}
	}

	// Collect named type aliases `(type Name T)` first, so everything
	// downstream — annotations, guards — resolves user names alongside builtins.
	env := collectTypeAliases(tree)

	// Record typed struct fields `(struct Name.{ T F … })` onto their owners'
	// FieldTypes, so member access `inst.F` types as T. (First pass: a generic
	// struct's type-variable field still resolves to Dynamic here — its variable
	// isn't bound until collectTemplateVars below; a second pass then fixes it.)
	w.harvestFieldTypes(w.checkScope, tree, env)

	// Record typed properties `(property (Type Recv.name) …)` onto their owners'
	// PropertyTypes, so member access `inst.prop` types as Type — the property
	// analogue of FieldTypes.
	w.harvestPropertyTypes(w.checkScope, tree, env)

	// Bind each fully-typed struct's name to its record type, so a struct name
	// in an annotation resolves to the struct rather than Dynamic. After the
	// field harvest (records need FieldTypes), before signatures are resolved —
	// and before template bounds, so a struct/trait BOUND resolves to its record.
	w.addStructTypes(env, tree)

	// Bind generic type parameters `(template …)` to type variables — after
	// addStructTypes so a struct/trait bound resolves to its record (not Dynamic),
	// enabling the bound check and bound-member access for struct-bounded params.
	w.collectTemplateVars(tree, env)

	// Second field pass: now that the type variables are bound, a generic struct's
	// variable-typed field resolves to its variable (overwriting the Dynamic the
	// first pass recorded), so instantiation and the construction bound check see it.
	w.harvestFieldTypes(w.checkScope, tree, env)

	sigs := sigIndex{}
	base := flowEnv{}

	// Inline type signatures feed the checker alongside the legacy `--@`
	// annotations (TypeSignatures.md Phase 3). A `(fun add (T…) R)` sig resolves
	// to the same funSig the `--@ (~sig …)` harvest produced, and a typed
	// binding `(const (T x) v)` records x's declared type — both BEFORE the
	// annotation loop, so a legacy annotation for the same name still wins if
	// (transitionally) both are present.
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok {
			continue
		}
		switch {
		case d.IsSig && d.Head == "fun" && d.Name != "":
			if sig := inlineFunSig(d, env); sig != nil {
				sigs.add(d.Name, sig)
			}
		case (d.Head == "var" || d.Head == "const") && form != nil:
			w.checkInlineTypedBinds(form, env, sigs, base)
		}
	}

	for _, form := range tree {
		br, ok := form.(*ast.PBranch)
		if !ok || len(br.Annotations) == 0 {
			continue
		}
		d, ok := declOf(br)
		if !ok {
			continue
		}
		ensure()
		entries := harvestEntries(br)
		switch {
		case d.Head == "fun" && d.Name != "":
			if sig := sigFromEntries(entries, env); sig != nil {
				sigs[d.Name] = []*funSig{sig} // a legacy annotation wins over inline sigs
			}
		case (d.Head == "var" || d.Head == "const") && len(d.Binds) > 0:
			declared := typeFromEntries(entries, env)
			if declared == nil {
				continue
			}
			for _, b := range d.Binds {
				base[b.Name] = declared
				if b.Value == nil {
					continue
				}
				if info, isTrait := core.TraitOf(declared); isTrait && w.checkTraitArg(b.Value, info, declared.Name()) {
					continue
				}
				if actual, bad := w.typeMismatch(b.Value, declared, sigs, base); bad {
					w.emit(Diagnostic{
						File: w.file, Span: b.Value.GetSpan(), Severity: SeverityError, Code: diag.ErrType,
						Message: fmt.Sprintf("'%s' is declared '%s' but its value has type '%s'", b.Name, declared.Name(), actual.Name()),
					})
				}
			}
		}
	}

	// Forward type propagation: bind each top-level CONST's inferred type, so a
	// later reference or call argument is typed from it — `(const a (f x))` makes
	// `a` f's result type, catching `(g a)` against an incompatible parameter
	// even though nothing is annotated at the use site. CONST only: a var is
	// reassignable, so its initializer type isn't a stable contract. A gradual
	// result is skipped (stays Dynamic, never fires). Source order lets an
	// earlier const inform a later one; annotated bindings are already in base
	// (their declared type) and skipped.
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || d.Head != "const" {
			continue
		}
		for _, b := range d.Binds {
			if _, has := base[b.Name]; has || b.Value == nil {
				continue
			}
			if t := w.exprType(b.Value, sigs, base); t != nil && !t.IsGradual() {
				base[b.Name] = t
			}
		}
	}

	// Assignment checking validates `(= x v)` against x's DECLARED type.
	w.declared = base

	// Return-type checking: each annotated function's return points (the body's
	// tail expressions plus every explicit `(return …)`) must inhabit the
	// declared result type.
	for _, form := range tree {
		if d, ok := declOf(form); ok && d.Head == "fun" && d.Name != "" && !d.IsSig && d.ArgList != nil && d.Body != nil {
			if sig := sigs.forArity(d.Name, clauseParamCount(d)); sig != nil && sig.Result != nil {
				w.checkReturns(sigs, d, sig)
			}
		}
	}

	for _, form := range tree {
		w.checkFlow(sigs, env, base, form)
	}
}

// checkReturns checks that every return point of an annotated function inhabits
// its declared result type: the body's syntactic tail expression(s) (the
// implicit return) and every explicit `(return X)`. Parameters are bound to
// their declared types so a return that uses them resolves. Un-inferable
// returns are Dynamic ⇒ gradual (no false positive).
func (w *walker) checkReturns(sigs sigIndex, d topLevelDecl, sig *funSig) {
	// Infer shapes against the body's scope (params/locals), like checkBody, so a
	// returned local struct/value is seen.
	if bodyScope, ok := w.bodyScopes[d.Body]; ok {
		prev := w.checkScope
		w.checkScope = bodyScope
		defer func() { w.checkScope = prev }()
	}
	flow := flowEnv{}
	if al, ok := d.ArgList.(*ast.PBranch); ok {
		for i, p := range al.Children {
			if lf, isLeaf := p.(*ast.PLeaf); isLeaf && i < len(sig.Params) {
				flow[lf.Value] = sig.Params[i]
			}
		}
	}
	// A trait result is satisfied structurally (by the returned struct's
	// members), not by type subtyping — route it through the struct-vs-trait
	// check, exactly as the argument/var/assign sites do, so a struct record
	// (which carries fields, not methods) can't false-positive against it.
	traitResult, isTrait := core.TraitOf(sig.Result)
	rets := tailExprs(d.Body)
	collectReturns(d.Body, &rets)
	for _, r := range rets {
		if isTrait {
			w.checkTraitArg(r, traitResult, sig.Result.Name())
			continue
		}
		if actual, bad := w.typeMismatch(r, sig.Result, sigs, flow); bad {
			w.emit(Diagnostic{
				File: w.file, Span: r.GetSpan(), Severity: SeverityError, Code: diag.ErrType,
				Message: fmt.Sprintf("'%s' returns '%s' but its signature declares '%s'",
					d.Name, actual.Name(), sig.Result.Name()),
			})
		}
	}
}

// tailExprs returns the expression(s) a body evaluates to — its implicit return
// value. A `do` block's value is its last form; an `if`/`unless`'s value is each
// arm's tail (an else-less form may also fall through to Nil, which is left
// unchecked — leniency keeps it gradual-safe); anything else is its own value.
// `(return …)` tails are left to collectReturns.
func tailExprs(n ast.PNode) []ast.PNode {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "(" || len(br.Children) == 0 {
		return []ast.PNode{n}
	}
	switch headIdent(br) {
	case "do":
		if len(br.Children) >= 2 {
			return tailExprs(br.Children[len(br.Children)-1])
		}
		return nil
	case "if", "unless":
		f := parseIfForm(br, headIdent(br), headIdent(br) == "if")
		var out []ast.PNode
		for _, b := range f.Branches {
			if b.Expr != nil {
				out = append(out, tailExprs(b.Expr)...)
			}
		}
		if f.Else != nil {
			out = append(out, tailExprs(f.Else)...)
		}
		return out
	case "return":
		return nil // handled by collectReturns
	}
	return []ast.PNode{n}
}

// collectReturns appends the value expression of every explicit `(return X)` in
// n to out, without descending into nested functions/methods (their returns are
// their own).
func collectReturns(n ast.PNode, out *[]ast.PNode) {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "(" || len(br.Children) == 0 {
		return
	}
	if h, ok := br.Children[0].(*ast.PLeaf); ok {
		switch h.Value {
		case "return":
			if len(br.Children) >= 2 {
				*out = append(*out, br.Children[1])
			}
			return
		case "fun", "method":
			return // a nested function — its returns belong to it
		case "let":
			if d, ok := declOf(br); ok && d.IsClause {
				return // a nested implementation CLAUSE — its returns belong to it
			}
		case "=":
			if len(br.Children) == 4 {
				return // a nested fun/method implementation — its returns belong to it
			}
		}
	}
	for _, c := range br.Children {
		collectReturns(c, out)
	}
}

// checkFlow walks expressions under a flow env, checking call arguments and
// narrowing on if/unless guards. Quoted data (not a `(`-branch) is skipped.
func (w *walker) checkFlow(sigs sigIndex, env typeEnv, flow flowEnv, n ast.PNode) {
	// A member access `b.member` on a value typed as a bounded type variable: the
	// member must be provided by the bound (or be universal). This catches a typo
	// inside a polymorphic body, where `b` is only known to be a subtype of the
	// bound — reached both bare (`b.member`) and as a call head (`(b.member …)`,
	// whose head PDot is visited as a child).
	if dot, ok := n.(*ast.PDot); ok {
		w.checkBoundMember(sigs, flow, dot)
		w.checkFlow(sigs, env, flow, dot.LHS)
		return
	}
	br, ok := n.(*ast.PBranch)
	if !ok {
		return
	}
	if br.Open == "(" && len(br.Children) >= 1 {
		switch head := br.Children[0].(type) {
		case *ast.PLeaf:
			switch head.Value {
			case "if", "unless":
				w.checkIfFlow(sigs, env, flow, br, head.Value)
				return
			case "fun":
				// Descend INTO the body with its own scope + params typed, so
				// in-body uses check too (checkBody recurses; skip the generic one).
				if d, ok := declOf(br); ok && !d.IsSig && d.Body != nil {
					var params []*core.PhoType
					if sig := sigs.forArity(d.Name, clauseParamCount(d)); sig != nil {
						params = sig.Params
					}
					w.checkBody(sigs, env, flow, d, params, 0)
					return
				}
			case "method":
				if d, ok := declOf(br); ok && d.Body != nil {
					var params []*core.PhoType
					if sig := w.methodSigFor(w.checkScope, d.Owner, d.Name); sig != nil {
						params = sig.Params
					}
					w.checkBody(sigs, env, flow, d, params, 1)
					return
				}
			case "let":
				// A `(let name (patterns) [where g] = body)` IMPLEMENTATION CLAUSE
				// (declOf normalizes it to Head fun/method): check its body with its
				// own scope + sig-typed params, like the fun/method arms. Bare-binder
				// pattern slots get the sig's param type; literal/destructure patterns
				// bind via the reference walk and stay gradual here. Value-binding
				// lets fall through to the generic descent (their initializers are
				// checked by checkInlineTypedBinds / const propagation).
				if d, ok := declOf(br); ok && d.IsClause && d.Body != nil {
					var params []*core.PhoType
					selfOffset := 0
					if d.Head == "method" {
						selfOffset = 1
						if sig := w.methodSigFor(w.checkScope, d.Owner, d.Name); sig != nil {
							params = sig.Params
						}
					} else if sig := sigs.forArity(d.Name, clauseParamCount(d)); sig != nil {
						params = sig.Params
					}
					w.checkBody(sigs, env, flow, d, params, selfOffset)
					return
				}
			case "=":
				// A 4-child `(= …)` normalizes to a fun/method IMPL: check its body
				// with its own scope + params, like the fun/method arms above. A
				// 3-child reassignment (declOf ok=false) falls through to checkAssignFlow.
				if d, ok := declOf(br); ok && d.Body != nil {
					var params []*core.PhoType
					selfOffset := 0
					if d.Head == "method" {
						selfOffset = 1
						if sig := w.methodSigFor(w.checkScope, d.Owner, d.Name); sig != nil {
							params = sig.Params
						}
					} else if sig := sigs.forArity(d.Name, clauseParamCount(d)); sig != nil {
						params = sig.Params
					}
					w.checkBody(sigs, env, flow, d, params, selfOffset)
					return
				}
				w.checkAssignFlow(sigs, flow, br)
			default:
				if overloads, found := sigs[head.Value]; found {
					w.checkCallOverloads(sigs, flow, head, br.Children[1:], overloads)
				} else if def, _, found := w.checkScope.Lookup(head.Value); found && def.Kind == DefStruct {
					if si, ok := w.checkScope.LookupStruct(head.Value); ok {
						w.checkConstructFields(sigs, flow, br, si, head.Value)
					}
				}
			}
		case *ast.PDot:
			// A method call `(x.M args…)` — check args against x's methodsig.
			w.checkMethodCall(sigs, flow, br, head)
		}
	}
	for _, ch := range br.Children {
		w.checkFlow(sigs, env, flow, ch)
	}
}

// checkBody type-checks INSIDE a function/method body, reusing the body scope
// the reference walk stashed so shape inference resolves the body's params and
// locals (and a local correctly shadows a top-level name). The params are bound
// to their signature types (params is the sig's parameter types; selfOffset is 1
// for a method, whose first arg-slot is the un-typed `self`); the body's own
// consts then propagate. Without a sig, params stay Dynamic but the correct
// scope + const propagation still apply.
func (w *walker) checkBody(sigs sigIndex, env typeEnv, flow flowEnv, d topLevelDecl, params []*core.PhoType, selfOffset int) {
	bodyScope, ok := w.bodyScopes[d.Body]
	if !ok {
		for _, ch := range d.Branch.Children { // no stashed scope — don't skip anything
			w.checkFlow(sigs, env, flow, ch)
		}
		return
	}
	bodyFlow := make(flowEnv, len(flow)+len(params))
	for k, v := range flow { // closures see the enclosing bindings
		bodyFlow[k] = v
	}
	if al, ok := d.ArgList.(*ast.PBranch); ok {
		for i, t := range params {
			if j := i + selfOffset; j < len(al.Children) {
				if lf, isLeaf := al.Children[j].(*ast.PLeaf); isLeaf {
					bodyFlow[lf.Value] = t
				}
			}
		}
	}
	prev := w.checkScope
	w.checkScope = bodyScope
	if d.Guard != nil {
		// A clause's `where` guard runs with the same params in scope as the body.
		w.checkFlow(sigs, env, bodyFlow, d.Guard)
	}
	w.propagateBodyConsts(sigs, bodyFlow, d.Body)
	w.checkFlow(sigs, env, bodyFlow, d.Body)
	w.checkScope = prev
}

// propagateBodyConsts updates a body's flow for its own statements, in source
// order: a CONST gains its inferred type (like the top-level pass); a VAR clears
// any binding of that name — it's reassignable (no stable type) and must
// override a same-named parameter. Only the body's direct statements are
// scanned (a `(do …)`'s children, or the lone body expression).
func (w *walker) propagateBodyConsts(sigs sigIndex, flow flowEnv, body ast.PNode) {
	stmts := []ast.PNode{body}
	if br, ok := body.(*ast.PBranch); ok && br.Open == "(" && headIdent(br) == "do" {
		stmts = br.Children[1:]
	}
	for _, s := range stmts {
		d, ok := declOf(s)
		if !ok {
			continue
		}
		switch d.Head {
		case "const":
			for _, b := range d.Binds {
				if b.Value == nil {
					continue
				}
				if t := w.exprType(b.Value, sigs, flow); t != nil && !t.IsGradual() {
					flow[b.Name] = t
				}
			}
		case "var":
			for _, b := range d.Binds {
				delete(flow, b.Name)
			}
		}
	}
}

// clauseParamCount is the number of parameter slots a clause/impl declares —
// its dispatch arity (a method's receiver slot included; callers offset it).
func clauseParamCount(d topLevelDecl) int {
	if br, ok := asList(d.ArgList); ok {
		n := len(br.Children)
		if d.Head == "method" && n > 0 {
			n-- // the receiver slot is not a call argument
		}
		return n
	}
	return 0
}

// checkCallOverloads checks a call against a callable's signature OVERLOADS
// (Features.md §9): the overloads whose arity window admits the argument count
// are candidates. No candidate → an arity diagnostic. One candidate → per-arg
// checking with precise messages (checkCallArgs). Several candidates → the call
// is fine if ANY accepts every argument; when the argument types are known and
// NONE accepts, it can never dispatch → no-matching-overload. provableMismatch
// keeps the gradual guarantee: an unknown argument type accepts everywhere.
func (w *walker) checkCallOverloads(sigs sigIndex, flow flowEnv, head *ast.PLeaf, args []ast.PNode, overloads []*funSig) {
	var cands []*funSig
	for _, sig := range overloads {
		if sig.admitsArity(len(args)) {
			cands = append(cands, sig)
		}
	}
	if len(cands) == 0 {
		w.emit(Diagnostic{
			File: w.file, Span: head.Span, Severity: SeverityError, Code: "arity-mismatch",
			Message: fmt.Sprintf("'%s' takes %s, but this call passes %d", head.Value, arityWindowLabel(overloads), len(args)),
		})
		return
	}
	if len(cands) == 1 {
		w.checkCallArgs(sigs, flow, args, cands[0], "'"+head.Value+"'")
		return
	}
	for _, sig := range cands {
		if w.sigAcceptsArgs(sigs, flow, args, sig) {
			return // some overload takes this call — fine
		}
	}
	w.emit(Diagnostic{
		File: w.file, Span: head.Span, Severity: SeverityError, Code: "no-matching-overload",
		Message: fmt.Sprintf("no overload of '%s' accepts these argument types", head.Value),
	})
}

// sigAcceptsArgs reports whether every argument can inhabit its parameter slot
// under sig — provableMismatch-based, so an unknown argument type accepts.
func (w *walker) sigAcceptsArgs(sigs sigIndex, flow flowEnv, args []ast.PNode, sig *funSig) bool {
	for i, arg := range args {
		j := i
		if j >= len(sig.Params) {
			if sig.Max >= 0 {
				break
			}
			j = len(sig.Params) - 1 // surplus args check against the spread slot
		}
		if j < 0 {
			break
		}
		param := sig.Params[j]
		if info, isTrait := core.TraitOf(param); isTrait && w.checkTraitArgOK(arg, info) {
			continue
		}
		if _, bad := w.typeMismatch(arg, param, sigs, flow); bad {
			return false
		}
	}
	return true
}

// arityWindowLabel renders the argument counts a set of overloads accepts —
// "2 arguments", "1–3 arguments", "at least 1 argument" — for arity messages.
func arityWindowLabel(overloads []*funSig) string {
	lo, hi := -1, 0
	for _, sig := range overloads {
		if lo < 0 || sig.Min < lo {
			lo = sig.Min
		}
		if sig.Max < 0 {
			hi = -1
		} else if hi >= 0 && sig.Max > hi {
			hi = sig.Max
		}
	}
	plural := func(n int) string {
		if n == 1 {
			return "1 argument"
		}
		return fmt.Sprintf("%d arguments", n)
	}
	switch {
	case hi < 0:
		return fmt.Sprintf("at least %s", plural(lo))
	case lo == hi:
		return plural(lo)
	default:
		return fmt.Sprintf("%d–%s", lo, plural(hi))
	}
}

// checkCallArgs checks positional arguments against a signature's parameters.
// label names the callee for diagnostics (e.g. "'f'" or "method 'M'").
func (w *walker) checkCallArgs(sigs sigIndex, flow flowEnv, args []ast.PNode, sig *funSig, label string) {
	for i, arg := range args {
		if i >= len(sig.Params) {
			break
		}
		param := sig.Params[i]
		if info, isTrait := core.TraitOf(param); isTrait && w.checkTraitArg(arg, info, param.Name()) {
			continue
		}
		if actual, bad := w.typeMismatch(arg, param, sigs, flow); bad {
			w.emit(Diagnostic{
				File: w.file, Span: arg.GetSpan(), Severity: SeverityError, Code: diag.ErrType,
				Message: fmt.Sprintf("argument %d to %s has type '%s', but %s expects '%s'",
					i+1, label, actual.Name(), label, param.Name()),
			})
		}
	}
}

// checkBoundMember flags a member access `b.member` whose receiver `b` is typed
// as a bounded type variable when the bound provably does NOT provide `member`.
// This is sound only inside a polymorphic body: `b` is some unknown subtype of
// the bound, so only the bound's members (plus the universal ones) are
// guaranteed. It fires ONLY when the bound is fully enumerable — a local struct
// or a trait — so a member reachable some other way (an imported extension, an
// unknown bound) can never be false-flagged.
func (w *walker) checkBoundMember(sigs sigIndex, flow flowEnv, dot *ast.PDot) {
	rhs, ok := dot.RHS.(*ast.PLeaf)
	if !ok || isUniversalMember(rhs.Value) {
		return
	}
	lt := w.exprType(dot.LHS, sigs, flow)
	bound, isVar := core.TypeVarBound(lt)
	if !isVar {
		return
	}
	enumerable, has := w.boundMemberKnown(lt, bound, rhs.Value)
	if !enumerable || has {
		return
	}
	w.emit(Diagnostic{
		File: w.file, Span: rhs.Span, Severity: SeverityError, Code: "unknown-member",
		Message: fmt.Sprintf("'%s' is not a member of the bound '%s' of this type parameter", rhs.Value, bound.Name()),
	})
}

// boundMemberKnown reports whether a bounded variable's bound can be fully
// enumerated (a local struct or a trait) and, if so, whether it provides
// `member`. A non-enumerable bound returns (false, false) so the caller stays
// silent — the gradual guarantee.
func (w *walker) boundMemberKnown(lt, bound *core.PhoType, member string) (enumerable, has bool) {
	if name, ok := w.tvarBoundStruct[lt]; ok {
		if si, ok := w.checkScope.LookupStruct(name); ok {
			_, f := si.Fields[member]
			_, m := si.Methods[member]
			_, p := si.PropertyTypes[member]
			return true, f || m || p
		}
	}
	if info, ok := core.TraitOf(bound); ok {
		_, m := info.Methods[member]
		_, p := info.Properties[member]
		return true, m || p
	}
	return false, false
}

// checkConstruction checks a struct construction `(Struct "field" value …)` —
// each field VALUE against its declared field type — so a value that provably
// cannot inhabit the field's type fires. For a generic struct field (a type
// variable) this enforces the parameter's BOUND at construction, the analogue of
// the call-site bound check. Untyped fields (no declared type) never fire, and
// the gradual guarantee holds because typeMismatch decides by disjointness.
func (w *walker) checkConstructFields(sigs sigIndex, flow flowEnv, br *ast.PBranch, si *structInfo, owner string) {
	if si == nil {
		return
	}
	for i := 1; i+1 < len(br.Children); i += 2 {
		nameLeaf, ok := br.Children[i].(*ast.PLeaf)
		if !ok {
			continue
		}
		fname, ok := unquoteField(nameLeaf.Value)
		if !ok {
			continue
		}
		ft := si.FieldTypes[fname]
		if ft == nil {
			continue
		}
		if actual, bad := w.typeMismatch(br.Children[i+1], ft, sigs, flow); bad {
			w.emit(Diagnostic{
				File: w.file, Span: br.Children[i+1].GetSpan(), Severity: SeverityError, Code: diag.ErrType,
				Message: fmt.Sprintf("field '%s' of '%s' has type '%s', but got '%s'",
					fname, owner, ft.Name(), actual.Name()),
			})
		}
	}
}

// checkMethodCall checks a `(x.M args…)` call against M's harvested methodsig,
// when x's struct is statically known. Unknown receivers / un-annotated methods
// are gradual (no check).
func (w *walker) checkMethodCall(sigs sigIndex, flow flowEnv, br *ast.PBranch, dot *ast.PDot) {
	rhs, ok := dot.RHS.(*ast.PLeaf)
	if !ok {
		return
	}
	sh := w.inferShape(w.checkScope, dot.LHS)
	if sh.Kind != ShapeInstance || sh.Owner == "" {
		return
	}
	// Resolve M's signature through the receiver shape, so a call on an IMPORTED
	// struct instance checks against the owner package's harvested MethodSigs too.
	sig := w.methodSigForShape(w.checkScope, sh, rhs.Value)
	if sig == nil {
		return
	}
	w.checkCallArgs(sigs, flow, br.Children[1:], sig, "method '"+rhs.Value+"'")
}

// checkAssignFlow checks `(= x v)`: v must inhabit x's DECLARED type. Only a
// bare-name target with a declared type is checked (a dotted target is a
// field/property write, handled elsewhere); an un-annotated x is gradual.
func (w *walker) checkAssignFlow(sigs sigIndex, flow flowEnv, br *ast.PBranch) {
	if len(br.Children) != 3 {
		return
	}
	name, ok := br.Children[1].(*ast.PLeaf)
	if !ok {
		return
	}
	declared := w.declared.typeOf(name.Value)
	val := br.Children[2]
	if info, isTrait := core.TraitOf(declared); isTrait && w.checkTraitArg(val, info, declared.Name()) {
		return
	}
	if actual, bad := w.typeMismatch(val, declared, sigs, flow); bad {
		w.emit(Diagnostic{
			File: w.file, Span: val.GetSpan(), Severity: SeverityError, Code: diag.ErrType,
			Message: fmt.Sprintf("cannot assign a value of type '%s' to '%s', declared '%s'",
				actual.Name(), name.Value, declared.Name()),
		})
	}
}

// checkIfFlow walks an if/unless form, narrowing the tested binding in each arm.
// `unless` inverts the guard (its then-arm runs when the condition is false).
func (w *walker) checkIfFlow(sigs sigIndex, env typeEnv, flow flowEnv, br *ast.PBranch, keyword string) {
	f := parseIfForm(br, keyword, keyword == "if")
	invert := keyword == "unless"

	for _, b := range f.Branches {
		w.checkFlow(sigs, env, flow, b.Cond) // the condition may contain calls
		armFlow := flow
		if name, t, ok := narrowGuard(b.Cond, env); ok {
			pos := t
			if invert {
				pos = t.Not()
			}
			armFlow = flow.narrowed(name, flow.typeOf(name).And(pos))
		}
		if b.Expr != nil {
			w.checkFlow(sigs, env, armFlow, b.Expr)
		}
	}

	if f.Else != nil {
		elseFlow := flow
		// Narrow the else only for a single-branch form, where it is exactly
		// the complement of the one guard.
		if len(f.Branches) == 1 {
			if name, t, ok := narrowGuard(f.Branches[0].Cond, env); ok {
				neg := t.Not()
				if invert {
					neg = t
				}
				elseFlow = flow.narrowed(name, flow.typeOf(name).And(neg))
			}
		}
		w.checkFlow(sigs, env, elseFlow, f.Else)
	}
}

// harvestEntries evaluates a form's annotations (memoized) and flattens entries.
func harvestEntries(br *ast.PBranch) []annot.Entry {
	var out []annot.Entry
	for _, res := range annot.Default().EvaluateBranch(br) {
		out = append(out, res.Entries...)
	}
	return out
}

// inlineFunSig resolves an inline `(fun name (T…) R)` SIGNATURE's parameter and
// result type expressions to a funSig — the same shape sigFromEntries builds
// from a `--@ (~sig …)` annotation. The `(var/spread/optional/const T)` slot
// modifiers are read here: they set the sig's arity window (Min/Max) and Const
// mask, and the TYPE plumbing sees only the inner type. (TypeSignatures.md
// Phase 3; Features.md §1.)
func inlineFunSig(d topLevelDecl, env typeEnv) *funSig {
	params, ok := asList(d.ArgList)
	if !ok {
		return nil
	}
	sig := readSigParams(params.Children, env)
	sig.Result = resolveTypeNode(d.Body, env)
	return sig
}

// inlineMethodSig resolves an inline `(method R.M (Self P…) R)` SIGNATURE to a
// funSig for the method-call surface. Param 0 is the RECEIVER type (the type of
// `self`) and is NOT part of the call-argument signature — it is dropped, like
// methodSigFromEntries does for the annotation form. (TypeSignatures.md Phase 3.)
func inlineMethodSig(d topLevelDecl, env typeEnv) *funSig {
	params, ok := asList(d.ArgList)
	if !ok || len(params.Children) == 0 {
		return nil
	}
	sig := readSigParams(params.Children[1:], env)
	sig.Result = resolveTypeNode(d.Body, env)
	return sig
}

// readSigParams resolves a signature's param slots (modifiers unwrapped) into a
// funSig with its arity window: required slots count into Min; `(optional T
// [else D])` slots are omittable; a `(spread T)` slot lifts Max to unbounded.
func readSigParams(slots []ast.PNode, env typeEnv) *funSig {
	sig := &funSig{Max: len(slots)}
	for _, p := range slots {
		mod := sigParamMod(p)
		sig.Params = append(sig.Params, resolveTypeNode(sigParamType(p), env))
		sig.Const = append(sig.Const, mod == "const")
		switch mod {
		case "spread":
			sig.Max = -1
		case "optional":
			// omittable — not required
		default:
			sig.Min++
		}
	}
	return sig
}

// sigParamMod returns the modifier keyword wrapping a signature param slot
// ("var"/"spread"/"optional"/"const"/"disc"), or "" for a bare type slot.
func sigParamMod(p ast.PNode) string {
	if br, ok := asList(p); ok && len(br.Children) >= 2 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			switch head.Value {
			case "var", "spread", "optional", "const", "disc":
				return head.Value
			}
		}
	}
	return ""
}

// checkInlineTypedBinds records the declared type of each inline typed binding
// `(const (T x) v)` / `(var (T x) v)` into base and checks its value against
// it — the inline counterpart of the `--@ (~type T)` handling. Bare (untyped)
// binds are left alone. (TypeSignatures.md Phase 3.)
func (w *walker) checkInlineTypedBinds(form ast.PNode, env typeEnv, sigs sigIndex, base flowEnv) {
	br, ok := asList(form)
	if !ok {
		return
	}
	// The binding layout differs by form: const/var are (name value) pairs whose
	// name slot may be the grouped typed form `(Type x)`; a `let` form is an
	// optional `var` modifier then `[Type] name = value` bindings (the ungrouped
	// typed form carries the type as a separate leading node). Collect
	// (typeNode, nameNode, valueNode) so the type check below is shared — typeNode
	// is nil for a grouped/bare slot and read out of the name slot instead.
	type binding struct{ typeNode, nameNode, valueNode ast.PNode }
	var binds []binding
	if headIdent(br) == "let" {
		i := 1
		if i < len(br.Children) {
			if mod, ok := br.Children[i].(*ast.PLeaf); ok && mod.Value == "var" {
				i++
			}
		}
		for i < len(br.Children) {
			targetNode, valueNode, next, ok := letBinding(br.Children, i)
			if !ok {
				break
			}
			// The type (if any) lives inside a grouped `(Type name)` target,
			// extracted below; a destructuring target has no scalar annotation.
			binds = append(binds, binding{nil, targetNode, valueNode})
			i = next
		}
	} else {
		for i := 1; i+1 < len(br.Children); i += 2 {
			binds = append(binds, binding{nil, br.Children[i], br.Children[i+1]})
		}
	}
	for _, bd := range binds {
		// Resolve the declared type + the name leaf: the ungrouped `let` form
		// supplies the type as a separate node; the grouped `(Type x)` slot holds
		// both. A bare name slot with no type is untyped — skipped.
		typeNode, nameNode := bd.typeNode, bd.nameNode
		if typeNode == nil {
			inner, ok := asList(bd.nameNode)
			if !ok || len(inner.Children) != 2 {
				continue // bare name, or a destructuring pattern — untyped here
			}
			// `(var name)` is a mutable binder, not a typed `(Type name)` — skip.
			if h, ok := inner.Children[0].(*ast.PLeaf); ok && h.Value == "var" {
				continue
			}
			typeNode, nameNode = inner.Children[0], inner.Children[1]
		}
		name, ok := nameNode.(*ast.PLeaf)
		if !ok {
			continue
		}
		declared := resolveTypeNode(typeNode, env)
		if declared == nil {
			continue
		}
		base[name.Value] = declared
		value := bd.valueNode
		if info, isTrait := core.TraitOf(declared); isTrait && w.checkTraitArg(value, info, declared.Name()) {
			continue
		}
		if actual, bad := w.typeMismatch(value, declared, sigs, base); bad {
			w.emit(Diagnostic{
				File: w.file, Span: value.GetSpan(), Severity: SeverityError, Code: diag.ErrType,
				Message: fmt.Sprintf("'%s' is declared '%s' but its value has type '%s'", name.Value, declared.Name(), actual.Name()),
			})
		}
	}
}

func typeFromEntries(entries []annot.Entry, env typeEnv) *core.PhoType {
	for _, e := range entries {
		if e.Key == "type" {
			return resolveAnnotType(e.Value, env)
		}
	}
	return nil
}

func sigFromEntries(entries []annot.Entry, env typeEnv) *funSig {
	var isSig bool
	var params, result core.Value
	for _, e := range entries {
		switch e.Key {
		case "kind":
			if s, _ := e.Value.Val.(string); s == "sig" {
				isSig = true
			}
		case "params":
			params = e.Value
		case "result":
			result = e.Value
		}
	}
	if !isSig {
		return nil
	}
	// New `~sig` form: params is the `(…)` list, result is a single type.
	return exactSig(resolveTypeList(params, env), resolveAnnotType(result, env))
}
