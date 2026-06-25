package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// PhoType is Pho's first-class type value — the descriptor behind a KindType
// value. It is a SET of runtime values, the foundation of the set-theoretic
// gradual type system (see Doc/PlanV1/GradualTyping.md). Stage A1 populates the
// `base` primitive bitset and the `structs` set (with a cofinite flag for the
// top type); later stages extend the SAME struct with atom singletons,
// structured (list/dict/arrow) components, and the gradual `dyn` flag, plus the
// full union/intersection/negation/emptiness algebra.
//
// Every PhoType is interned (hash-consed) by its canonical structure, so two
// structurally-equal types share one *PhoType pointer and KindType equality is
// an O(1) pointer compare (mirroring the *Atom intern pool in atom.go). The
// display name is an attribute of the canonical type, not part of its identity.
type PhoType struct {
	key     string                // canonical intern key (structural)
	name    string                // display name ("" => derive structurally)
	base    baseBits              // primitive value-families present
	structs map[*tstruct]struct{} // struct types: members (allStructs false) or exceptions (allStructs true)
	allStr  bool                  // structs is COFINITE — every struct EXCEPT those listed (used by the top type / negation)
	dyn     bool                  // mentions the gradual `Dynamic` type (Stage C; consumed by the checker in Stage E)

	// Singleton refinements — finite sets of literal values that refine a
	// primitive bit into exact singleton/enum types (the basis for tagged
	// unions): atoms refines bAtom (`:ok`, `(Or :ok :error)`), nums refines
	// bNum (`5`, `(Or 200 404)`), strs refines bStr (`"GET"`, `(Or "GET"
	// "POST")`), bools refines bBool (`True`). In every case nil ≡ "all values
	// of that primitive" (the bare type) and a non-nil set is EXACTLY those
	// values. internType normalizes a non-nil EMPTY set to "none" (drops the
	// bit), so `:ok ∧ :error` is correctly None, and the full bool set
	// {True,False} back to the bare Boolean. Unlike list/map these are PRECISE
	// (exact members), so enum narrowing is exact.
	atoms map[*Atom]struct{}
	nums  map[float64]struct{}
	strs  map[string]struct{}
	bools map[bool]struct{}

	// Parametric refinements (Stage F). When bList is present, listElem (nil ≡
	// Unknown) bounds the element type of lists; when bDict is present, mapKey/
	// mapVal bound the key/value types. They are COVARIANT and approximate the
	// full set-theoretic product — combination is conservative (gradual-safe).
	listElem       *PhoType
	mapKey, mapVal *PhoType

	// arrow refines bFun (nil ≡ any function). Function subtyping is
	// CONTRAVARIANT in parameters and COVARIANT in the result. Runtime
	// membership can't inspect a closure's signature, so it only checks "is a
	// function" — arrows are precise in the static checker, best-effort at
	// runtime. Combination in Or/And drops the refinement (conservative).
	arrow *arrowType

	// fields is a STRUCTURAL (record) constraint: when non-nil, this type also
	// includes every struct INSTANCE that has at least the named fields, each
	// inhabiting its bound type (an OPEN record — width subtyping). It is
	// orthogonal to the nominal `structs` set: an instance inhabits the type if
	// it matches the nominal set OR satisfies the field shape. Membership and
	// record⊆record subtyping (width + covariant depth) are precise; mixing a
	// record with a nominal struct in And can't be represented exactly, so such
	// a result is marked gradual (dyn) to stay sound (no false positives).
	fields map[string]*PhoType

	// trait is a structural, implicit interface (Doc/PlanV1/Traits.md): a value
	// inhabits the type when its TYPE provides every required method/property
	// (checked against the object-model member tables — so trait membership is
	// Context-aware, unlike base Contains). Trait identity is by traitInfo
	// pointer (each `(Trait …)` is distinct, like a struct); subtyping is
	// structural (requirement coverage).
	trait *TraitInfo
}

// TraitInfo is a Trait's FLATTENED requirement set (supertraits already folded
// in). Defaults provide behavior auto-injected onto satisfying types.
type TraitInfo struct {
	Methods    map[string]TraitMethod
	Properties map[string]TraitProperty
}

// TraitMethod is one required method. Arity is the parameter count excluding
// self (the runtime satisfaction check); Params/Result are the static-checker
// signature (nil ≡ Unknown). Default is the auto-injected implementation, or
// nil when the method is abstract.
type TraitMethod struct {
	Arity   int
	Params  []*PhoType
	Result  *PhoType
	Default Fun
}

// TraitProperty is one required property; a field of the same name satisfies it.
// Get/Set say which accessors are required; Type bounds the value (nil ≡
// Unknown). GetDefault/SetDefault are auto-injected, or nil when abstract.
type TraitProperty struct {
	Get, Set   bool
	Type       *PhoType
	GetDefault Fun
	SetDefault Fun
}

type arrowType struct {
	Params []*PhoType
	Result *PhoType
}

// ArrowType is the type of functions (params…) -> result; ArrowType(nil-ish,
// Unknown) ... but a fully-Unknown arrow is just the bare Function type.
func ArrowType(params []*PhoType, result *PhoType) *PhoType {
	return internType(&PhoType{base: bFun, arrow: &arrowType{Params: params, Result: result}})
}

// orUnknown treats a nil refinement as Unknown (the "any element" default).
func orUnknown(t *PhoType) *PhoType {
	if t == nil {
		return TypeUnknown
	}
	return t
}

// normRefine canonicalizes a refinement: Unknown ≡ nil (a bare List/Map).
func normRefine(t *PhoType) *PhoType {
	if t == nil || t == TypeUnknown {
		return nil
	}
	return t
}

// ListType is the type of lists whose elements inhabit elem; ListType(Unknown)
// is the bare List type.
func ListType(elem *PhoType) *PhoType {
	return internType(&PhoType{base: bList, listElem: normRefine(elem)})
}

// AtomSingleton / NumSingleton / StrSingleton / BoolSingleton build the
// singleton type containing exactly one literal value — the building block of
// enums. (Or :ok :error) unions two; the bare type (TypeAtom/Number/…) is the
// same component with a nil set ("all values of that primitive").
func AtomSingleton(name string) *PhoType {
	return internType(&PhoType{base: bAtom, atoms: map[*Atom]struct{}{Intern(name): {}}})
}

func NumSingleton(n float64) *PhoType {
	return internType(&PhoType{base: bNum, nums: map[float64]struct{}{n: {}}})
}

func StrSingleton(s string) *PhoType {
	return internType(&PhoType{base: bStr, strs: map[string]struct{}{s: {}}})
}

func BoolSingleton(b bool) *PhoType {
	return internType(&PhoType{base: bBool, bools: map[bool]struct{}{b: {}}})
}

// Singleton sets use nil ≡ "all values of the primitive" (the universe); a
// non-nil set is exactly its members. setCopy/setOr/setAnd/setSubset implement
// the set algebra under that convention for any comparable element type;
// setAnd may return a non-nil EMPTY set (internType then drops the base bit).
func setCopy[T comparable](a map[T]struct{}) map[T]struct{} {
	if a == nil {
		return nil
	}
	out := make(map[T]struct{}, len(a))
	for k := range a {
		out[k] = struct{}{}
	}
	return out
}

func setOr[T comparable](a, b map[T]struct{}) map[T]struct{} {
	if a == nil || b == nil { // all ∨ x = all
		return nil
	}
	out := make(map[T]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func setAnd[T comparable](a, b map[T]struct{}) map[T]struct{} {
	if a == nil { // all ∧ b = b
		return setCopy(b)
	}
	if b == nil {
		return setCopy(a)
	}
	out := map[T]struct{}{}
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

// orSet/andSet combine one singleton refinement across a union/intersection,
// given whether each side carries the corresponding base bit. orSet keeps the
// member of whichever side(s) contribute; andSet intersects only when both do
// (otherwise the base `&` has already cleared the bit, so there is no set).
func orSet[T comparable](aHas, bHas bool, as, bs map[T]struct{}) map[T]struct{} {
	switch {
	case aHas && bHas:
		return setOr(as, bs)
	case aHas:
		return setCopy(as)
	case bHas:
		return setCopy(bs)
	}
	return nil
}

func andSet[T comparable](aHas, bHas bool, as, bs map[T]struct{}) map[T]struct{} {
	if aHas && bHas {
		return setAnd(as, bs)
	}
	return nil
}

// containsSingleton reports whether literal value v (already unwrapped) is in a
// type's singleton component for one primitive: false if the base bit is
// absent, true if the set is nil (all values), else exact membership.
func containsSingleton[T comparable](base, bit baseBits, set map[T]struct{}, v T) bool {
	if base&bit == 0 {
		return false
	}
	if set == nil {
		return true
	}
	_, ok := set[v]
	return ok
}

// setSubset reports a ⊆ b under nil ≡ universe.
func setSubset[T comparable](a, b map[T]struct{}) bool {
	if b == nil { // ⊆ everything
		return true
	}
	if a == nil { // the universe ⊄ a finite set
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// MapType is the type of maps with keys in key and values in val.
func MapType(key, val *PhoType) *PhoType {
	return internType(&PhoType{base: bDict, mapKey: normRefine(key), mapVal: normRefine(val)})
}

// RecordType is the OPEN structural type of struct instances that have at least
// the named fields, each inhabiting its bound type — `{x Number y String}`. A
// nil field type means Unknown (any value in that field). Width subtyping: an
// instance may have more fields than required.
func RecordType(fields map[string]*PhoType) *PhoType {
	norm := make(map[string]*PhoType, len(fields))
	for k, v := range fields {
		norm[k] = orUnknown(v)
	}
	// allStr=true: a record is the COFINITE "every struct instance", narrowed by
	// the field shape. Carrying allStr keeps the struct dimension visible to the
	// base-emptiness subtype check (a record's base bits are 0); membership and
	// record⊆record subtyping then refine it by the fields.
	return internType(&PhoType{allStr: true, fields: norm})
}

// FieldsOf returns the record's field constraints (nil if t is not a record).
func FieldsOf(t *PhoType) map[string]*PhoType { return t.fields }

func copyFields(f map[string]*PhoType) map[string]*PhoType {
	if f == nil {
		return nil
	}
	out := make(map[string]*PhoType, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out
}

// recordOrJoin is the record component of a UNION: a value in the union need
// only satisfy ONE side, so the common constraint keeps only SHARED fields,
// each joined (Or). A nil side has no structural constraint of its own (its
// instances match nominally / via allStr), so the other side's constraint is
// kept as the structural alternative.
func recordOrJoin(a, b map[string]*PhoType) map[string]*PhoType {
	if a == nil {
		return copyFields(b)
	}
	if b == nil {
		return copyFields(a)
	}
	out := map[string]*PhoType{}
	for k, av := range a {
		if bv, ok := b[k]; ok {
			out[k] = av.Or(bv)
		}
	}
	return out
}

// recordAndMeet is the record component of an INTERSECTION: a value must
// satisfy BOTH, so the result requires the UNION of fields, shared ones And-ed.
func recordAndMeet(a, b map[string]*PhoType) map[string]*PhoType {
	if a == nil {
		return copyFields(b)
	}
	if b == nil {
		return copyFields(a)
	}
	out := copyFields(a)
	for k, bv := range b {
		if av, ok := out[k]; ok {
			out[k] = av.And(bv)
		} else {
			out[k] = bv
		}
	}
	return out
}

// recordSubtype decides the structural part of a ⊆ b once the base/struct-set
// emptiness check has passed. b imposes a field constraint only when b.fields
// is non-nil; then every required field must be present in a (width) and
// covariantly a subtype (depth). a.fields nil means a does not guarantee any
// field, so it fails any non-empty requirement (unless gradual).
func recordSubtype(a, b *PhoType) bool {
	if b.fields == nil {
		return true // b imposes no shape; the struct dimension was handled above
	}
	if !a.hasStructPart() || a.dyn || b.dyn {
		return true // a has no struct values to constrain (vacuous), or gradual
	}
	for k, bt := range b.fields {
		at, ok := a.fields[k] // a.fields nil ⇒ a's structs are unconstrained ⇒ fail
		if !ok || !Subtype(at, bt) {
			return false
		}
	}
	return true
}

// fieldsMatch reports whether instance inst satisfies the open record fields.
func fieldsMatch(fields map[string]*PhoType, inst *tinstance) bool {
	for k, ft := range fields {
		v, ok := inst.Fields[k]
		if !ok || !ft.Contains(v) {
			return false
		}
	}
	return true
}

// TraitType builds the structural-interface type for an (already-flattened)
// requirement set. Each call yields a distinct type (identity by info pointer,
// like a struct), so two structurally-identical Trait declarations are distinct
// types; their SUBTYPING is still structural (requirement coverage).
func TraitType(info *TraitInfo) *PhoType {
	t := internType(&PhoType{allStr: true, trait: info})
	registerTraitDefaults(t)
	return t
}

// The process-wide registry of traits that carry at least one DEFAULT member —
// the candidate set the dot accessor scans to auto-inject a default onto a
// value that satisfies the trait but lacks the member (Doc/PlanV1/Traits.md §6).
var (
	traitMu            sync.Mutex
	traitsWithDefaults []*PhoType
)

func registerTraitDefaults(t *PhoType) {
	if t.trait == nil {
		return
	}
	has := false
	for _, m := range t.trait.Methods {
		has = has || m.Default != nil
	}
	for _, p := range t.trait.Properties {
		has = has || p.GetDefault != nil || p.SetDefault != nil
	}
	if !has {
		return
	}
	traitMu.Lock()
	defer traitMu.Unlock()
	for _, e := range traitsWithDefaults {
		if e == t {
			return // already registered (interned identity)
		}
	}
	traitsWithDefaults = append(traitsWithDefaults, t)
}

// TraitDefaultMember finds an auto-injected default for member `name` on value v:
// a registered trait that v satisfies and that defaults `name`. `isProp` reports
// whether it is a property getter (call immediately) vs a method (return the
// callable). clash=true when two distinct satisfied traits both default it — an
// ambiguity the caller reports as an error, mirroring the member-resolution rule.
func TraitDefaultMember(ctx Context, v Tval, name string) (fn Fun, isProp, found, clash bool) {
	traitMu.Lock()
	cands := append([]*PhoType(nil), traitsWithDefaults...)
	traitMu.Unlock()
	for _, t := range cands {
		var cand Fun
		prop := false
		if m, ok := t.trait.Methods[name]; ok && m.Default != nil {
			cand = m.Default
		} else if p, ok := t.trait.Properties[name]; ok && p.GetDefault != nil {
			cand, prop = p.GetDefault, true
		} else {
			continue
		}
		if !TraitSatisfiedBy(ctx, t, v) {
			continue
		}
		if found {
			return fn, isProp, true, true // ambiguous
		}
		fn, isProp, found = cand, prop, true
	}
	return fn, isProp, found, false
}

// TraitOf returns t's trait requirement set, or ok=false when t is not a trait.
func TraitOf(t *PhoType) (*TraitInfo, bool) { return t.trait, t.trait != nil }

// TraitSatisfiedBy reports whether value v's TYPE provides every required
// member of trait t — Context-aware membership, since it reads the per-package
// member tables. A method is satisfied by a struct method or an extension/
// universal method of that name (runtime check is by name; full signature is
// the static checker's job). A property is satisfied by a property OR a FIELD
// of that name with compatible get/set. Defaulted members are auto-filled and
// never constrain satisfaction.
func TraitSatisfiedBy(ctx Context, t *PhoType, v Tval) bool {
	if t.trait == nil {
		return true
	}
	typeKey := TypeKeyOf(v)
	inst, isInst := v.Val.(*tinstance)

	hasMethod := func(name string) bool {
		if isInst {
			if _, ok := inst.Struct.Methods[name]; ok {
				return true
			}
		}
		res := ctx.ResolveMember(typeKey, name)
		return res.Found && !res.IsProperty
	}
	// propCaps reports the read/write capability of a member named `name` — a
	// field is read+write; a property is read, and write iff it has a setter.
	propCaps := func(name string) (get, set, found bool) {
		if isInst {
			for _, f := range inst.Struct.Fields {
				if f == name {
					return true, true, true
				}
			}
			if p, ok := inst.Struct.Properties[name]; ok {
				return true, p.HasSetter, true
			}
		}
		if res := ctx.ResolveMember(typeKey, name); res.Found && res.IsProperty {
			return true, res.Property.HasSetter, true
		}
		return false, false, false
	}

	for name, m := range t.trait.Methods {
		if m.Default == nil && !hasMethod(name) {
			return false
		}
	}
	for name, p := range t.trait.Properties {
		needGet := p.Get && p.GetDefault == nil
		needSet := p.Set && p.SetDefault == nil
		if !needGet && !needSet {
			continue
		}
		get, set, found := propCaps(name)
		if !found || (needGet && !get) || (needSet && !set) {
			return false
		}
	}
	return true
}

// traitSubtype decides the trait dimension of a ⊆ b after the base-emptiness
// check. Value⊆trait (a is a concrete type, b a trait) is NOT decidable here —
// it needs the object-model member tables — so it is conservatively false; the
// Context-aware membership (TraitSatisfiedBy) and the static checker decide it.
// trait⊆trait is structural: a must require at least b's (non-defaulted)
// members.
func traitSubtype(a, b *PhoType) bool {
	if b.trait == nil {
		return true // b imposes no trait requirement
	}
	if a.trait == nil {
		// a concrete type's satisfaction is decided elsewhere (runtime/checker);
		// but a type with no struct VALUES (e.g. None) or gradual passes
		// vacuously — nothing to fail.
		return !a.hasStructPart() || a.dyn
	}
	for name, bm := range b.trait.Methods {
		if bm.Default != nil {
			continue // auto-filled for any value ⇒ a need not require it
		}
		am, ok := a.trait.Methods[name]
		if !ok || am.Arity != bm.Arity {
			return false
		}
	}
	for name, bp := range b.trait.Properties {
		if bp.GetDefault != nil && bp.SetDefault != nil {
			continue
		}
		ap, ok := a.trait.Properties[name]
		if !ok || (bp.Get && !ap.Get) || (bp.Set && !ap.Set) {
			return false
		}
	}
	return true
}

// refineOr / refineAnd compute the covariant refinement of a structured
// component in a union / intersection, given whether each side carries the
// component's base bit.
func refineOr(aHas, bHas bool, ae, be *PhoType) *PhoType {
	switch {
	case aHas && bHas:
		if ae == nil && be == nil { // Unknown ∨ Unknown = Unknown ≡ nil — avoids recursion
			return nil
		}
		return normRefine(orUnknown(ae).Or(orUnknown(be)))
	case aHas:
		return ae
	case bHas:
		return be
	}
	return nil
}

func refineAnd(aHas, bHas bool, ae, be *PhoType) *PhoType {
	if aHas && bHas {
		if ae == nil && be == nil { // Unknown ∧ Unknown = Unknown ≡ nil
			return nil
		}
		return normRefine(orUnknown(ae).And(orUnknown(be)))
	}
	return nil
}

// TypeID is a stable identity for a nominal type, used as the key for
// per-package method/property tables and (later) for set-theoretic struct
// literals:
//
//	primitive: "prim:num" "prim:str" "prim:list" "prim:dict" "prim:bool"
//	           "prim:chr" "prim:atom" "prim:fun" "prim:nil" "prim:type"
//	top:       "unknown"   bottom: "none"
//	struct:    "<declaring-pkg-path>#<Name>"  (assigned in Stage A2; Stage A1
//	           identifies struct types by their *tstruct pointer)
type TypeID string

// baseBits is a bitset over the disjoint PRIMITIVE value-families a type may
// include. A single primitive type sets exactly one bit; a union sets several;
// the top type sets all. Struct instances are tracked in PhoType.structs, not
// here.
type baseBits uint16

const (
	bNum  baseBits = 1 << iota
	bList          // runtime KindArray
	bDict
	bStr
	bChr
	bAtom
	bBool
	bNil
	bFun  // KindFun / KindMacro / KindMethod all read as Function
	bType // KindType values (struct-type values and primitive type values alike)

	baseAll = bNum | bList | bDict | bStr | bChr | bAtom | bBool | bNil | bFun | bType
)

// The interned type pool — process-global, like atomPool. Types are never
// removed; the set of types a program can name is bounded by its source plus
// the finitely-many structs it declares.
var (
	typeMu   sync.Mutex
	typePool = map[string]*PhoType{}
)

// internType canonicalizes t (sets its key) and returns the pooled
// representative for that structure, so structurally-equal types share one
// pointer. The supplied *PhoType is discarded if an equal one already exists.
// normSingleton normalizes one singleton refinement: its base bit must be
// present iff the set is inhabited. nil ≡ all values (keep the bit); a non-nil
// EMPTY set ≡ none (drop the bit, e.g. `:ok ∧ :error`); no bit ⇒ no set.
func normSingleton[T comparable](base *baseBits, bit baseBits, set *map[T]struct{}) {
	if *base&bit != 0 && *set != nil && len(*set) == 0 {
		*base &^= bit
	}
	if *base&bit == 0 {
		*set = nil
	}
}

func internType(t *PhoType) *PhoType {
	// The bool domain is finite, so the full {True,False} set is just "all
	// bools" — collapse it to the bare Boolean (nil) before normalizing.
	if len(t.bools) == 2 {
		t.bools = nil
	}
	normSingleton(&t.base, bAtom, &t.atoms)
	normSingleton(&t.base, bNum, &t.nums)
	normSingleton(&t.base, bStr, &t.strs)
	normSingleton(&t.base, bBool, &t.bools)
	t.key = t.canonicalKey()
	typeMu.Lock()
	defer typeMu.Unlock()
	if ex, ok := typePool[t.key]; ok {
		return ex
	}
	typePool[t.key] = t
	return t
}

// canonicalKey renders the structural identity of t — base bits, the cofinite
// flag, and the sorted struct-pointer set. The display name is deliberately
// excluded: identity is structural, names are assigned to canonical types.
func (t *PhoType) canonicalKey() string {
	var b strings.Builder
	fmt.Fprintf(&b, "b%d", t.base)
	if t.dyn {
		b.WriteString("|dyn")
	}
	if t.allStr {
		b.WriteString("|S*")
	}
	if len(t.structs) > 0 {
		ids := make([]string, 0, len(t.structs))
		for st := range t.structs {
			ids = append(ids, fmt.Sprintf("%p", st))
		}
		sort.Strings(ids)
		b.WriteString("|s" + strings.Join(ids, ","))
	}
	if t.atoms != nil {
		names := make([]string, 0, len(t.atoms))
		for at := range t.atoms {
			names = append(names, at.Name())
		}
		sort.Strings(names)
		b.WriteString("|A" + strings.Join(names, ","))
	}
	if t.nums != nil {
		ns := make([]string, 0, len(t.nums))
		for n := range t.nums {
			ns = append(ns, strconv.FormatFloat(n, 'g', -1, 64))
		}
		sort.Strings(ns)
		b.WriteString("|N" + strings.Join(ns, ","))
	}
	if t.strs != nil {
		ss := make([]string, 0, len(t.strs))
		for s := range t.strs {
			ss = append(ss, QuoteStrLit(s))
		}
		sort.Strings(ss)
		b.WriteString("|R" + strings.Join(ss, ","))
	}
	if t.bools != nil {
		bs := ""
		if _, ok := t.bools[false]; ok {
			bs += "F"
		}
		if _, ok := t.bools[true]; ok {
			bs += "T"
		}
		b.WriteString("|B" + bs)
	}
	if t.listElem != nil {
		b.WriteString("|L" + t.listElem.key)
	}
	if t.mapKey != nil {
		b.WriteString("|K" + t.mapKey.key)
	}
	if t.mapVal != nil {
		b.WriteString("|V" + t.mapVal.key)
	}
	if t.arrow != nil {
		b.WriteString("|F(")
		for i, p := range t.arrow.Params {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(p.key)
		}
		b.WriteString(")" + t.arrow.Result.key)
	}
	if t.fields != nil {
		keys := make([]string, 0, len(t.fields))
		for k := range t.fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("|Rec{")
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(k + ":" + t.fields[k].key)
		}
		b.WriteByte('}')
	}
	if t.trait != nil {
		// Identity by pointer: each (Trait …) declaration is a distinct type.
		fmt.Fprintf(&b, "|Trait%p", t.trait)
	}
	return b.String()
}

// The pre-interned primitive, top, and bottom types. Bound to their builtin
// names in pkg/builtins/typeval.go; returned by TvTypeOf.
var (
	TypeUnknown  *PhoType // ⊤ — every value
	TypeNone     *PhoType // ⊥ — no value
	TypeNumber   *PhoType
	TypeString   *PhoType
	TypeList     *PhoType // the list type (runtime kind "array")
	TypeDict     *PhoType
	TypeBoolean  *PhoType
	TypeChar     *PhoType
	TypeAtom     *PhoType
	TypeFunction *PhoType
	TypeNil      *PhoType
	TypeType     *PhoType // the type of type values

	TypeCollection *PhoType // String | List | Map — the iterables
	TypeDynamic    *PhoType // the gradual type: every value, flagged gradual
	TypeStruct     *PhoType // any struct instance — the open record base, refined by `Struct.{ … }`
)

func init() {
	prim := func(b baseBits, name string) *PhoType {
		return internType(&PhoType{base: b, name: name})
	}
	TypeNumber = prim(bNum, "Number")
	TypeString = prim(bStr, "String")
	TypeList = prim(bList, "List")
	TypeDict = prim(bDict, "Map")
	TypeBoolean = prim(bBool, "Boolean")
	TypeChar = prim(bChr, "Char")
	TypeAtom = prim(bAtom, "Atom")
	TypeFunction = prim(bFun, "Function")
	TypeNil = prim(bNil, "Nil")
	TypeType = prim(bType, "Type")
	TypeUnknown = internType(&PhoType{base: baseAll, allStr: true, name: "Unknown"})
	TypeNone = internType(&PhoType{name: "None"})

	// Collection is the union of the iterable kinds; Dynamic is the top type
	// flagged gradual (it contains every value, so `(x.Is? Dynamic)` is always
	// true, but the checker can detect it via IsGradual).
	TypeCollection = TypeList.Or(TypeDict).Or(TypeString)
	TypeCollection.name = "Collection"
	TypeDynamic = internType(&PhoType{base: baseAll, allStr: true, dyn: true, name: "Dynamic"})

	// Struct is the open record base — every struct instance, with no required
	// fields. `Struct.{ X Number Y Number }` refines it to a specific shape.
	TypeStruct = RecordType(map[string]*PhoType{})
	TypeStruct.name = "Struct"
}

// Name returns the type's display string — its assigned name, or a structural
// rendering for composite types that have none.
func (t *PhoType) Name() string {
	if t.name != "" {
		return t.name
	}
	return t.render()
}

// render builds a display string from structure for an unnamed composite. In
// Stage A1 every constructed type is named, so this is a forward-compatible
// fallback (exercised once unions/negation land in Stage C).
func (t *PhoType) render() string {
	if t.base == 0 && len(t.structs) == 0 && !t.allStr && t.fields == nil {
		return "None"
	}
	// A trait renders as `(Trait <member names>)`.
	if t.trait != nil {
		names := make([]string, 0, len(t.trait.Methods)+len(t.trait.Properties))
		for k := range t.trait.Methods {
			names = append(names, k)
		}
		for k := range t.trait.Properties {
			names = append(names, k)
		}
		sort.Strings(names)
		return "(Trait " + strings.Join(names, " ") + ")"
	}
	// A pure structural record renders as its field shape `{x Number y String}`.
	if t.fields != nil && t.base == 0 && len(t.structs) == 0 && t.allStr && !t.dyn {
		keys := make([]string, 0, len(t.fields))
		for k := range t.fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + " " + t.fields[k].Name()
		}
		return "{" + strings.Join(parts, " ") + "}"
	}
	// Pure parametric collections render with their element types.
	if t.base == bList && t.listElem != nil && !t.allStr && len(t.structs) == 0 && !t.dyn {
		return "[" + t.listElem.Name() + "]"
	}
	if t.base == bDict && (t.mapKey != nil || t.mapVal != nil) && !t.allStr && len(t.structs) == 0 && !t.dyn {
		return "{" + orUnknown(t.mapKey).Name() + " " + orUnknown(t.mapVal).Name() + "}"
	}
	if t.base == bFun && t.arrow != nil && !t.allStr && len(t.structs) == 0 && !t.dyn {
		parts := make([]string, len(t.arrow.Params))
		for i, p := range t.arrow.Params {
			parts[i] = p.Name()
		}
		return "(Fun [" + strings.Join(parts, " ") + "] " + t.arrow.Result.Name() + ")"
	}
	parts := make([]string, 0, 4)
	for _, bn := range baseNames {
		if t.base&bn.bit == 0 {
			continue
		}
		// A finite singleton set renders as its literals (":ok", 5, "GET",
		// True); a nil set renders as the bare primitive name.
		if names := t.singletonNames(bn.bit); names != nil {
			parts = append(parts, names...)
			continue
		}
		parts = append(parts, bn.name)
	}
	if t.allStr {
		parts = append(parts, "Struct…")
	} else {
		names := make([]string, 0, len(t.structs))
		for st := range t.structs {
			names = append(names, structName(st))
		}
		sort.Strings(names)
		parts = append(parts, names...)
	}
	return strings.Join(parts, " | ")
}

// singletonNames returns the sorted display forms of a finite singleton set
// for one primitive bit (":ok", 5, "GET", True), or nil if that bit has no
// finite refinement (the bare primitive) — used by render.
func (t *PhoType) singletonNames(bit baseBits) []string {
	switch bit {
	case bAtom:
		if t.atoms == nil {
			return nil
		}
		out := make([]string, 0, len(t.atoms))
		for at := range t.atoms {
			out = append(out, ":"+at.Name())
		}
		sort.Strings(out)
		return out
	case bNum:
		if t.nums == nil {
			return nil
		}
		fs := make([]float64, 0, len(t.nums))
		for n := range t.nums {
			fs = append(fs, n)
		}
		sort.Float64s(fs)
		out := make([]string, len(fs))
		for i, n := range fs {
			out[i] = strconv.FormatFloat(n, 'g', -1, 64)
		}
		return out
	case bStr:
		if t.strs == nil {
			return nil
		}
		out := make([]string, 0, len(t.strs))
		for s := range t.strs {
			out = append(out, QuoteStrLit(s))
		}
		sort.Strings(out)
		return out
	case bBool:
		if t.bools == nil {
			return nil
		}
		out := make([]string, 0, 2)
		if _, ok := t.bools[false]; ok {
			out = append(out, "False")
		}
		if _, ok := t.bools[true]; ok {
			out = append(out, "True")
		}
		return out
	}
	return nil
}

var baseNames = []struct {
	bit  baseBits
	name string
}{
	{bNum, "Number"}, {bList, "List"}, {bDict, "Map"}, {bStr, "String"},
	{bChr, "Char"}, {bAtom, "Atom"}, {bBool, "Boolean"}, {bNil, "Nil"},
	{bFun, "Function"}, {bType, "Type"},
}

// TvTypeOf returns the most-precise type of a runtime value — the runtime's
// complete type knowledge. Stage A1 is nominal: numbers are Number, atoms are
// Atom (singleton atom types arrive in a later phase), a struct instance is its
// struct's type.
func TvTypeOf(v Tval) *PhoType {
	switch v.Kind {
	case KindNum:
		return TypeNumber
	case KindStr:
		return TypeString
	case KindArray:
		return TypeList
	case KindDict:
		return TypeDict
	case KindBool:
		return TypeBoolean
	case KindChr:
		return TypeChar
	case KindAtom:
		return TypeAtom
	case KindNil:
		return TypeNil
	case KindFun, KindMacro, KindMethod:
		return TypeFunction
	case KindType:
		return TypeType
	case KindInstance:
		return structType(v.Val.(*tinstance).Struct)
	}
	return TypeUnknown
}

// structType returns the interned single-struct type for st.
func structType(st *tstruct) *PhoType {
	t := &PhoType{structs: map[*tstruct]struct{}{st: {}}, name: structName(st)}
	return internType(t)
}

// structName recovers a struct's declared name — from the nominal registry
// (the authoritative source after Stage A2), falling back to the origin-env
// reverse-lookup Stringify uses; "<struct>" on a miss.
func structName(st *tstruct) string {
	if info := nominalOf(st); info != nil && info.Name != "" {
		return info.Name
	}
	if st != nil && st.Origin != nil {
		for n, other := range st.Origin.Structs {
			if st == other {
				return n
			}
		}
	}
	return "<struct>"
}

// NominalInfo is the per-struct metadata the type system needs beyond the
// set-theoretic descriptor: the display name, the declaring package path (for
// the Stage-B origin rule), the stable "<pkg>#Name" id, and the constructor
// closure that builds instances. The descriptor itself identifies a struct
// type by the *tstruct pointer (unique per declaration, hence collision-free
// across reloads and test programs); this registry hangs the nominal data off
// that same pointer.
type NominalInfo struct {
	Name        string
	OriginPath  string
	ID          TypeID
	Constructor tfun
}

var (
	registryMu     sync.Mutex
	structRegistry = map[*tstruct]*NominalInfo{}
)

// RegisterStruct records a struct's nominal metadata and returns its interned
// type value (a single-struct PhoType). Called by the `struct` builtin so a
// struct's name evaluates to a KindType carrying its constructor.
func RegisterStruct(st *tstruct, name, originPath string, ctor tfun) *PhoType {
	registryMu.Lock()
	structRegistry[st] = &NominalInfo{
		Name:        name,
		OriginPath:  originPath,
		ID:          TypeID(originPath + "#" + name),
		Constructor: ctor,
	}
	registryMu.Unlock()
	return structType(st) // outside the lock — structName re-takes registryMu
}

func nominalOf(st *tstruct) *NominalInfo {
	registryMu.Lock()
	defer registryMu.Unlock()
	return structRegistry[st]
}

// StructOf returns the single struct a struct-type value denotes, or ok=false
// for a primitive, composite, top/bottom, or multi-struct type.
func StructOf(t *PhoType) (*tstruct, bool) {
	if t.base != 0 || t.allStr || len(t.structs) != 1 {
		return nil, false
	}
	for st := range t.structs {
		return st, true
	}
	return nil, false
}

// allBaseBits lists every primitive family bit, for enumerating a union.
var allBaseBits = []baseBits{bNum, bList, bDict, bStr, bChr, bAtom, bBool, bNil, bFun, bType}

// MemberKeys returns the extension-table keys of the concrete member types of a
// FINITE union of primitives — e.g. Collection (String | List | Map) yields
// ["prim:str", "prim:list", "prim:dict"]. This lets a method or property attach
// to every member at once: `(method Collection.Size …)` registers under each
// key, so it dispatches on any string, list, or map. Returns nil for a type
// that isn't a finite primitive union — the gradual type, a cofinite (negated)
// set, the empty type, or one that includes struct types (whose methods live on
// the struct itself, not the extension table).
func (t *PhoType) MemberKeys() []string {
	if t.dyn || t.allStr || len(t.structs) > 0 || t.base == 0 {
		return nil
	}
	var keys []string
	for _, bit := range allBaseBits {
		if t.base&bit != 0 {
			keys = append(keys, internType(&PhoType{base: bit}).TypeKey())
		}
	}
	return keys
}

// MemberTypeNames returns the display names of the concrete member types of a
// finite primitive union with ≥2 members — e.g. Collection → ["List", "Map",
// "String"]. For the linter to mirror the runtime's union-receiver expansion:
// a `(method Collection.Foo …)` is registered on each member's surface so a
// member access on a concrete list/string/map resolves. Returns nil for a
// single type or a non-enumerable one.
func MemberTypeNames(t *PhoType) []string {
	if len(t.MemberKeys()) < 2 {
		return nil
	}
	var names []string
	for _, bn := range baseNames {
		if t.base&bn.bit != 0 {
			names = append(names, bn.name)
		}
	}
	return names
}

// ConstructorOf returns the constructor of a constructible (single-struct)
// type, or ok=false if t is not a struct type.
func ConstructorOf(t *PhoType) (tfun, bool) {
	st, ok := StructOf(t)
	if !ok {
		return nil, false
	}
	if info := nominalOf(st); info != nil && info.Constructor != nil {
		return info.Constructor, true
	}
	return nil, false
}

// ----------------------------------------------------------------------
// Set-theoretic Boolean algebra (Stage C)
//
// A type is a set of values, decomposed into a primitive bitset and a
// finite/cofinite set of struct types. Union/intersection/negation operate
// component-wise; subtyping reduces to emptiness — a ⊆ b iff a ∧ ¬b ≡ ∅.
// This fragment (base kinds + structs) is decidable directly; atoms,
// structured types, and arrows extend the SAME operations in later stages.
// ----------------------------------------------------------------------

// Or returns the union a ∪ b.
func (a *PhoType) Or(b *PhoType) *PhoType {
	t := &PhoType{base: a.base | b.base, dyn: a.dyn || b.dyn}
	switch {
	case !a.allStr && !b.allStr:
		t.structs, t.allStr = sUnion(a.structs, b.structs), false
	case a.allStr && b.allStr:
		// (U\Ea) ∪ (U\Eb) = U \ (Ea ∩ Eb)
		t.structs, t.allStr = sInter(a.structs, b.structs), true
	case a.allStr:
		// (U\Ea) ∪ b = U \ (Ea \ b)
		t.structs, t.allStr = sDiff(a.structs, b.structs), true
	default:
		t.structs, t.allStr = sDiff(b.structs, a.structs), true
	}
	t.atoms = orSet(a.base&bAtom != 0, b.base&bAtom != 0, a.atoms, b.atoms)
	t.nums = orSet(a.base&bNum != 0, b.base&bNum != 0, a.nums, b.nums)
	t.strs = orSet(a.base&bStr != 0, b.base&bStr != 0, a.strs, b.strs)
	t.bools = orSet(a.base&bBool != 0, b.base&bBool != 0, a.bools, b.bools)
	aL, bL := a.base&bList != 0, b.base&bList != 0
	t.listElem = refineOr(aL, bL, a.listElem, b.listElem)
	aD, bD := a.base&bDict != 0, b.base&bDict != 0
	t.mapKey = refineOr(aD, bD, a.mapKey, b.mapKey)
	t.mapVal = refineOr(aD, bD, a.mapVal, b.mapVal)
	// arrow: keep the refinement only when exactly one side contributes a
	// function; the union of two distinct arrows degrades to the bare Function.
	switch aF, bF := a.base&bFun != 0, b.base&bFun != 0; {
	case aF && bF:
		t.arrow = nil
	case aF:
		t.arrow = a.arrow
	case bF:
		t.arrow = b.arrow
	}
	// Record fields in a union: if either side contributes UNCONSTRAINED structs
	// (a nominal set, or all-structs with no shape), the union includes them
	// unconstrained, so no field shape survives (sound over-approx). Otherwise
	// both sides constrain their structs by shape — keep the shared join.
	aUncon := (a.allStr && a.fields == nil) || len(a.structs) > 0
	bUncon := (b.allStr && b.fields == nil) || len(b.structs) > 0
	switch {
	case aUncon || bUncon:
		t.fields = nil
	case a.hasStructPart() || b.hasStructPart():
		t.fields = recordOrJoin(a.fields, b.fields)
	}
	return internType(t)
}

// hasStructPart reports whether t includes struct instances — nominally (a
// struct set or the cofinite allStr) or structurally (a record field shape).
func (t *PhoType) hasStructPart() bool {
	return t.allStr || len(t.structs) > 0 || t.fields != nil
}

// And returns the intersection a ∩ b.
func (a *PhoType) And(b *PhoType) *PhoType {
	t := &PhoType{base: a.base & b.base, dyn: a.dyn || b.dyn}
	switch {
	case !a.allStr && !b.allStr:
		t.structs, t.allStr = sInter(a.structs, b.structs), false
	case a.allStr && b.allStr:
		// (U\Ea) ∩ (U\Eb) = U \ (Ea ∪ Eb)
		t.structs, t.allStr = sUnion(a.structs, b.structs), true
	case a.allStr:
		// (U\Ea) ∩ b = b \ Ea
		t.structs, t.allStr = sDiff(b.structs, a.structs), false
	default:
		t.structs, t.allStr = sDiff(a.structs, b.structs), false
	}
	// Both sides carry the bit ⇒ intersect the singleton sets (may go empty,
	// which internType collapses by dropping the bit). If only one side carries
	// it, the base `&` already cleared it, so there is no set to carry.
	t.atoms = andSet(a.base&bAtom != 0, b.base&bAtom != 0, a.atoms, b.atoms)
	t.nums = andSet(a.base&bNum != 0, b.base&bNum != 0, a.nums, b.nums)
	t.strs = andSet(a.base&bStr != 0, b.base&bStr != 0, a.strs, b.strs)
	t.bools = andSet(a.base&bBool != 0, b.base&bBool != 0, a.bools, b.bools)
	aL, bL := a.base&bList != 0, b.base&bList != 0
	t.listElem = refineAnd(aL, bL, a.listElem, b.listElem)
	aD, bD := a.base&bDict != 0, b.base&bDict != 0
	t.mapKey = refineAnd(aD, bD, a.mapKey, b.mapKey)
	t.mapVal = refineAnd(aD, bD, a.mapVal, b.mapVal)
	// Record fields: a value must satisfy BOTH, so meet (union of fields) — but
	// only when both sides include some struct part (else the struct dimension
	// is empty and there is nothing to constrain). A nominal struct intersected
	// with a structural shape can't be represented exactly (membership is OR,
	// not AND, across nominal+structural), so degrade that mix to gradual to
	// stay sound — no false positives in narrowing.
	if a.hasStructPart() && b.hasStructPart() {
		t.fields = recordAndMeet(a.fields, b.fields)
		aNominalOnly := a.fields == nil && (a.allStr || len(a.structs) > 0)
		bNominalOnly := b.fields == nil && (b.allStr || len(b.structs) > 0)
		if t.fields != nil && ((a.fields != nil && bNominalOnly) || (b.fields != nil && aNominalOnly)) {
			t.dyn = true
		}
	}
	return internType(t)
}

// Not returns the complement ¬a within the universe of all values. The atom
// refinement is deliberately dropped: ¬(:ok) loses "the other atoms" and
// becomes just the non-atom values (atoms nil ≡ all only when bAtom turns ON,
// i.e. a had no atom component). This under-approximates the complement, which
// is gradual-SAFE (a smaller negation only ever makes a Subtype check more
// permissive — never a false positive). Precise enum narrowing instead rides on
// And against the positive singleton in the THEN-branch. The record `fields`
// shape is likewise dropped (¬a carries no field constraint).
func (a *PhoType) Not() *PhoType {
	return internType(&PhoType{
		base:    baseAll &^ a.base,
		structs: sCopy(a.structs),
		allStr:  !a.allStr,
		dyn:     a.dyn,
	})
}

// Diff returns the difference a \ b (= a ∩ ¬b).
func (a *PhoType) Diff(b *PhoType) *PhoType { return a.And(b.Not()) }

// IsEmpty reports whether a denotes no values (≡ ⊥ / None). A cofinite struct
// component is never empty. The gradual flag does not affect emptiness.
func (a *PhoType) IsEmpty() bool {
	return a.base == 0 && !a.allStr && len(a.structs) == 0
}

// IsGradual reports whether a mentions the dynamic type — the signal the
// static checker uses to apply the gradual guarantee (Stage E).
func (a *PhoType) IsGradual() bool { return a.dyn }

// Subtype reports whether every value of type a is also a value of type b
// (a ⊆ b), decided by emptiness of a ∧ ¬b.
func Subtype(a, b *PhoType) bool {
	if a == b { // interned: identical types short-circuit
		return true
	}
	if !a.And(b.Not()).IsEmpty() {
		return false
	}
	// Covariant refinement: where a and b share a structured component, a's
	// element type must be a subtype of b's. (Done directly, not via emptiness:
	// the base bitset abstracts `(List X)` to "a list".)
	if a.base&bList != 0 && b.base&bList != 0 && !Subtype(orUnknown(a.listElem), orUnknown(b.listElem)) {
		return false
	}
	if a.base&bDict != 0 && b.base&bDict != 0 {
		if !Subtype(orUnknown(a.mapKey), orUnknown(b.mapKey)) || !Subtype(orUnknown(a.mapVal), orUnknown(b.mapVal)) {
			return false
		}
	}
	// Singletons: the base bitset abstracts every literal to its primitive bit,
	// so the precise set inclusion is decided here (nil ≡ all values of that
	// primitive). This is exact, not covariant-approximate — `:ok ⊄ :error`,
	// `5 ⊆ (Or 5 6)`, `"GET" ⊆ String`.
	if a.base&bAtom != 0 && b.base&bAtom != 0 && !setSubset(a.atoms, b.atoms) {
		return false
	}
	if a.base&bNum != 0 && b.base&bNum != 0 && !setSubset(a.nums, b.nums) {
		return false
	}
	if a.base&bStr != 0 && b.base&bStr != 0 && !setSubset(a.strs, b.strs) {
		return false
	}
	if a.base&bBool != 0 && b.base&bBool != 0 && !setSubset(a.bools, b.bools) {
		return false
	}
	// Function arrows: contravariant parameters, covariant result. A bare
	// Function (a.arrow nil) is NOT a subtype of a specific arrow.
	if a.base&bFun != 0 && b.base&bFun != 0 && b.arrow != nil {
		if a.arrow == nil || len(a.arrow.Params) != len(b.arrow.Params) {
			return false
		}
		for i := range a.arrow.Params {
			if !Subtype(b.arrow.Params[i], a.arrow.Params[i]) { // contravariant
				return false
			}
		}
		if !Subtype(a.arrow.Result, b.arrow.Result) { // covariant
			return false
		}
	}
	// Structural records: width + covariant depth. The struct-set dimension
	// already passed the base-emptiness check (a record's base is 0), so the
	// field constraints decide here.
	if (a.fields != nil || b.fields != nil) && !recordSubtype(a, b) {
		return false
	}
	// Structural traits: requirement coverage (value⊆trait is decided elsewhere).
	if (a.trait != nil || b.trait != nil) && !traitSubtype(a, b) {
		return false
	}
	return true
}

// sCopy clones a struct set.
func sCopy(s map[*tstruct]struct{}) map[*tstruct]struct{} {
	out := make(map[*tstruct]struct{}, len(s))
	for k := range s {
		out[k] = struct{}{}
	}
	return out
}

// sUnion = a ∪ b.
func sUnion(a, b map[*tstruct]struct{}) map[*tstruct]struct{} {
	out := sCopy(a)
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// sInter = a ∩ b.
func sInter(a, b map[*tstruct]struct{}) map[*tstruct]struct{} {
	out := map[*tstruct]struct{}{}
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

// sDiff = a \ b.
func sDiff(a, b map[*tstruct]struct{}) map[*tstruct]struct{} {
	out := map[*tstruct]struct{}{}
	for k := range a {
		if _, ok := b[k]; !ok {
			out[k] = struct{}{}
		}
	}
	return out
}

// Contains reports whether the value v inhabits type t (v ∈ ⟦t⟧) — the runtime
// membership test behind Is?. Equivalent to TvTypeOf(v) being a subtype of t.
func (t *PhoType) Contains(v Tval) bool {
	switch v.Kind {
	case KindArray:
		if t.base&bList == 0 {
			return false
		}
		if t.listElem != nil {
			for _, e := range *v.Val.(*[]Tval) {
				if !t.listElem.Contains(e) {
					return false
				}
			}
		}
		return true
	case KindDict:
		if t.base&bDict == 0 {
			return false
		}
		if t.mapKey != nil || t.mapVal != nil {
			for k, val := range *v.Val.(*map[Tval]Tval) {
				if t.mapKey != nil && !t.mapKey.Contains(k) {
					return false
				}
				if t.mapVal != nil && !t.mapVal.Contains(val) {
					return false
				}
			}
		}
		return true
	case KindFun, KindMethod, KindMacro:
		// A closure carries no domain/codomain witness, so runtime membership
		// can't verify a specific arrow — only that the value IS a function.
		return t.base&bFun != 0
	case KindAtom:
		return containsSingleton(t.base, bAtom, t.atoms, v.Val.(*Atom))
	case KindNum:
		return containsSingleton(t.base, bNum, t.nums, v.Val.(float64))
	case KindStr:
		return containsSingleton(t.base, bStr, t.strs, v.Val.(string))
	case KindBool:
		return containsSingleton(t.base, bBool, t.bools, v.Val.(bool))
	case KindInstance:
		inst := v.Val.(*tinstance)
		if !t.allStr {
			// Finite nominal set: explicit membership (no field constraint), or
			// a structural record contributed by a union.
			if _, ok := t.structs[inst.Struct]; ok {
				return true
			}
			return t.fields != nil && fieldsMatch(t.fields, inst)
		}
		// Cofinite "all structs except the exceptions" — a record narrows it by
		// the field shape; without a shape it is every struct.
		if _, excepted := t.structs[inst.Struct]; excepted {
			return false
		}
		return t.fields == nil || fieldsMatch(t.fields, inst)
	}
	return Subtype(TvTypeOf(v), t)
}

// TvType wraps an interned *PhoType as a KindType value.
func TvType(t *PhoType) Tval {
	return Tval{t, KindType}
}

// buildFromType is the result of CALLING a type value: constructing a struct
// instance, or building a parametric collection type — `(List T)` / `(Map K V)`.
// ok=false means the type isn't callable; the caller emits a not-constructible
// error.
func buildFromType(ctx Context, pt *PhoType, args []ttnode) (Tval, bool) {
	if ctor, ok := ConstructorOf(pt); ok {
		return ctor(ctx, args), true
	}
	switch pt {
	case TypeList:
		if len(args) == 1 {
			if e, ok := argAsType(ctx, args[0]); ok {
				return TvType(ListType(e)), true
			}
		}
	case TypeDict:
		if len(args) == 2 {
			k, ok1 := argAsType(ctx, args[0])
			v, ok2 := argAsType(ctx, args[1])
			if ok1 && ok2 {
				return TvType(MapType(k, v)), true
			}
		}
	case TypeStruct:
		// `Struct.{ X Number Y Number }` parses to (Struct "X" Number "Y" …):
		// alternating field-name string + field-type. Builds an open record.
		if len(args)%2 != 0 {
			return TvNil, false
		}
		fields := make(map[string]*PhoType, len(args)/2)
		for i := 0; i < len(args); i += 2 {
			name := args[i].Evaluate(ctx)
			ft, ok := coerceToType(ctx, args[i+1])
			if name.Kind != KindStr || !ok {
				return TvNil, false
			}
			fields[name.Val.(string)] = ft
		}
		return TvType(RecordType(fields)), true
	}
	return TvNil, false
}

func argAsType(ctx Context, n ttnode) (*PhoType, bool) {
	val := n.Evaluate(ctx)
	if val.Kind != KindType {
		return nil, false
	}
	return val.Val.(*PhoType), true
}

// coerceToType evaluates n and yields a type: a type value as-is, or a LITERAL
// (atom/number/string/bool/nil) as its singleton — so `Struct.{ X 5 }` and
// `Struct.{ Mode :fast }` work alongside `Struct.{ X Number }`.
func coerceToType(ctx Context, n ttnode) (*PhoType, bool) {
	v := n.Evaluate(ctx)
	switch v.Kind {
	case KindType:
		return v.Val.(*PhoType), true
	case KindAtom:
		return AtomSingleton(v.Val.(*Atom).Name()), true
	case KindNum:
		return NumSingleton(v.Val.(float64)), true
	case KindStr:
		return StrSingleton(v.Val.(string)), true
	case KindBool:
		return BoolSingleton(v.Val.(bool)), true
	case KindNil:
		return TypeNil, true
	}
	return nil, false
}

// TypeByName resolves a built-in type NAME to its pre-interned type, for the
// linter's annotation harvest (a `--@ (~type Number)` arrives as the string
// "Number"). ok=false for an unknown name — the caller treats that as Dynamic.
func TypeByName(name string) (*PhoType, bool) {
	switch name {
	case "Number":
		return TypeNumber, true
	case "String":
		return TypeString, true
	case "List":
		return TypeList, true
	case "Map":
		return TypeDict, true
	case "Boolean":
		return TypeBoolean, true
	case "Char":
		return TypeChar, true
	case "Atom":
		return TypeAtom, true
	case "Function":
		return TypeFunction, true
	case "Nil", "NilT":
		return TypeNil, true
	case "Type":
		return TypeType, true
	case "Unknown":
		return TypeUnknown, true
	case "None":
		return TypeNone, true
	case "Collection":
		return TypeCollection, true
	case "Dynamic":
		return TypeDynamic, true
	case "Struct":
		return TypeStruct, true
	}
	return nil, false
}
