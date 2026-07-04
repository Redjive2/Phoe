package lint

import (
	"fmt"
	"sort"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
)

// Effect tracking — see Doc/PlanV1/Effects.md.
//
// Phase 2 (this file): the effect MODEL plus the data-extraction layer that
// reads a callable's DECLARED effect surface — whether its name marks it
// effectful (`name!`) and which parameter slots are mutable (`(var name)`,
// slot 0 being a method's receiver). No inference or enforcement yet; Phase 3
// wires these into the walk to infer effects and check the `!` convention.
//
// Locked decisions (Effects.md §8): a method must declare `(var self)` to
// mutate self (Q2 = required); mutation of a locally-owned binding is pure
// (Q4 = exempt).

// An effect is one distinct thing a callable does to the world, tracked by its
// own NAME rather than lumped into a coarse bucket. Three families of name:
//
//   - a PRIMITIVE effect — a Go-side dep-bridge operation, named for the
//     operation itself ("io-write", "os-open", "random-int"), not a coarse "io".
//   - a CALLED `!`-function — recorded by its own name ("print-line!"), so the
//     set names exactly which effectful functions run. Cross-module is free: the
//     `!` on the spelled name carries the effect without resolving the callee.
//   - a value MUTATION — mutates-self / mutates-arg / mutates-free.
//
// This granularity is what lets a hover/diagnostic say WHICH effects a function
// has (and is the substrate for per-effect signatures later). The spelled name
// stays binary — `!` means "has ≥1 environmental effect" — so the granularity
// lives in the inferred set, never in the spelling (Effects.md §8 Q1).
const (
	fxMutatesSelf = "mutates-self" // (= self …) through a (var self) receiver — drives '='
	fxMutatesArg  = "mutates-arg"  // (= p …) through a (var p) parameter       — drives '='
	fxMutatesFree = "mutates-free" // (= <module-var> …) — module-global write  — drives '!'
)

// effectSet is the set of named effects a callable performs. The empty set is
// pure. A nil set is a valid empty set for reads; adders require a seeded map.
type effectSet map[string]bool

func (s effectSet) has(name string) bool { return s[name] }

// add records one effect. The receiver must be non-nil — scanEffects seeds it.
func (s effectSet) add(name string) { s[name] = true }

// union returns a fresh set with every effect of both s and o.
func (s effectSet) union(o effectSet) effectSet {
	out := make(effectSet, len(s)+len(o))
	for k := range s {
		out[k] = true
	}
	for k := range o {
		out[k] = true
	}
	return out
}

func (s effectSet) pure() bool { return len(s) == 0 }

// needsEquals reports whether the set requires the SELF-mutation suffix '=' — it
// mutates a value it was given (a `(var self)` receiver or a `(var arg)` param).
func (s effectSet) needsEquals() bool { return s[fxMutatesSelf] || s[fxMutatesArg] }

// needsBang reports whether the set requires the ENVIRONMENTAL suffix '!' — it
// has any effect that isn't a self/arg value-mutation: a module-global write, a
// primitive io/randomness op, or a call to a `!`-function.
func (s effectSet) needsBang() bool {
	for k := range s {
		if k != fxMutatesSelf && k != fxMutatesArg {
			return true
		}
	}
	return false
}

// String renders the set for hovers/diagnostics: "pure" or a sorted comma list
// of effect names ("io-write, mutates-self, print-line!").
func (s effectSet) String() string {
	if s.pure() {
		return "pure"
	}
	names := make([]string, 0, len(s))
	for k := range s {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Effects are FREEFORM and per-function: there is no curated table of "known
// effectful primitives". The `!` convention is the sole source of truth — a
// function marked `!` is effectful by declaration, and an effect is simply the
// NAME of a `!`-function that gets called. The environmental primitives (io,
// randomness, …) enter the system only through the stdlib's own `!`-named
// wrappers (`print-line!`, `random/float!`), which the checker trusts; nothing
// needs to enumerate the Go-side dep bridge. (Structural mutations — self/arg/
// module-var writes — are still inferred directly from `(= …)`; see scanEffects.)

// declaredEffectful reports whether a callable's NAME marks it effectful — by
// convention an identifier ending in '!' (a private `#name!` counts too). This
// is the spelled contract the Phase-3 checker validates against inference.
func declaredEffectful(name string) bool {
	return core.IsEffectName(name)
}

// isVarParam reports whether a parameter slot is the mutable `(var name)` form
// (distinct from `(optional name)` / `(spread name)`).
func isVarParam(p ast.PNode) bool {
	br, ok := p.(*ast.PBranch)
	if !ok || br.Open != "(" || len(br.Children) != 2 {
		return false
	}
	h, ok := br.Children[0].(*ast.PLeaf)
	return ok && h.Value == "var"
}

// paramMutability returns the per-parameter mutability of a parameter list:
// out[i] is true iff slot i is a `(var name)` parameter. A non-list (malformed)
// argument list yields nil. For a method the receiver is slot 0.
func paramMutability(argList ast.PNode) []bool {
	br, ok := argList.(*ast.PBranch)
	if !ok {
		return nil
	}
	out := make([]bool, len(br.Children))
	for i, p := range br.Children {
		out[i] = isVarParam(p)
	}
	return out
}

// receiverMutable reports whether a method's receiver (parameter slot 0) is
// declared `(var self)` — the static contract that permits mutating self.
func receiverMutable(argList ast.PNode) bool {
	mut := paramMutability(argList)
	return len(mut) > 0 && mut[0]
}

// checkVarSelfNeedsEquals enforces that a method signature whose receiver is
// `(var Self)` — declaring that the method mutates self — carries the self-
// mutation suffix `=` on its name. A mutable receiver without `=` is a contract
// error (Effects.md): declaring the receiver mutable but not marking the name
// self-mutating. Mirrors the runtime check in pkg/builtins registerSig.
func (w *walker) checkVarSelfNeedsEquals(d topLevelDecl) {
	if !receiverMutable(d.ArgList) || core.IsSelfEffectName(d.Name) {
		return
	}
	w.emit(Diagnostic{
		File:     w.file,
		Span:     d.NameSpan,
		Severity: SeverityError,
		Code:     "var-self-needs-equals",
		Message:  fmt.Sprintf("a method with a '(var Self)' receiver mutates self — its name must end in '=' (e.g. '%s=')", d.Name),
	})
}

// checkVarParams enforces where a `(var …)` parameter may appear. It is a
// SIGNATURE-only construct marking the mutable receiver: a signature may write
// `(var Self)` (and nothing else); an IMPLEMENTATION writes no `(var …)` at all —
// it names its receiver plainly `self` (or matches it with a pattern) and reads
// mutability from the signature's `(var Self)`. Mirrors the runtime rejection in
// pkg/builtins compileClause / parseArgList / evalSigParams (Effects.md).
func (w *walker) checkVarParams(argList ast.PNode, isSig bool) {
	br, ok := argList.(*ast.PBranch)
	if !ok {
		return
	}
	for _, p := range br.Children {
		if !isVarParam(p) {
			continue
		}
		if !isSig {
			// An implementation never carries `(var …)`.
			w.emit(Diagnostic{
				File:     w.file,
				Span:     p.GetSpan(),
				Severity: SeverityError,
				Code:     "var-in-impl",
				Message:  "(var …) is not allowed in an implementation — name the receiver plainly 'self'; declare mutability in the signature's (var Self)",
			})
			continue
		}
		// A signature's `(var …)` is the mutable receiver `(var Self)`; nothing else.
		if inner, ok := p.(*ast.PBranch).Children[1].(*ast.PLeaf); ok && inner.Value == "Self" {
			continue
		}
		w.emit(Diagnostic{
			File:     w.file,
			Span:     p.GetSpan(),
			Severity: SeverityError,
			Code:     "var-non-receiver",
			Message:  "(var …) may only mark the receiver — write (var Self); a value parameter type cannot be mutable",
		})
	}
}

// varParamNames returns the names of the `(var name)` parameters — excluding a
// receiver named `self`, which is handled separately via receiverMutable.
// Assigning to one of these is a mutates-arg effect (write-back to the caller).
func varParamNames(argList ast.PNode) map[string]bool {
	br, ok := argList.(*ast.PBranch)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, p := range br.Children {
		if !isVarParam(p) {
			continue
		}
		name := p.(*ast.PBranch).Children[1].(*ast.PLeaf).Value
		if name != "self" {
			out[name] = true
		}
	}
	return out
}

// paramNames returns every parameter name a param/pattern list binds. A slot
// may be a plain name, a `(var/spread name)` wrapper, or — in a CLAUSE — any
// PATTERN (Features.md §2): a `(Type name)` type test, a `[p…]` list
// destructure, or a `Type.{ field = pat }` struct destructure (pre-desugared to
// `(Type 'field' pat …)`), all of whose BINDERS count. Literals bind nothing.
// Mirrors walker.collectPatternBinders. Used to shadow module vars: a param
// named like a module var refers to the param, not the module state.
func paramNames(argList ast.PNode) map[string]bool {
	br, ok := argList.(*ast.PBranch)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, p := range br.Children {
		addPatternBinders(out, p)
	}
	return out
}

// addPatternBinders records every binder of one param/pattern slot into out.
func addPatternBinders(out map[string]bool, item ast.PNode) {
	switch v := item.(type) {
	case *ast.PLeaf:
		s := v.Value
		if s == "" || s == "true" || s == "false" || s == "none" {
			return
		}
		if c := s[0]; c >= 'A' && c <= 'Z' {
			return // a type value matched by identity — not a binder
		}
		out[s] = true // a lowercase leaf binds (literals like 5/:a/'s' are harmless keys)
	case *ast.PBranch:
		if v.Open == "[" { // list pattern
			for _, ch := range v.Children {
				addPatternBinders(out, ch)
			}
			return
		}
		if v.Open != "(" || len(v.Children) == 0 {
			return
		}
		head, ok := v.Children[0].(*ast.PLeaf)
		if !ok {
			return
		}
		// (or name default) — the retired defaulted optional still binds `name`
		// (Children[1]); the DEFAULT is an expression, not a binding.
		if head.Value == "or" && len(v.Children) == 3 {
			if leaf, ok := v.Children[1].(*ast.PLeaf); ok {
				out[leaf.Value] = true
			}
			return
		}
		// (var/spread/optional name) wrappers and (Type name) type tests all bind
		// Children[1]; a `(disc X)` slot binds nothing.
		if len(v.Children) == 2 {
			if head.Value == "disc" {
				return
			}
			if leaf, ok := v.Children[1].(*ast.PLeaf); ok {
				out[leaf.Value] = true
			}
			return
		}
		// (Type 'field' pat …) — struct destructure: the pattern slots recurse.
		for i := 2; i < len(v.Children); i += 2 {
			addPatternBinders(out, v.Children[i])
		}
	}
}

// collectLocalNames gathers every name bound *locally* within a callable body —
// `let`/`let var`/`const` bindings and `foreach` loop vars — so a mutation of
// such a name is recognised as a locally-owned (pure) write, not a module-var
// write. It stops at nested `fun`/`method`/`macro` (their locals aren't ours).
//
// The collection is flat (it ignores block nesting), which is a safe
// over-approximation: at worst a real module-var write is missed because the
// name is also bound locally somewhere — conservative (no false effect), never a
// misclassified local.
func collectLocalNames(body ast.PNode) map[string]bool {
	locals := map[string]bool{}
	var walk func(n ast.PNode)
	walk = func(n ast.PNode) {
		br, ok := n.(*ast.PBranch)
		if !ok {
			return
		}
		if br.Open == "(" && len(br.Children) > 0 {
			if lf, ok := br.Children[0].(*ast.PLeaf); ok {
				switch lf.Value {
				case "fun", "method", "macro":
					return
				case "=":
					if len(br.Children) == 4 {
						return // a nested fun/method implementation — its locals aren't ours
					}
				case "let", "var", "const":
					if d, ok := declOf(br); ok {
						if d.IsClause {
							return // a nested implementation CLAUSE — its locals aren't ours
						}
						for _, b := range d.Binds {
							locals[b.Name] = true
						}
					}
				case "foreach":
					if len(br.Children) >= 2 {
						if name, _, ok := declIdent(br.Children[1]); ok {
							locals[name] = true
						}
					}
				}
			}
		}
		for _, c := range br.Children {
			walk(c)
		}
	}
	walk(body)
	return locals
}

// EffectCheck gates the Phase-3 effect diagnostics. It stays OFF until the
// stdlib finishes its `!`-migration (Effects.md Phase 4); flipping it on early
// would flag every not-yet-renamed effectful function. Tests set it directly;
// the CLI/LSP will expose it once the migration lands.
var EffectCheck = false

// rootLeaf returns the leftmost leaf of a dot-chain (or the leaf itself),
// naming the receiver an assignment targets: for `self.a.b` it is `self`.
func rootLeaf(n ast.PNode) (string, bool) {
	for {
		switch v := n.(type) {
		case *ast.PLeaf:
			return v.Value, true
		case *ast.PDot:
			n = v.LHS
		default:
			return "", false
		}
	}
}

// headName returns the callable name a call head invokes — a bare `f` leaf, the
// member name of `x.f` (the dot's RHS), or the member name of a package call
// `pkg/f` (the slash's RHS). The suffix on this name (`!`/`=`) tells the effect
// a call to it performs, so a `!`-named package function (`core/print-line!`)
// propagates its environmental effect to the caller.
func headName(head ast.PNode) (string, bool) {
	switch v := head.(type) {
	case *ast.PLeaf:
		return v.Value, true
	case *ast.PSlash:
		if r, ok := v.RHS.(*ast.PLeaf); ok {
			return r.Value, true
		}
	case *ast.PDot:
		if r, ok := v.RHS.(*ast.PLeaf); ok {
			return r.Value, true
		}
	}
	return "", false
}

// scanResult is what a body scan observes.
type scanResult struct {
	set        effectSet   // the callable's directly-observable effects
	violations []span.Span // self-mutations through a non-mutable receiver (effect-through-readonly)
}

// scanEffects walks a callable's body and summarises its effects.
//
// It is a LOCAL analysis — no call-graph fixpoint — because the `!` convention
// is enforced: an effectful callee always ends in '!', so its name alone
// summarises its effects without resolving it. The observed primitives:
//   - (= self …)  → mutates-self (an effect-through-readonly violation too when
//     the receiver isn't `(var self)`).
//   - (= p …) where p is a `(var p)` parameter → mutates-arg.
//   - (= <module-var> …) → mutates-free, when isFree says the root is a
//     module-level var and not locally shadowed. Q4: assigning a locally-owned
//     binding (a `let`/`let var` local, or a plain param) is pure, so only
//     self / var-params / module vars count.
//   - a call to a `!`-named function → that function's name as an effect
//     ("print-line!"), naming exactly which effectful function runs. This is the
//     ONLY environmental-effect source: there is no primitive table (freeform,
//     per-function tracking). A `!` name is trusted as effectful by declaration.
//
// Nested `(fun …)`/`(method …)`/`(macro …)` definitions are separate callables
// checked on their own, so their bodies are NOT descended into here. `&` blocks
// run in this scope, so they ARE descended into.
func scanEffects(body ast.PNode, recvMut bool, varParams map[string]bool, isFree func(string) bool) scanResult {
	r := scanResult{set: effectSet{}}

	// classifyMutation records the effect of mutating the value rooted at `root`
	// — whether by a direct `(= root.… v)` assignment or by calling a `=`-method
	// on it (`root.….m=`). The kind depends on what `root` is to THIS callable:
	// its own `self` (mutates-self, and an effect-through-readonly violation when
	// the receiver isn't `(var self)`), a `(var arg)` parameter (mutates-arg), a
	// module var (mutates-free), or a locally-owned binding (contained → pure).
	classifyMutation := func(root string, at span.Span) {
		switch {
		case root == "self":
			r.set.add(fxMutatesSelf)
			if !recvMut {
				r.violations = append(r.violations, at)
			}
		case varParams[root]:
			r.set.add(fxMutatesArg)
		case isFree != nil && isFree(root):
			r.set.add(fxMutatesFree)
		}
	}

	// classifyCallSuffix reads a call head's effect suffix. A `=`-name mutates
	// the value it acts on: for a method `x.m=` that's the receiver `x`, so it's
	// classified by x's root exactly like an assignment; a bare `f=` mutates a
	// var-arg (conservatively mutates-arg). A `!`-name is an opaque environmental
	// call. A name may carry both (`m!=`).
	classifyCallSuffix := func(head ast.PNode) {
		name, ok := headName(head)
		if !ok {
			return
		}
		if core.IsSelfEffectName(name) {
			if dot, ok := head.(*ast.PDot); ok {
				if root, ok := rootLeaf(dot.LHS); ok {
					classifyMutation(root, dot.LHS.GetSpan())
				}
			} else {
				r.set.add(fxMutatesArg)
			}
		}
		if core.IsEffectName(name) {
			// The called `!`-function IS the effect: record it by name, so the
			// set says exactly which effectful function runs (fine-grained).
			r.set.add(name)
		}
	}

	var walk func(n ast.PNode)
	walk = func(n ast.PNode) {
		switch v := n.(type) {
		case *ast.PBranch:
			if v.Open == "(" && len(v.Children) > 0 {
				if lf, ok := v.Children[0].(*ast.PLeaf); ok {
					switch lf.Value {
					case "fun", "method", "macro":
						return // a nested callable — checked separately
					case "let":
						if d, ok := declOf(v); ok && d.IsClause {
							return // a nested implementation CLAUSE — checked separately
						}
					case "=":
						if len(v.Children) == 4 {
							return // a nested fun/method implementation — checked separately
						}
						if len(v.Children) == 3 {
							if root, ok := rootLeaf(v.Children[1]); ok {
								classifyMutation(root, v.Children[1].GetSpan())
							}
						}
					}
				}
				classifyCallSuffix(v.Children[0])
			}
			for _, c := range v.Children {
				walk(c)
			}
		case *ast.PDot:
			walk(v.LHS)
			walk(v.RHS)
		case *ast.PSigil:
			walk(v.Inner)
			// PMacroCall args are quoted data (the macro expansion isn't visible
			// here), so macro calls contribute no statically-observable effect.
		}
	}
	walk(body)
	return r
}

// callableEffectLabel returns a hover label — the callable's effect set, UNIONED
// over ALL its clauses ("print-line!", "mutates-self, write!", or "pure") — for
// the fun/method/sig whose form contains defSpan, or "" if that form isn't a
// callable. Aggregating across clauses (rather than reading the single form at
// defSpan) is what lets the hover on `print-vertical!` see the `core/print-line!`
// call that lives in only one of its clauses; it also works when defSpan is on
// the SIGNATURE (which has no body of its own).
func callableEffectLabel(tree []ast.PNode, defSpan span.Span) string {
	br := declFormContaining(tree, defSpan)
	if br == nil || len(br.Children) == 0 {
		return ""
	}
	d, ok := declOf(br)
	if !ok || (d.Head != "fun" && d.Head != "method") {
		return ""
	}
	qname := d.Name
	if d.Owner != "" {
		qname = d.Owner + "." + d.Name
	}
	if qname == "" {
		return ""
	}

	mv := moduleVarNames(tree)
	union := effectSet{}
	for _, n := range tree {
		cb, ok := n.(*ast.PBranch)
		if !ok {
			continue
		}
		cd, ok := declOf(cb)
		if !ok || cd.IsSig || cd.Body == nil {
			continue // only implementation clauses carry effects
		}
		cq := cd.Name
		if cd.Owner != "" {
			cq = cd.Owner + "." + cd.Name
		}
		if cq != qname {
			continue
		}
		shadowed := paramNames(cd.ArgList)
		if shadowed == nil {
			shadowed = map[string]bool{}
		}
		for nm := range collectLocalNames(cd.Body) {
			shadowed[nm] = true
		}
		isFree := func(nm string) bool { return !shadowed[nm] && mv[nm] }
		// recvMut=true: the hover reports the effect SET, not readonly violations.
		r := scanEffects(cd.Body, true, varParamNames(cd.ArgList), isFree)
		union = union.union(r.set)
	}

	// A `!`/`=`-marked callable is effectful BY DECLARATION even when no inner
	// effect is visible (a leaf that only wraps a Go primitive) — say so rather
	// than mislabelling it "pure".
	if union.pure() && (core.IsEffectName(d.Name) || core.IsSelfEffectName(d.Name)) {
		return "effectful (declared)"
	}
	return union.String()
}

// moduleVarNames collects the top-level `let`/`var`/`const` binding names of a
// file — the module-level vars — so the effect hover can classify mutates-free
// writes without a full walker/scope.
func moduleVarNames(tree []ast.PNode) map[string]bool {
	out := map[string]bool{}
	for _, n := range tree {
		br, ok := n.(*ast.PBranch)
		if !ok || len(br.Children) == 0 {
			continue
		}
		if lf, ok := br.Children[0].(*ast.PLeaf); ok {
			switch lf.Value {
			case "let", "var", "const":
				if d, ok := declOf(br); ok {
					for _, b := range d.Binds {
						out[b.Name] = true
					}
				}
			}
		}
	}
	return out
}

// freeVarClassifier builds scanEffects' isFree predicate: a name is a module-var
// write (mutates-free) only if it resolves to a module-level DefVar AND is not
// shadowed by a param or a local `let`/`foreach` binding (which would make the
// assignment a locally-owned, pure write).
func freeVarClassifier(scope *Scope, argList, body ast.PNode) func(string) bool {
	shadowed := paramNames(argList)
	if shadowed == nil {
		shadowed = map[string]bool{}
	}
	for n := range collectLocalNames(body) {
		shadowed[n] = true
	}
	return func(n string) bool {
		if shadowed[n] {
			return false
		}
		def, _, ok := scope.Lookup(n)
		return ok && def.Kind == DefVar
	}
}

// checkPureContext flags any inferred effect inside a body that must stay pure.
// A property GETTER is auto-invoked whenever the property is read, so a side
// effect there is a bug — reading a value must not change the world. (Setters
// are excluded: assigning a property is expected to have an effect.)
func (w *walker) checkPureContext(scope *Scope, getter ast.PNode, what string, at span.Span) {
	if !EffectCheck {
		return
	}
	br, ok := getter.(*ast.PBranch)
	if !ok {
		return
	}
	d, ok := declOf(br)
	if !ok || d.Body == nil {
		return
	}
	// recvMut=true suppresses readonly-receiver violations — here we care only
	// whether the effect set is empty.
	r := scanEffects(d.Body, true, varParamNames(d.ArgList), freeVarClassifier(scope, d.ArgList, d.Body))
	if !r.set.pure() {
		w.emit(Diagnostic{
			File:     w.file,
			Span:     at,
			Severity: SeverityError,
			Code:     "effect-in-pure-context",
			Message:  fmt.Sprintf("%s must be pure, but has an effect (%s)", what, r.set),
		})
	}
}

// checkClauseGuardEffect flags a clause's `where` GUARD that has any effect.
// A guard runs during DISPATCH — possibly several times, for clauses that end
// up not matching — so it must be pure: no mutation, no io/randomness, and no
// call to a `!`/`=` function (an opaque effect). Gated by EffectCheck.
func (w *walker) checkClauseGuardEffect(scope *Scope, d topLevelDecl) {
	if !EffectCheck || d.Guard == nil {
		return
	}
	// recvMut=true: we only care whether the effect SET is empty, not whether a
	// self-write went through a read-only receiver (that's already an effect).
	r := scanEffects(d.Guard, true, varParamNames(d.ArgList), freeVarClassifier(scope, d.ArgList, d.Guard))
	if !r.set.pure() {
		w.emit(Diagnostic{
			File:     w.file,
			Span:     d.Guard.GetSpan(),
			Severity: SeverityError,
			Code:     "guard-effect",
			Message:  fmt.Sprintf("a clause's 'where' guard must be pure — it runs during dispatch — but this one has an effect (%s)", r.set),
		})
	}
}

// checkClauseSetEffects enforces the effect conventions on ONE callable, taking
// its effect set as the UNION over ALL its implementation clauses and reporting
// against its SIGNATURE (Effects.md). Two properties fall out of aggregating:
//
//   - An effect is a property of the WHOLE callable, so a pure clause never
//     draws a spurious diagnostic while a sibling clause performs the effect
//     (the multi-clause `print-vertical!` in rps.pho: one clause is `= None`,
//     the other calls `core/print-line!` — together the callable is effectful).
//   - Every effect diagnostic lands on the SIGNATURE — the one place the name
//     and its `!`/`=` are declared — instead of being scattered across impls.
//     Without a signature (a sig-less script clause, itself a signature-required
//     error) it falls back to the first clause.
//
// The two suffixes are checked independently: '=' for SELF/value mutation (a
// `(var self)` receiver or `(var arg)`), '!' for ENVIRONMENTAL effects (a
// module-global write or a call to a `!`-function — the only two sources, since
// environmental primitives enter only through `!`-named stdlib wrappers). A `!`
// name is trusted as effectful, so only MISSING marks are flagged, never
// spurious ones. Guards and pure-context accessors are checked separately, per
// clause — they constrain specific code, not the name's contract. Gated by
// EffectCheck.
func (w *walker) checkClauseSetEffects(scope *Scope, set clauseSet) {
	if !EffectCheck || len(set.clauses) == 0 {
		return
	}
	// Receiver mutability is declared once, in the SIGNATURE's `(var Self)`
	// receiver (a clause names its receiver plainly). Read it from the adjacent
	// sig; without one, a clause can't be `(var self)`, so a self-mutation is an
	// effect-through-readonly — consistent with signature-required for sig-less
	// impls.
	recvMut := false
	if set.adjacentSig != nil {
		recvMut = receiverMutable(set.adjacentSig.ArgList)
	}

	union := effectSet{}
	var violations []span.Span
	for _, cl := range set.clauses {
		if cl.Body == nil {
			continue
		}
		r := scanEffects(cl.Body, recvMut, varParamNames(cl.ArgList), freeVarClassifier(scope, cl.ArgList, cl.Body))
		union = union.union(r.set)
		violations = append(violations, r.violations...)
	}

	// Anchor on the signature's name — its declaration site — falling back to the
	// first clause when the set has no adjacent signature.
	name := set.clauses[0].Name
	anchor := set.clauses[0].NameSpan
	if set.adjacentSig != nil {
		name = set.adjacentSig.Name
		anchor = set.adjacentSig.NameSpan
	}

	// A self-mutation through a read-only receiver: the remedy is declaring the
	// receiver `(var Self)` in the SIGNATURE, so report it once, on the sig.
	if len(violations) > 0 {
		w.emit(Diagnostic{
			File: w.file, Span: anchor, Severity: SeverityError, Code: "effect-through-readonly",
			Message: fmt.Sprintf("'%s' mutates 'self' but its signature's receiver is read-only — declare it '(var Self)'", name),
		})
	}

	needsEquals, needsBang := union.needsEquals(), union.needsBang()
	hasEquals, hasBang := core.IsSelfEffectName(name), core.IsEffectName(name)

	// Environmental effect: a name that inherits one (calls a `!`-function, or
	// writes module state) MUST end in '!'. The reverse is NOT flagged — a `!`
	// function is effectful BY DECLARATION, trusted, never second-guessed (so no
	// spurious-bang). `hasBang` is still read: it suppresses missing-bang.
	if needsBang && !hasBang {
		w.emit(Diagnostic{
			File: w.file, Span: anchor, Severity: SeverityError, Code: "missing-bang",
			Message: fmt.Sprintf("'%s' has an environmental effect (%s) — an environmental function must end in '!'", name, union),
		})
	}
	switch {
	case needsEquals && !hasEquals:
		w.emit(Diagnostic{
			File: w.file, Span: anchor, Severity: SeverityError, Code: "missing-equals",
			Message: fmt.Sprintf("'%s' mutates a value passed to it (%s) — a self/value-mutating function must end in '='", name, union),
		})
	case hasEquals && !needsEquals:
		w.emit(Diagnostic{
			File: w.file, Span: anchor, Severity: SeverityWarning, Code: "spurious-equals",
			Message: fmt.Sprintf("'%s' mutates nothing it was given — a name should not end in '=' unless it mutates a '(var self)'/'(var arg)'", name),
		})
	}
}
