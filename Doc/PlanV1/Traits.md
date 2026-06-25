# v1 Plan — Traits (structural, implicit interfaces)

Status: **IMPLEMENTED** (all six phases). Builds on the structural record work in
[GradualTyping.md](GradualTyping.md) (the `fields` component, `Struct.{…}`).
Surface (decided with the owner): `(Trait (extends-list) member…)` — anonymous,
extends-list mandatory (empty `()` allowed); bind with `(type Name (Trait …))`.
Static checking is now wired: `(Trait …)` resolves to a real trait type in the
checker (`resolveTraitNode`), and a value flowing into a trait-typed slot is
checked against the collected struct member surface (`structMissingTraitMembers`
/ `checkTraitArg`) — a struct missing a required member is flagged, a non-struct
never satisfies a method-trait, property requirements honor fields, and extends
folds in supertrait requirements. Unknown/non-struct-shape values stay gradual
(no false positives).

## 1. Concept

A **Trait** is a first-class *type* (a `KindType` value) describing a set of
REQUIRED members — methods and properties — that a value's type must provide.
Like a Go interface it is **structural and implicit**: a type satisfies a trait
by *having the members*, with no `implements` declaration anywhere. A trait may
**extend** other traits (inheriting their requirements) and may carry **default
implementations** for any member.

```pho
(Trait (ExtendedTrait1 ExtendedTrait2)          ; extends-list: may be (), never omitted
    (method Self.private (self arg))             ; required method — signature only
    (method Self.Public (self arg))
    (method Self.WithDefaultImpl (self) Nil)     ; required + DEFAULT implementation (body)

    (property Self.Readonly get)                 ; required getter
    (property Self.Mutable get set)              ; required getter + setter
    (property Self.PublicMutableWithDefault
        get (method Self (self) 0)               ; default getter
        set (method Self (self newValue) Nil)))  ; default setter
```

`Self` is the **receiver placeholder** — the implementing type. Lowercase
members are private, uppercase public (the existing capitalization convention).
The extends-list is mandatory even when empty: `(Trait ExtendsNothing () …)`.

A key affordance: a **property requirement can be satisfied by a FIELD** of the
same name. `(property Self.Readonly get)` is met by a struct with a field
`Readonly`; `(property Self.Mutable get set)` is met by a (mutable) field too.

## 2. Type-system semantics

- **Membership** — `(x.Is? SomeTrait)` ⇔ x's type provides every required member
  (a method by name + arity/signature; a property by a property *or* field of
  that name with compatible get/set). Members with a default are always
  satisfied (the default fills them).
- **Subtyping**
  - `T <: Trait` ⇔ T provides all of the trait's (flattened) requirements.
  - `TraitA <: TraitB` ⇔ TraitA's requirement set ⊇ TraitB's — a *bigger* trait
    is the *narrower* type (exactly like records: more requirements ⇒ smaller
    value set). Extends is therefore just requirement-set union.
- **Gradual**: where a member's signature can't be verified statically, the
  requirement degrades to "has a member of that name" (gradual-safe — never a
  false positive), consistent with arrow/record handling.

## 3. Representation (`core.PhoType`)

A new `trait *traitInfo` component, parallel to `arrow` and `fields`. Like a
record it carries `allStr: true` (it constrains struct instances; primitives can
satisfy it too via the universal/object-model member tables). The extends-list
is **flattened at construction** so membership/subtyping read one map.

```go
type traitInfo struct {
    Methods    map[string]traitMethod
    Properties map[string]traitProperty
}
type traitMethod struct {
    Arity   int          // params excluding self (runtime check)
    Params  []*PhoType   // param types (static check; nil ⇒ Unknown)
    Result  *PhoType
    Default tfun         // nil ⇒ no default implementation
}
type traitProperty struct {
    Get, Set   bool
    Type       *PhoType
    GetDefault tfun
    SetDefault tfun
}
```

## 4. Membership & subtyping

- `Contains(v, trait)` — for each required method, `ctx.ResolveMember(typeKey,
  name)` must find a method (runtime check is name + arity); for each property,
  ResolveMember must find a property OR (for a `KindInstance`) `inst.Fields[name]`
  must exist, with get/set compatibility. A defaulted member is always met.
  Membership needs a `Context` (to reach the per-package member tables), so it
  rides the same path as the `x.Is?` Go-native method, not the context-free
  `Contains` — see §7.
- `Subtype(T, trait)` / `Subtype(traitA, traitB)` — requirement coverage /
  requirement-superset. Method signatures use the harvested `methodsig` data
  (already collected by the linter); properties match on get/set + type.

## 5. The `(Trait …)` builtin

Parses: the extends-list (arg 0, a `(…)` of trait references — evaluated to trait
types and flattened), then `(method Self.name (self params…) [body])` and
`(property Self.name get [getImpl] [set [setImpl]])` forms. `Self` is recognised
as the receiver placeholder. A body present on a method/property becomes its
default. Produces a `KindType` whose `*PhoType` carries the `traitInfo`.

## 6. Default-implementation dispatch — DECIDED: (A) auto-inject

**Chosen: (A) auto-inject (Rust-like, implicit).** When `x.method` doesn't
resolve on x's own type, the dot accessor searches the registered traits whose
membership x satisfies and that define a default `method`/property, and calls
it. This makes defaults usable implicitly, matching the syntax's intent.

Mechanics:
- A process-wide **trait registry** records every constructed trait that has at
  least one default member (so the scan set is small).
- The KindInstance dot fallback in `dot.go` (the same seam that already falls
  back to universal `Unknown` members for `x.Is?`) gains a second fallback:
  if still unresolved, walk the registry, and for each trait the receiver
  satisfies (`trait.Contains(recv)`) that defines a default for `name`, dispatch
  it (pushing recv as self). First match wins; **ambiguity (two satisfied traits
  defaulting the same name) is an error**, mirroring the object model's no-
  precedence member rule.
- **Cache** keyed by (typeKey, name) → resolved default tfun / "none" / "clash",
  so repeated access is O(1); the registry only grows at trait-construction time.

Cost & risk: this is the largest piece and it edits the sibling-owned dot
dispatch — it lands last (phase 4), behind the cheaper, self-contained phases.

## 7. Implementation phases

1. **Core** — `traitInfo`, `TraitType`, the `trait` component; canonicalKey;
   render (`(Trait …)`); IsEmpty (non-empty). *(pkg/core/phtype.go)*
2. **Satisfaction** — a `Context`-carrying membership/subtype check (methods via
   ResolveMember, properties via ResolveMember-or-field), wired into the
   `x.Is?` Go-native method and the static checker. Signatures degrade to
   name+arity at runtime, full match statically.
3. **`(Trait …)` builtin** — parse + flatten extends + `Self` + default bodies.
   *(pkg/builtins)*
4. **Default dispatch** — per the §6 decision (A is a dot.go change + a trait
   registry + cache).
5. **Lint** — `(Trait …)` lints clean (Self, member sigs are declarations);
   resolve trait types in annotations/guards; satisfaction checking against a
   struct's collected methods/fields. *(pkg/lint)*
6. **Tests** — membership, subtyping, extends, property-by-field, defaults.

## 8. Hard parts / risks

- **Runtime signatures** — closures carry no witness, so runtime satisfaction is
  name + arity; the static checker does full signature matching from harvested
  `methodsig`s (the linter already gathers these). Gradual-safe.
- **Default dispatch (§6 A)** scans/caches and touches the sibling-owned dot
  dispatch — the riskiest, most invasive piece.
- **`Self`** resolution in signatures and default bodies.
- **Context dependence** — membership must reach per-package member tables, so a
  trait can't use the pure context-free `Contains`; it goes through the
  member-resolving path (like `x.Is?` already does).

## 9. Decisions (settled)

1. **Default-method dispatch** — (A) auto-inject. *(§6.)*
2. **Runtime satisfaction depth** — name + arity at runtime; full signature
   matching in the static checker (consistent with arrows). A closure carries no
   signature witness, so the runtime can only see "a member of this name with
   this arity exists"; the checker uses the harvested `methodsig`s for the rest.
3. **Private (lowercase) members** — required-but-private: a satisfying type MUST
   provide them, and the existing privacy rule governs who may call them.
