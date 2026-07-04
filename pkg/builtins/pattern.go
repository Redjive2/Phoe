package builtins

import (
	"strings"

	"pho/pkg/core"
)

// Pattern engine (Features.md §1–§3): the shared matcher behind `let`
// implementation-clause parameters and `select … case` arms.
//
// A pattern is parsed ONCE at definition time from a LOWERED node (the same
// tree the evaluator walks), with every constant sub-expression evaluated
// eagerly — a Capitalized leaf resolves to its (type) value, `:atom`/number/
// string literals to theirs — mirroring how the retired discriminant machinery
// evaluated `(disc X)` keys. Matching is then a pure structural walk that
// produces the clause/case bindings.
//
// Grammar (over lowered shapes):
//
//	name                  → bind (wildcard) — lowercase identifier
//	0 · 'str' · :atom …   → literal, matched with tvalEqual
//	true / false / none   → literal
//	Capitalized           → evaluated at def time (a type value, usually);
//	                        matched by tvalEqual — disc-style static dispatch
//	(Type name)           → runtime type test (PhoType.Contains) + bind
//	[p1 p2 …]             → list destructure, exact length ((core.Slice …) lowered)
//	Type.{ field = pat }  → instance-of Type + field destructure; arrives
//	                        pre-desugared as (Type 'field' pat …)
type patternKind int

const (
	patBind patternKind = iota
	patLiteral
	patList
	patStruct
	patTypeTest
)

type pattern struct {
	kind patternKind
	name string        // patBind / patTypeTest: the bound name
	lit  core.Value    // patLiteral: the value to equal
	typ  *core.PhoType // patStruct / patTypeTest: the type to test

	typeName string     // patStruct / patTypeTest: rendered name for errors
	elems    []*pattern // patList
	fields   []fieldPat // patStruct, in source order
	src      string     // rendered source, for diagnostics

	// mutable marks a binder written `(var name)` / `(var Type name)`: the
	// binding it introduces is reassignable. Meaningful for `let` destructuring
	// (const vs var) and for a `(var self)` receiver; a plain binder is const in
	// a `let` and an ordinary parameter in a clause.
	mutable bool
}

type fieldPat struct {
	name    string        // the field to select
	pat     *pattern      // the pattern its value must match
	capture *fieldCapture // optional: also bind the whole field value (the () capture operator)
}

// fieldCapture is the `()` capture operator on a struct-field key: `(field)`
// (or `(var field)` / `(Type field)`) binds the field's whole value to `name`
// (reassignable when mutable) in ADDITION to matching it against the field's
// pattern. The name is the field name itself; any type is erased.
type fieldCapture struct {
	name    string
	mutable bool
}

// binderSet tracks the names a clause's patterns bind, in appearance order —
// the clause compiler feeds them to BindFun as its positional argList.
type binderSet struct {
	seen  map[string]bool
	order []string
}

func newBinderSet() *binderSet { return &binderSet{seen: map[string]bool{}} }

// isBinderName reports whether a leaf can BIND in a pattern: a plain
// lowercase identifier (no `#` privacy marker — a binder is a fresh local).
func isBinderName(s string) bool {
	if !core.IsIdent(s) || s == "" || s[0] == '#' {
		return false
	}
	c := s[0]
	return c >= 'a' && c <= 'z'
}

// parsePattern converts a lowered node into a pattern, evaluating constant
// sub-expressions in ctx (the clause's definition context). binders tracks
// names bound so far across the WHOLE clause so duplicates are rejected.
// Reports through ctx and returns ok=false on a malformed pattern.
func parsePattern(ctx core.Context, node core.Node, binders *binderSet) (*pattern, bool) {
	if leaf, ok := core.AsLeaf(node); ok {
		return parseLeafPattern(ctx, string(leaf), binders)
	}

	branch, ok := core.AsBranch(node)
	if !ok || len(branch) == 0 {
		ctx.Errorf(core.ErrBadForm, "cannot read '%s' as a pattern", core.Inspect(node))
		return nil, false
	}

	// [p1 p2 …] — the array literal lowers to (core.Slice …).
	if head, ok := core.AsLeaf(branch[0]); ok && string(head) == core.Slice {
		elems := make([]*pattern, 0, len(branch)-1)
		for _, ch := range branch[1:] {
			p, ok := parsePattern(ctx, ch, binders)
			if !ok {
				return nil, false
			}
			elems = append(elems, p)
		}
		return &pattern{kind: patList, elems: elems, src: core.Inspect(node)}, true
	}

	// (var name) / (var Type name) — a mutable binder. `var` is the one
	// lowercase branch head a pattern accepts; it wraps a plain binder or a
	// (Type name) test and marks the binding reassignable (Effects.md / let).
	if h, ok := core.AsLeaf(branch[0]); ok && string(h) == "var" {
		var inner core.Node
		switch len(branch) {
		case 2: // (var name)
			inner = branch[1]
		case 3: // (var Type name) — reparse the (Type name) core, then mark mutable
			inner = core.Branch{branch[1], branch[2]}
		default:
			ctx.Errorf(core.ErrBadForm, "a (var …) pattern is written (var name) or (var Type name), got '%s'", core.Inspect(node))
			return nil, false
		}
		p, ok := parsePattern(ctx, inner, binders)
		if !ok {
			return nil, false
		}
		if p.kind != patBind && p.kind != patTypeTest {
			ctx.Errorf(core.ErrBadForm, "(var …) must wrap a name or (Type name), got '%s'", core.Inspect(node))
			return nil, false
		}
		p.mutable = true
		p.src = core.Inspect(node)
		return p, true
	}

	// (name) — the capture operator: a parenthesized binder. Redundant with a
	// bare `name` in most positions (kept for uniformity), it is the way to bind
	// a struct field whose key would otherwise be a bare selector.
	if h, ok := core.AsLeaf(branch[0]); ok && len(branch) == 1 && isBinderName(string(h)) {
		if !claimBinder(ctx, string(h), binders) {
			return nil, false
		}
		return &pattern{kind: patBind, name: string(h), src: core.Inspect(node)}, true
	}

	head, ok := core.AsLeaf(branch[0])
	if !ok || !isTypeHeadName(string(head)) {
		ctx.Errorf(core.ErrBadForm, "cannot read '%s' as a pattern — expected a name, literal, [list], (var name), (Type name), or Type.{ field = pattern }", core.Inspect(node))
		return nil, false
	}

	// The head names a type: evaluate it once at def time.
	tv := branch[0].Evaluate(ctx)
	if tv.Kind != core.KindType {
		ctx.Errorf(core.ErrType, "pattern head '%s' is not a type (kind '%s')", head, tv.Kind)
		return nil, false
	}
	pt := tv.Val.(*core.PhoType)

	// (Type name) — a runtime type test binding `name`.
	if len(branch) == 2 {
		nameLeaf, ok := core.AsLeaf(branch[1])
		if ok && isBinderName(string(nameLeaf)) {
			if !claimBinder(ctx, string(nameLeaf), binders) {
				return nil, false
			}
			return &pattern{kind: patTypeTest, name: string(nameLeaf), typ: pt, typeName: string(head), src: core.Inspect(node)}, true
		}
		ctx.Errorf(core.ErrBadForm, "a (Type name) pattern needs a plain lowercase name to bind, got '%s'", core.Inspect(branch[1]))
		return nil, false
	}

	// Type.{ field = pat … } — pre-desugared to (Type 'field' pat …): the head,
	// then alternating field keys and field patterns. A key is a quoted field
	// name `'field'` (a plain selector) or a capture form `(field)` / `(var
	// field)` / `(Type field)` — the `()` capture operator, which also binds the
	// whole field value.
	if len(branch) >= 3 && (len(branch)-1)%2 == 0 {
		fields := make([]fieldPat, 0, (len(branch)-1)/2)
		for i := 1; i+1 < len(branch); i += 2 {
			fname, capture, ok := parseFieldKey(ctx, branch[i], binders)
			if !ok {
				return nil, false
			}
			fp, ok := parsePattern(ctx, branch[i+1], binders)
			if !ok {
				return nil, false
			}
			fields = append(fields, fieldPat{name: fname, pat: fp, capture: capture})
		}
		return &pattern{kind: patStruct, typ: pt, typeName: string(head), fields: fields, src: core.Inspect(node)}, true
	}

	ctx.Errorf(core.ErrBadForm, "cannot read '%s' as a pattern", core.Inspect(node))
	return nil, false
}

// parseLeafPattern classifies a single leaf: binder, or evaluated literal.
func parseLeafPattern(ctx core.Context, s string, binders *binderSet) (*pattern, bool) {
	switch {
	case s == "true" || s == "false" || s == "none",
		core.IsStrLit(s),
		strings.HasPrefix(s, ":"),
		isNumericLeaf(s):
		// Self-evaluating literals; a malformed atom/number reports here (def
		// time), which is the visible failure we want.
		return &pattern{kind: patLiteral, lit: core.Leaf(s).Evaluate(ctx), src: s}, true

	case isBinderName(s):
		if !claimBinder(ctx, s, binders) {
			return nil, false
		}
		return &pattern{kind: patBind, name: s, src: s}, true

	case core.IsIdent(s): // Capitalized identifier: resolve at def time —
		// normally a type value used disc-style (`(let to (self Number) = …)`).
		v, found := ctx.Resolve(s)
		if !found {
			ctx.Errorf(core.ErrUnresolved, "unknown name '%s' in pattern", s)
			return nil, false
		}
		return &pattern{kind: patLiteral, lit: v, src: s}, true
	}
	ctx.Errorf(core.ErrBadForm, "cannot read '%s' as a pattern", s)
	return nil, false
}

// parseFieldKey reads a struct-pattern field key: a quoted field name `'field'`
// (a plain selector) or a capture form `(field)` / `(var field)` / `(Type field)`
// / `(var Type field)` — the `()` capture operator, whose binder name IS the
// field name and whose type is erased. Claims the capture binder when present.
func parseFieldKey(ctx core.Context, key core.Node, binders *binderSet) (name string, capture *fieldCapture, ok bool) {
	if lf, isLeaf := core.AsLeaf(key); isLeaf && core.IsStrLit(string(lf)) {
		return core.StrLitBody(string(lf)), nil, true
	}
	if bn, mutable, isSimple := simpleLetBinder(key); isSimple {
		if !claimBinder(ctx, bn, binders) {
			return "", nil, false
		}
		return bn, &fieldCapture{name: bn, mutable: mutable}, true
	}
	ctx.Errorf(core.ErrBadForm, "struct pattern field key '%s' — write `field = pattern` or a capture `(field) = pattern`", core.Inspect(key))
	return "", nil, false
}

func claimBinder(ctx core.Context, name string, binders *binderSet) bool {
	if binders.seen[name] {
		ctx.Errorf(core.ErrBadForm, "pattern binds '%s' more than once", name)
		return false
	}
	binders.seen[name] = true
	binders.order = append(binders.order, name)
	return true
}

func isNumericLeaf(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	return len(s) > 0 && s[0] >= '0' && s[0] <= '9'
}

// isTypeHeadName reports whether a branch head could name a type in a
// pattern: a Capitalized identifier (Self included — the method receiver type).
func isTypeHeadName(s string) bool {
	if !core.IsIdent(s) {
		return false
	}
	c := s[0]
	if c == '#' && len(s) > 1 { // private type
		c = s[1]
	}
	return c >= 'A' && c <= 'Z'
}

// matchPattern matches val against pat, accumulating bindings into out.
// priv grants private-field destructuring (true when matching a method
// clause's receiver). A privacy violation reports through ctx and fails the
// match. Returns whether the pattern matched.
func matchPattern(ctx core.Context, pat *pattern, val core.Value, out map[string]core.Value, priv bool) bool {
	switch pat.kind {
	case patBind:
		out[pat.name] = val
		return true

	case patLiteral:
		return tvalEqual(pat.lit, val)

	case patTypeTest:
		if !pat.typ.Contains(val) {
			return false
		}
		out[pat.name] = val
		return true

	case patList:
		if val.Kind != core.KindArray {
			return false
		}
		elems := *val.Val.(*[]core.Value)
		if len(elems) != len(pat.elems) {
			return false
		}
		for i, ep := range pat.elems {
			if !matchPattern(ctx, ep, elems[i], out, priv) {
				return false
			}
		}
		return true

	case patStruct:
		if val.Kind != core.KindInstance || !pat.typ.Contains(val) {
			return false
		}
		inst := val.Val.(*core.Instance)
		for _, fp := range pat.fields {
			fv, found := inst.Fields[fp.name]
			if !found {
				return false
			}
			if strings.HasPrefix(fp.name, "#") && !priv && !inst.Privileged {
				ctx.Errorf(core.ErrField, "cannot destructure private field '%s' outside %s's own methods", fp.name, pat.typeName)
				return false
			}
			if fp.capture != nil {
				out[fp.capture.name] = fv // the () capture operator: bind the whole field value
			}
			if !matchPattern(ctx, fp.pat, fv, out, priv) {
				return false
			}
		}
		return true
	}
	return false
}

// patternsDescribe renders a clause's patterns for no-matching-impl errors.
func patternsDescribe(pats []*pattern) string {
	parts := make([]string, len(pats))
	for i, p := range pats {
		parts[i] = p.src
	}
	return "(" + strings.Join(parts, " ") + ")"
}

// patternBinder is one name a pattern binds, with whether it was written
// `(var …)` (reassignable). The `let` destructurer declares each accordingly.
type patternBinder struct {
	name    string
	mutable bool
}

// patternBinders enumerates every name a pattern binds, in source order, with
// its mutability. (Types on a binder are erased for binding — see
// eraseScalarTypeTests — so patTypeTest contributes its name like a plain bind.)
func patternBinders(p *pattern) []patternBinder {
	var out []patternBinder
	var walk func(*pattern)
	walk = func(p *pattern) {
		switch p.kind {
		case patBind, patTypeTest:
			out = append(out, patternBinder{p.name, p.mutable})
		case patList:
			for _, e := range p.elems {
				walk(e)
			}
		case patStruct:
			for _, f := range p.fields {
				if f.capture != nil {
					out = append(out, patternBinder{f.capture.name, f.capture.mutable})
				}
				walk(f.pat)
			}
		}
	}
	walk(p)
	return out
}

// eraseScalarTypeTests turns `(Type name)` type-test patterns into plain binds,
// so a `let` destructure treats a scalar type annotation as erased — the
// gradual checker reads it, the runtime just binds (matching how a typed
// `var`/`const`/`let` binding already erases its type). Struct patterns keep
// their type: it is structurally required to read the instance's fields.
func eraseScalarTypeTests(p *pattern) {
	switch p.kind {
	case patTypeTest:
		p.kind = patBind
	case patList:
		for _, e := range p.elems {
			eraseScalarTypeTests(e)
		}
	case patStruct:
		for _, f := range p.fields {
			eraseScalarTypeTests(f.pat)
		}
	}
}

// isCatchAll reports whether every pattern is a bare binder — the clause
// accepts anything (the required fall-through when coverage is undecidable).
func isCatchAll(pats []*pattern) bool {
	for _, p := range pats {
		if p.kind != patBind {
			return false
		}
	}
	return true
}
