package core

import (
	"fmt"
	"sort"
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
func internType(t *PhoType) *PhoType {
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
)

func init() {
	prim := func(b baseBits, name string) *PhoType {
		return internType(&PhoType{base: b, name: name})
	}
	TypeNumber = prim(bNum, "Number")
	TypeString = prim(bStr, "String")
	TypeList = prim(bList, "List")
	TypeDict = prim(bDict, "Dict")
	TypeBoolean = prim(bBool, "Boolean")
	TypeChar = prim(bChr, "Char")
	TypeAtom = prim(bAtom, "Atom")
	TypeFunction = prim(bFun, "Function")
	TypeNil = prim(bNil, "Nil")
	TypeType = prim(bType, "Type")
	TypeUnknown = internType(&PhoType{base: baseAll, allStr: true, name: "Unknown"})
	TypeNone = internType(&PhoType{name: "None"})
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
	if t.base == 0 && len(t.structs) == 0 && !t.allStr {
		return "None"
	}
	parts := make([]string, 0, 4)
	for _, bn := range baseNames {
		if t.base&bn.bit != 0 {
			parts = append(parts, bn.name)
		}
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

var baseNames = []struct {
	bit  baseBits
	name string
}{
	{bNum, "Number"}, {bList, "List"}, {bDict, "Dict"}, {bStr, "String"},
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

// Subtype reports whether every value of type a is also a value of type b
// (a ⊆ b). For the Stage A1 fragment (primitive bitsets + finite/cofinite
// struct sets) this is decidable directly: a's base bits must be a subset of
// b's, and a's struct members must all be in b. The full set-theoretic
// emptiness-based algorithm (with negation, atoms, and structured types)
// supersedes this in Stage C.
func Subtype(a, b *PhoType) bool {
	if a == b {
		return true
	}
	if a.base&^b.base != 0 {
		return false
	}
	return structsSubset(a, b)
}

// structsSubset reports whether a's struct component is contained in b's,
// honoring the cofinite (allStr) flag on each side.
func structsSubset(a, b *PhoType) bool {
	switch {
	case !a.allStr && !b.allStr:
		// finite ⊆ finite: every a member is a b member.
		for st := range a.structs {
			if _, ok := b.structs[st]; !ok {
				return false
			}
		}
		return true
	case !a.allStr && b.allStr:
		// finite ⊆ cofinite(all except E): no a member may be an exception.
		for st := range a.structs {
			if _, ok := b.structs[st]; ok {
				return false
			}
		}
		return true
	case a.allStr && !b.allStr:
		// cofinite ⊆ finite is impossible (infinitely many structs vs finite).
		return false
	default:
		// cofinite(all except Ea) ⊆ cofinite(all except Eb): Eb ⊆ Ea.
		for st := range b.structs {
			if _, ok := a.structs[st]; !ok {
				return false
			}
		}
		return true
	}
}

// Contains reports whether the value v inhabits type t (v ∈ ⟦t⟧) — the runtime
// membership test behind Is?. Equivalent to TvTypeOf(v) being a subtype of t.
func (t *PhoType) Contains(v Tval) bool {
	return Subtype(TvTypeOf(v), t)
}

// TvType wraps an interned *PhoType as a KindType value.
func TvType(t *PhoType) Tval {
	return Tval{t, KindType}
}
