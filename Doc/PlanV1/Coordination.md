# v1 Plan — Coordination: Object Model × Gradual Type System

Status: **proposed** · This document reconciles
[ObjectModel.md](ObjectModel.md) (the **substrate**: universal object model,
import-scoped methods, `KindType`, `typeof`, dispatch, `Unknown`) with
[GradualTyping.md](GradualTyping.md) (the **layer**: set-theoretic algebra,
`Dynamic`, the gradual static checker). It exists so the two can be executed as a
single track with no rework.

**Verdict up front:** the plans are compatible. ObjectModel was written for this
merge (its §13). There is **one** structural decision to lock (how `KindType`'s
value is represented — §2 below) and a short list of naming/ownership alignments
(§3–§4). No hard incompatibilities. Sequencing is in §5.

## 1. Contact points

| Shared surface | ObjectModel | GradualTyping | Resolution |
|---|---|---|---|
| `KindType` value repr | `TypeValue{ID,Name,OriginPath,Struct,Constructor}` (nominal) | `*PhoType` (set-theoretic descriptor) | **Merge into one `*PhoType` + a `TypeRegistry`** (§2) |
| `typeof` | owns it (Go builtin, Phase 3) | consumes it | **ObjectModel owns**; GT never re-specs it |
| `(x.Is? T)` dispatch | universal import-scoped dot dispatch | (earlier proposed a dot.go hook) | **Drop GT's hook; ride ObjectModel's dispatch** |
| `Is?`/`In?` impl | Pho methods on `Unknown` using `==` | needs `subtype?` semantics | **Ship with `==`, upgrade to `subtype?`** (§4) |
| Top type | `Unknown` (`unknown`) | `Unknown` = ⊤ | **Agree.** GT adds `None`=⊥, `Dynamic`=gradual |
| Signature annotations | `methodsig!` (§4.5) | `sig!` | **Keep both: `sig!` for funs, `methodsig!` for methods (first arg = receiver)** (§4) |
| List type name | `List` (`prim:array`) | `List` | **`List`** (owner's call) — internal kind stays `KindList` |
| Method/extension record | `MethodEntry{Fun,Privileged,Exported,NameSpan}` | `Sig *FunSig` on `Definition` | **One record carries both** (§3) |
| `KindConstructor` | removed → `KindType` | referenced in a few places | **GT aligns to post-removal world** |
| Struct identity | `TypeID = "<pkg>#Name"` | keyed by `*tstruct` ptr | **Key by `TypeID`**; carry `*tstruct` as payload |
| Member resolution (lint) | import-scoped, per-`TypeID` surface | member-type-checking | **GT builds on ObjectModel's resolution** |
| `SemTokType` / tree-sitter | type names → `type`, members → `method` | `SemTokType` for type names | **One tooling pass; no grammar regen** |

## 2. The central merge — one `KindType`, a descriptor + a registry

This is the only decision that, if gotten wrong, forces rework. Lock it first.

**`KindType.Val` is a `*core.PhoType` (GradualTyping's descriptor).** ObjectModel's
`TypeValue` *splits* into two complementary parts joined by `TypeID`:

```go
// pkg/core/phtype.go — the set-theoretic descriptor (GradualTyping owns the algebra).
// A SINGLE nominal type interns under key == its TypeID, so KindType equality
// (interned-pointer) coincides with ObjectModel's "equal iff IDs match".
type PhoType struct { key string; base baseBits; atoms atomSet
                      strct, list, rec, arrow *dnf; dyn bool }

// pkg/core — the nominal metadata ObjectModel needs for construction/dispatch/origin,
// keyed by the stable TypeID. Replaces TypeValue's Name/OriginPath/Struct/Constructor.
type NominalInfo struct { Name, OriginPath string; Struct *tstruct; Constructor tfun }
var TypeRegistry map[TypeID]*NominalInfo   // primitives + unknown pre-seeded; (struct …) registers
```

Why this shape (and not ObjectModel's flat `TypeValue`): a set-theoretic type can be
a *union/negation*, which has no single `ID`, `Struct`, or `Constructor`. Keeping
those on the descriptor would be ill-defined for composites. Putting nominal
metadata in a `TypeID`-keyed registry keeps the descriptor pure and lets `typeof`,
construction, dispatch, and the origin rule all look up by the same stable id the
algebra already uses as its atom.

Consequences for each plan (small, mechanical):

- **ObjectModel Phase 0** builds the descriptor (single-nominal form) **+** the
  `TypeRegistry`, instead of a flat `TypeValue`. `(struct Reader …)` interns the
  single-struct descriptor (key `reader#Reader`) and registers its `NominalInfo`.
  `Reader.{…}` construction and `eval.go`'s call path look up
  `TypeRegistry[id].Constructor`. Method/property tables stay keyed by `TypeID`
  (unchanged from ObjectModel §4.2).
- **`==` on `KindType`** is interned-pointer equality (works for nominal *and*
  composite), superseding ObjectModel's "compare `ID`s" — same result for nominal
  types, and correct for `(Or …)`. (ObjectModel §4.1 "extend `==`" → "extend `==`
  to intern-pointer compare".)
- **GradualTyping** keys its `strct` DNF component by `TypeID` (not raw pointer),
  matching ObjectModel's tables.

Net: ObjectModel can build and ship Phases 0–3 with only the descriptor's
`base`/registry populated; GradualTyping later fills `atoms`/`strct`/`list`/`rec`/
`arrow`/`dyn` with **zero changes** to ObjectModel's code.

## 3. One signature record for member resolution *and* type-checking

ObjectModel's per-package method table (`MethodEntry`) and lint extension surface
(§9.1) record `{Fun/span, Privileged, Exported}` per `(TypeID, member)`.
GradualTyping needs the harvested `sig!` type on the same member. **Add `Sig
*FunSig` to `MethodEntry`** (and to the lint extension-surface record). The method
declaration harvests its `--@ (sig! …)` into `Sig`; ObjectModel's member
resolution returns the member, and GradualTyping's checker reads `member.Sig` to
type the call. `Definition.Sig` (GradualTyping §4) and `MethodEntry.Sig` are the
same `FunSig` type. No second harvest path.

## 4. Naming & semantics alignments (locked recommendations)

1. **Two signature annotations: `sig!` (functions) and `methodsig!` (methods).**
   `(sig! P1 P2 -> R)` → `params=[P1,P2], result=R`. `(methodsig! Recv P1 -> R)`
   **assumes the first type is the receiver** → `receiver=Recv, params=[P1],
   result=R`. The checker cross-checks a method's `methodsig!` receiver against the
   type named in `(method Recv.Name …)`. *Both* are new stdlib funs in
   `script/std/annot/annot.phl` (today only `sig` exists; `methodsig` is added).
   ObjectModel §4.5 already uses `methodsig!` — no delta there.
2. **`List` is the list type name** (display name `List`, bound builtin `List`).
   The internal kind stays `KindList`; the `TypeID` is `prim:list`. *Delta:*
   GradualTyping and ObjectModel both render the list type as `List`.
3. **`Is?`/`In?` ship on `==`, then upgrade to `subtype?`.** ObjectModel's
   built-in `universal.phl` ships `(method Unknown.Is? (self type)
   (== (typeof self) type))` (correct for exact-nominal tests). When GradualTyping
   lands `subtype?` (Phase GT-1), change it to `(subtype? (typeof self) type)` so
   union/composite membership works. *Delta:* a one-line edit to `universal.phl`,
   owned by GT-1.
4. **`typeof` is nominal in v1.** It returns `Number`/`Atom`/… (ObjectModel), not
   singletons. GradualTyping's singleton types (`:ok` as its own type) are a late,
   **additive** refinement (Phase GT-6): they make `typeof` return a finer
   descriptor while `Atom` stays the supertype, so nothing breaks. No conflict —
   just precision deferred.
5. **`Unknown` ≠ `Dynamic`.** `Unknown` = ⊤ (object model's universal receiver);
   `Dynamic` = the checker's gradual type (GradualTyping only). Documented hazard.

## 5. Unified roadmap (the single execution track)

Each stage compiles + tests green before the next. Ownership in brackets; the
underlying plans' phase ids in parentheses.

- **Stage A — Shared foundation** *[co-owned]* (OM Phase 0 ∪ GT repr). The `PhoType`
  descriptor (single-nominal form) + `TypeRegistry` + `KindType` + `TypeID` +
  `typeof` + intern-pointer `==`; remove `KindConstructor` (struct → `KindType`);
  update `eval.go`/`dot.go`/`decl.go` construct sites. **Lock §2 here.**
- **Stage B — Object model** *[OM]* (OM Phases 1–3). Per-package method/property
  tables; import-scoped dot dispatch; non-privileged rule; the auto-loaded built-in
  module (`Size`/`Keys`, `Is?`/`In?` on `==`). After B: `x.Is?`, `.Size`, literals
  as objects work.
- **Stage C — Set-theoretic algebra** *[GT]* (GT-1). `Or/And/Not/Diff`,
  `subtype?`/`IsEmpty` over nominal leaves; `None`, `Collection`, `Dynamic`.
  Upgrade `Is?`/`In?` to `subtype?` (§4.3). *C depends on A only — it can overlap B*
  (different files: `phtype.go`/`typeval.go` vs `dot.go`/`bind.go`/`builtins/pho`),
  except the one-line `universal.phl` upgrade lands after B.
- **Stage D — Cleanup + lint member resolution** *[OM]* (OM Phases 4–5). Delete
  `len`/`keyof` + migrate; import-scoped member resolution; clash/origin/privacy
  diagnostics; per-package extension surface **carrying `Sig`** (§3); drift test.
- **Stage E — Annotations + gradual checker** *[GT]* (GT-2, GT-3).
  `sig!`/`methodsig!`/`type!` attach `*PhoType`; `Definition.Type`/`Sig`;
  `ProvableMismatch`; call/return/assign
  checks; occurrence-typing/narrowing. *Member-type-checking builds on D's
  resolution; the rest needs only C.*
- **Stage F — Deepen the type theory** *[GT]* (GT-4, GT-5, GT-6). Structured types
  (`List(T)`/`Dict`/records/nominal structs, keyed by `TypeID`) → function arrows
  (`Fun`) → singletons/cofinite/recursive. The two hard, bug-prone pieces (Φ, arrow
  emptiness) are here, isolated, behind oracle property tests.
- **Stage G — Docs & tooling finalize** *[co-owned]* (OM Phase 6 ∪ GT tooling).
  `SemTokType`/`method` semantic tokens; tree-sitter verify (qualified receivers +
  type names — no grammar regen); `Doc/Language.md` + `Doc/Features.md` `---DONE---`.

Dependency spine: **A → B → C → D → E → F → G**, with **B ∥ C** after A. Stages
A–E deliver a usable, gradually-typed object model; F–G deepen and polish.

## 6. Required edits to make the docs consistent

Small, surgical — applied when each owning stage runs (none block planning):

- **ObjectModel.md** — §4.1: `TypeValue` → `PhoType` descriptor + `TypeRegistry`
  (§2 here); `==` via interned pointer. §4.5: `methodsig!` stays (note `Is?`/`In?`
  upgrade to `subtype?` at Stage C). §4.2/§9.1: `MethodEntry` and the lint surface
  gain `Sig` (§3 here). Rename the list type `List` → `List`.
- **GradualTyping.md** — reconciled (`List`, `typeof`/dispatch owned by
  ObjectModel, no dot.go hook, `sig!`+`methodsig!`, `TypeID`-keyed structs,
  descriptor+registry).
- **Features.md** — when Stage G marks items DONE, the `Is?`/`In?` examples use
  `sig!` (already do) and the object-model + type-system bullets get the
  editor-support sentence.

## 7. Resolved by the owner

1. **Two signature annotations** — `sig!` (functions) and `methodsig!` (methods,
   first arg = receiver). Both are stdlib funs; `methodsig` is newly added.
2. **List type name is `List`** (internal kind `KindList`, `TypeID` `prim:list`).

Everything else (the `PhoType`+registry merge, `subtype?` upgrade, shared `Sig`
record, the unified roadmap) follows necessarily from the two plans.
