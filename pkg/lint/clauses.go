package lint

import (
	"fmt"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/core"
	"pho/pkg/span"
)

// Clause-set checking (Features.md §1/§2/§9).
//
// A callable is declared by a SIGNATURE (`fun`/`method`/`static method`) and
// implemented by one or more `(let name (patterns) [where guard] = body)`
// CLAUSES that directly follow it. This pass groups a file's consecutive
// clauses by qualified name and checks the layout and dispatch surface:
//
//   - impl-not-adjacent       — the set's signature exists in this file but not
//     directly above the first clause.
//   - signature-required      — a set with no signature anywhere: always in a
//     .phl library; in a .pho script when the signature can't be inferred
//     (some parameter is a bare binder in every clause).
//   - non-exhaustive-clauses  — no catch-all and the literal patterns provably
//     don't cover the dispatch space.
//   - const-arg-not-static    — a call passes a non-constant into a `(const T)`
//     signature slot (its value must be known at parse time).
//   - no-impl-for-const       — a call's constant matches no clause literal and
//     the set has no catch-all for that slot.

// clauseSet is one maximal run of consecutive clauses sharing a qualified name.
type clauseSet struct {
	qname    string // "add" or "Owner.name"
	isMethod bool
	clauses  []topLevelDecl
	// adjacentSig is the signature form immediately preceding the first clause
	// (same qualified name), nil when the set has none.
	adjacentSig *topLevelDecl
}

// checkClauses runs the clause-set checks over a file's top-level forms.
// scope is the file scope (chained to the package scope), so signature
// presence is checked across sibling files too.
func (w *walker) checkClauses(scope *Scope, tree []ast.PNode) {
	type entry struct {
		d        topLevelDecl
		isSig    bool
		isClause bool
	}
	var entries []entry
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok {
			entries = append(entries, entry{})
			continue
		}
		isSig := d.IsSig && qualifiedName(d) != ""
		// A `(static method R.M …)` signature reads like an instance sig here:
		// its clauses use the same `(let R.M …)` surface.
		if d.Head == "static" && d.Sub == "method" && d.IsSig && d.Owner != "" && d.Name != "" {
			isSig = true
		}
		entries = append(entries, entry{d: d, isSig: isSig, isClause: d.IsClause && qualifiedName(d) != ""})
	}

	// Group consecutive clauses by qualified name; remember each set's
	// immediately preceding form.
	var sets []clauseSet
	for i := 0; i < len(entries); i++ {
		if !entries[i].isClause {
			continue
		}
		qn := qualifiedName(entries[i].d)
		set := clauseSet{qname: qn, isMethod: entries[i].d.Head == "method"}
		j := i
		for j < len(entries) && entries[j].isClause && qualifiedName(entries[j].d) == qn {
			set.clauses = append(set.clauses, entries[j].d)
			j++
		}
		if i > 0 && entries[i-1].isSig && qualifiedName(entries[i-1].d) == qn {
			sig := entries[i-1].d
			set.adjacentSig = &sig
		}
		sets = append(sets, set)
		i = j - 1
	}

	for _, set := range sets {
		w.checkClauseSet(scope, set)
		// The effect contract (`!`/`=`) is inferred over the whole clause set and
		// reported on the signature — not per clause (Effects.md).
		w.checkClauseSetEffects(scope, set)
	}
	w.checkConstCalls(scope, sets, tree)
}

// qualifiedName is a decl's clause-set key: "name" for a fun, "Owner.name" for
// a method. "" when the decl has no usable name.
func qualifiedName(d topLevelDecl) string {
	if d.Name == "" {
		return ""
	}
	if d.Owner != "" {
		return d.Owner + "." + d.Name
	}
	return d.Name
}

// checkClauseSet runs the per-set layout + exhaustiveness checks.
func (w *walker) checkClauseSet(scope *Scope, set clauseSet) {
	first := set.clauses[0]
	if set.adjacentSig == nil {
		switch {
		case scope.HasSig(set.qname):
			// A signature exists (this file or a sibling) but isn't directly
			// above. Only flag when it is in THIS file — a sibling file's sig
			// can't be adjacent, and the layout there is that file's business.
			if w.fileScope != nil && w.fileScope.Sigs[set.qname] {
				w.emit(Diagnostic{
					File: w.file, Span: first.NameSpan, Severity: SeverityWarning, Code: "impl-not-adjacent",
					Message: fmt.Sprintf("the clauses of '%s' should directly follow its signature", set.qname),
				})
			}
		default:
			// No signature anywhere. An implementation must be preceded by its
			// declaration — in scripts as well as libraries; a `let` clause set
			// never stands alone (Doc/PlanV1/DeclImplSplit.md). This holds even
			// when a signature could be INFERRED from the clause patterns: an
			// inferable shape is not a written contract, and the split requires
			// the contract to be explicit.
			w.emit(Diagnostic{
				File: w.file, Span: first.NameSpan, Severity: SeverityError, Code: "signature-required",
				Message: fmt.Sprintf("an implementation needs a signature: declare (%s) before the clauses of '%s'", sigHint(set), set.qname),
			})
		}
	}
	w.checkExhaustive(set)
}

// sigHint renders the signature form a diagnostic should point the writer at.
func sigHint(set clauseSet) string {
	if set.isMethod {
		return "method " + set.qname + " (Self …) Result"
	}
	return "fun " + set.qname + " (Types…) Result"
}

// patternConstrains reports whether a top-level pattern slot pins the slot's
// type: a literal, a Capitalized type value, a `(Type name)` test, or a
// list/struct destructure. A bare binder or `(var/spread x)` wrapper doesn't.
func patternConstrains(item ast.PNode) bool {
	switch node := item.(type) {
	case *ast.PLeaf:
		return !isBinderLeaf(node)
	case *ast.PBranch:
		if node.Open == "[" {
			return true // a list destructure pins List
		}
		if node.Open != "(" || len(node.Children) == 0 {
			return false
		}
		if head, ok := node.Children[0].(*ast.PLeaf); ok {
			switch head.Value {
			case "var", "spread", "or", "optional", "disc":
				return false
			}
			// A Capitalized head is a (Type name) test or Type.{…} destructure.
			c := strings.TrimPrefix(head.Value, "#")
			return c != "" && c[0] >= 'A' && c[0] <= 'Z'
		}
	}
	return false
}

// isBinderLeaf reports whether a leaf pattern is a plain BINDER (a lowercase
// identifier) rather than a literal or a type value.
func isBinderLeaf(node *ast.PLeaf) bool {
	v := node.Value
	if !looksLikeIdentifier(v) || v == "true" || v == "false" || v == "none" {
		return false
	}
	return v[0] >= 'a' && v[0] <= 'z'
}

// isCatchAllClause reports whether a clause matches ANY arguments of its arity:
// unguarded, with every top-level slot a bare binder / (var/spread x) wrapper.
func isCatchAllClause(cl topLevelDecl) bool {
	if cl.Guard != nil {
		return false
	}
	br, ok := asList(cl.ArgList)
	if !ok {
		return false
	}
	for _, item := range br.Children {
		if patternConstrains(item) {
			return false
		}
	}
	return true
}

// clauseCoversAll reports whether an unguarded clause matches EVERY value of
// every slot's type — so its presence makes the whole set exhaustive. Unlike
// isCatchAllClause it accepts structural patterns that don't NARROW the
// dispatch space: an all-binder struct destructure of the slot's own type, a
// `(Type x)` test whose type covers the slot, and an all-binder list pattern.
// Such a clause is a catch-all in every way that matters — the reason a
// single-clause total method (e.g. `(let Box.peek (Box.{ #inner = v }) = v)`)
// must not draw a spurious non-exhaustive-clauses warning.
func clauseCoversAll(set clauseSet, cl topLevelDecl) bool {
	if cl.Guard != nil {
		return false
	}
	br, ok := asList(cl.ArgList)
	if !ok {
		return false
	}
	owner := ""
	if len(set.clauses) > 0 {
		owner = set.clauses[0].Owner
	}
	for pos, item := range br.Children {
		if !patternTotalForSlot(item, slotTypeText(set, pos, owner), owner) {
			return false
		}
	}
	return true
}

// slotTypeText renders the declared type of clause slot `pos`: the owner type
// for a method receiver (slot 0), else the adjacent signature's param type.
// "" when there is no signature — then a structural pattern defines the slot's
// own type and covers it.
func slotTypeText(set clauseSet, pos int, owner string) string {
	if set.isMethod && pos == 0 {
		return owner
	}
	if set.adjacentSig == nil {
		return ""
	}
	if slot, ok := sigSlotFor(set.adjacentSig, pos); ok {
		return pnodeText(sigParamType(slot))
	}
	return ""
}

// patternTotalForSlot reports whether a pattern matches every value the slot
// can hold — so it doesn't narrow the dispatch space. Binders and `(var/spread
// x)` wrappers always do; a literal or a Capitalized type-value leaf never
// does; a `(Type x)` test or `Type.{ … }` destructure does iff its type covers
// the slot's declared type AND every nested field pattern is itself total; an
// all-binder list pattern does (a length gap still errors at runtime — the
// warning targets missed literal/type cases, not arity).
func patternTotalForSlot(item ast.PNode, slotType, owner string) bool {
	switch node := item.(type) {
	case *ast.PLeaf:
		return isBinderLeaf(node)
	case *ast.PBranch:
		if node.Open == "[" {
			for _, e := range node.Children {
				if !patternTotalForSlot(e, "", owner) {
					return false
				}
			}
			return true
		}
		if node.Open != "(" || len(node.Children) == 0 {
			return false
		}
		head, ok := node.Children[0].(*ast.PLeaf)
		if !ok {
			return false
		}
		switch head.Value {
		case "var", "spread":
			return true
		case "or", "optional", "disc":
			return false
		}
		if !isTypeHead(head.Value) {
			return false
		}
		// (Type name) — a runtime type test binding `name`.
		if len(node.Children) == 2 {
			nm, ok := node.Children[1].(*ast.PLeaf)
			return ok && isBinderLeaf(nm) && typeCoversSlot(head.Value, slotType, owner)
		}
		// Type.{ 'field' pat … } destructure (pre-desugared to (Type 'field' pat …)):
		// every value pattern (odd indices are the field-name keys) must be total.
		for i := 2; i < len(node.Children); i += 2 {
			if !patternTotalForSlot(node.Children[i], "", owner) {
				return false
			}
		}
		return typeCoversSlot(head.Value, slotType, owner)
	}
	return false
}

// typeCoversSlot reports whether a pattern's type covers the slot's declared
// type — i.e. every value of the slot inhabits the pattern's type. True when
// there is no declared slot type (the pattern's own type IS the slot's), when
// the pattern type is ⊤ (Unknown/Dynamic), or when the two name the same type.
// `Self` aliases the receiver owner.
func typeCoversSlot(patType, slotType, owner string) bool {
	norm := func(s string) string {
		s = strings.TrimPrefix(s, "#")
		if s == "Self" && owner != "" {
			return owner
		}
		return s
	}
	if slotType == "" {
		return true
	}
	pt := norm(patType)
	return pt == "Unknown" || pt == "Dynamic" || pt == norm(slotType)
}

// isTypeHead reports whether a branch head names a type (a Capitalized
// identifier, with an optional leading `#` for a private type).
func isTypeHead(s string) bool {
	c := strings.TrimPrefix(s, "#")
	return c != "" && c[0] >= 'A' && c[0] <= 'Z'
}

// checkExhaustive flags a clause set whose dispatch provably has gaps:
// no clause that covers every input, and the patterned slots aren't
// (const …) in the signature, and the literals don't fully cover a Boolean
// or atom-union slot.
func (w *walker) checkExhaustive(set clauseSet) {
	for _, cl := range set.clauses {
		if clauseCoversAll(set, cl) {
			return
		}
	}
	patterned := patternedSlots(set)
	if len(patterned) == 0 {
		// Every slot is a binder yet no clause is a catch-all — every clause is
		// guarded. Guards are opaque; stay gradual.
		return
	}
	sig := set.adjacentSig
	if sig != nil && allConstSlots(sig, patterned) {
		return // dispatch is by parse-time constants; call sites are checked instead
	}
	// A single patterned slot may still be exhaustive by literal coverage:
	// {true false}, or every atom of the sig's (Or :a :b …) union.
	if len(patterned) == 1 {
		pos := patterned[0]
		covered := map[string]bool{}
		for _, cl := range set.clauses {
			if cl.Guard != nil {
				continue
			}
			br, ok := asList(cl.ArgList)
			if !ok || pos >= len(br.Children) {
				continue
			}
			if !otherSlotsAreBinders(br.Children, pos) {
				continue
			}
			if lf, ok := br.Children[pos].(*ast.PLeaf); ok {
				covered[lf.Value] = true
			}
		}
		if covered["true"] && covered["false"] {
			return
		}
		if sig != nil {
			if atoms, ok := sigAtomUnion(sig, pos); ok {
				all := true
				for _, a := range atoms {
					if !covered[a] {
						all = false
					}
				}
				if all {
					return
				}
			}
		}
	}
	first := set.clauses[0]
	w.emit(Diagnostic{
		File: w.file, Span: first.NameSpan, Severity: SeverityWarning, Code: "non-exhaustive-clauses",
		Message: fmt.Sprintf("the clauses of '%s' may not cover every call — add an unguarded all-binder clause (or mark the dispatch slots (const …) in the signature)", set.qname),
	})
}

// patternedSlots returns the slot positions where SOME clause has a
// constraining pattern.
func patternedSlots(set clauseSet) []int {
	seen := map[int]bool{}
	for _, cl := range set.clauses {
		br, ok := asList(cl.ArgList)
		if !ok {
			continue
		}
		for i, item := range br.Children {
			if patternConstrains(item) {
				seen[i] = true
			}
		}
	}
	var out []int
	for i := range seen {
		out = append(out, i)
	}
	return out
}

// otherSlotsAreBinders reports whether every slot except pos is a plain binder.
func otherSlotsAreBinders(slots []ast.PNode, pos int) bool {
	for i, item := range slots {
		if i == pos {
			continue
		}
		if patternConstrains(item) {
			return false
		}
	}
	return true
}

// allConstSlots reports whether every patterned slot is a `(const T)` slot in
// the signature. Clause slot i aligns with sig slot i: an instance-method sig
// carries the receiver type at slot 0 mirroring the clause's receiver pattern,
// and a static sig has no receiver slot mirroring its receiver-less clauses.
func allConstSlots(sig *topLevelDecl, patterned []int) bool {
	for _, pos := range patterned {
		slot, ok := sigSlotFor(sig, pos)
		if !ok || sigParamMod(slot) != "const" {
			return false
		}
	}
	return true
}

// sigSlotFor maps a clause slot position to the signature's param node.
func sigSlotFor(sig *topLevelDecl, pos int) (ast.PNode, bool) {
	br, ok := asList(sig.ArgList)
	if !ok || pos < 0 || pos >= len(br.Children) {
		return nil, false
	}
	return br.Children[pos], true
}

// sigAtomUnion reads a signature slot as a closed atom union `(Or :a :b …)`,
// returning the atom literals (leaf text, ":a") when every member is an atom.
func sigAtomUnion(sig *topLevelDecl, pos int) ([]string, bool) {
	slot, ok := sigSlotFor(sig, pos)
	if !ok {
		return nil, false
	}
	t := sigParamType(slot)
	br, ok := asList(t)
	if !ok || len(br.Children) < 2 {
		return nil, false
	}
	if head, ok := br.Children[0].(*ast.PLeaf); !ok || head.Value != "Or" {
		return nil, false
	}
	var atoms []string
	for _, c := range br.Children[1:] {
		lf, ok := c.(*ast.PLeaf)
		if !ok || len(lf.Value) < 2 || lf.Value[0] != ':' {
			return nil, false
		}
		atoms = append(atoms, lf.Value)
	}
	return atoms, true
}

// ---------------------------------------------------------------------------
// Const call-site checks
// ---------------------------------------------------------------------------

// constFunInfo is the const-dispatch surface of one FREE function: which
// argument positions are `(const T)` slots, the literal keys implemented at
// each, and whether some clause leaves the slot open (a binder catch-all).
type constFunInfo struct {
	pos      []int
	lits     map[int]map[string]bool
	open     map[int]bool
	nameSpan span.Span
}

// checkConstCalls verifies every call to a const-slotted free function in the
// file: the const argument must be a parse-time constant, and it must match an
// implemented clause literal (or an open slot).
func (w *walker) checkConstCalls(scope *Scope, sets []clauseSet, tree []ast.PNode) {
	infos := map[string]*constFunInfo{}
	// Const positions come from each set's adjacent sig; only free functions
	// (methods would need receiver typing — left gradual).
	for _, set := range sets {
		if set.isMethod || set.adjacentSig == nil {
			continue
		}
		br, ok := asList(set.adjacentSig.ArgList)
		if !ok {
			continue
		}
		info := infos[set.qname]
		if info == nil {
			info = &constFunInfo{lits: map[int]map[string]bool{}, open: map[int]bool{}}
			infos[set.qname] = info
		}
		for i, slot := range br.Children {
			if sigParamMod(slot) != "const" {
				continue
			}
			if info.lits[i] == nil {
				info.pos = append(info.pos, i)
				info.lits[i] = map[string]bool{}
			}
			for _, cl := range set.clauses {
				cbr, ok := asList(cl.ArgList)
				if !ok || i >= len(cbr.Children) {
					continue
				}
				slot := cbr.Children[i]
				if !patternConstrains(slot) {
					info.open[i] = true
					continue
				}
				if lf, ok := slot.(*ast.PLeaf); ok {
					info.lits[i][lf.Value] = true
				} else {
					// A structured pattern in a const slot — treat as open
					// (matching is richer than literal identity; stay gradual).
					info.open[i] = true
				}
			}
		}
	}
	if len(infos) == 0 {
		return
	}
	for _, form := range tree {
		w.walkConstCalls(infos, form)
	}
}

// walkConstCalls descends an expression tree checking `(name args…)` calls
// against the const registry.
func (w *walker) walkConstCalls(infos map[string]*constFunInfo, n ast.PNode) {
	br, ok := n.(*ast.PBranch)
	if !ok {
		return
	}
	if br.Open == "(" && len(br.Children) > 0 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			if info := infos[head.Value]; info != nil {
				w.checkConstCall(info, head, br.Children[1:])
			}
		}
	}
	for _, c := range br.Children {
		w.walkConstCalls(infos, c)
	}
}

// checkConstCall checks one call's const-slot arguments.
func (w *walker) checkConstCall(info *constFunInfo, head *ast.PLeaf, args []ast.PNode) {
	for _, pos := range info.pos {
		if pos >= len(args) {
			continue // arity is checked elsewhere
		}
		key, isConst := parseTimeConstant(args[pos])
		if !isConst {
			w.emit(Diagnostic{
				File: w.file, Span: args[pos].GetSpan(), Severity: SeverityError, Code: "const-arg-not-static",
				Message: fmt.Sprintf("this argument of '%s' is a (const …) slot — pass a parse-time constant (a literal or a type name), not a runtime value", head.Value),
			})
			continue
		}
		if !info.open[pos] && !info.lits[pos][key] {
			w.emit(Diagnostic{
				File: w.file, Span: args[pos].GetSpan(), Severity: SeverityError, Code: "no-impl-for-const",
				Message: fmt.Sprintf("no clause of '%s' implements the constant %s", head.Value, key),
			})
		}
	}
}

// parseTimeConstant reports whether an argument is a parse-time constant — a
// literal (number, string, atom, bool, none) or a Capitalized name (a type
// value) — returning its canonical key (the leaf text).
func parseTimeConstant(n ast.PNode) (string, bool) {
	lf, ok := n.(*ast.PLeaf)
	if !ok {
		return "", false
	}
	v := lf.Value
	switch {
	case v == "true" || v == "false" || v == "none":
		return v, true
	case core.IsStrLit(v):
		return v, true
	case len(v) > 1 && v[0] == ':':
		return v, true
	}
	if len(v) > 0 && (v[0] >= '0' && v[0] <= '9' || v[0] == '-' && len(v) > 1 && v[1] >= '0' && v[1] <= '9') {
		return v, true
	}
	if looksLikeIdentifier(v) {
		c := strings.TrimPrefix(v, "#")
		if c != "" && c[0] >= 'A' && c[0] <= 'Z' {
			return v, true
		}
	}
	return "", false
}
