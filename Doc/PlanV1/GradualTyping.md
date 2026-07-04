# v1 Plan — Set-Theoretic Gradual Type System

Status: **proposed** · Owner: language core · Scope: `pkg/core`, `pkg/builtins`,
`pkg/lint`, `pkg/modload`, `cmd/pho-lsp`, `script/std`, `Doc/`

> This plan **layers on** [ObjectModel.md](ObjectModel.md). The object model owns
> `KindType`, `typeof`, dot-dispatch, the top type `Unknown`, and the built-in
> module; this plan adds the *set-theoretic algebra* (unions/intersections/
> negations + emptiness/subtyping), the *gradual layer* (`Dynamic`), and the
> *static checker* that consumes type annotations. The two are sequenced in
> [Coordination.md](Coordination.md) — read it before scheduling work.

## 1. Design pillars

- **One algebra, two consumers.** A single hash-consed Go descriptor —
  `core.PhoType`, the value behind `KindType` (see [Coordination.md](Coordination.md)
  §2 for the merge with ObjectModel's `TypeValue`) — *is* the set-theoretic type.
  The runtime uses it for `typeof`/`Is?`/`subtype?`; the static checker harvests
  the *same* values out of `--@` annotations. One `IsEmpty`/`Subtype`
  implementation; everything else is sugar.
- **Types are evaluated Pho expressions.** `Number`, `(Or A B)`, `(List T)`,
  `(Fun (A B) R)` are builtins that evaluate to `*PhoType` values. A type
  annotation is just an expression yielding a type value — no separate type-AST
  parser.
- **Transient / opt-in gradual typing.** Un-annotated code is `Dynamic`. The
  checker flags a constraint **only when both sides are `Dynamic`-free and
  provably incompatible**. Stripping every `--@` yields zero type diagnostics —
  the *gradual guarantee*, enforced by routing every diagnostic through one
  predicate.
- **Three distinct "wide" types.** `Unknown` = ⊤ (every value; the object model's
  universal-method receiver). `None` = ⊥ (no value). `Dynamic` = the gradual
  "unknown to the checker" type. They are not interchangeable — call it out in
  docs.

## 2. Type representation

The descriptor lives in `pkg/core/phtype.go` and is the value behind `KindType`.
Its *nominal-leaf* identity (primitive ids, struct ids, `unknown`) is ObjectModel's
`TypeID`; its *structural* components are the set-theoretic algebra. The unified
struct and the nominal `TypeRegistry` are specified in
[Coordination.md](Coordination.md) §2 — this plan owns the algebra fields:

```go
type PhoType struct {
    key   string   // canonical hash-cons key; for a single nominal type == its TypeID
    base  baseBits // bitset over the primitive kinds present (num/str/array/dict/bool/chr/atom/fun/nil)
    atoms atomSet   // finite/cofinite set of *core.Atom singletons (Phase F)
    strct *dnf      // DNF of nominal struct literals, keyed by TypeID (Phase F)
    list  *dnf      // DNF of (List T) element literals (Phase F)
    rec   *dnf      // DNF of dict/record literals (Phase F)
    arrow *dnf      // DNF of (Fun dom cod) literals (Phase F)
    dyn   bool      // the Dynamic flavor
}
```

- **Hash-consing** mirrors `pkg/core/atom.go` (`typePool`/`typeMu`/`internType`),
  canonicalizing (sort/dedup) **before** keying so `(Or A B)` and `(Or B A)`
  intern to one pointer. This gives O(1) equality, dict-keyability, and
  termination for recursive-emptiness memoization. **Mandatory.**
- A **single nominal type** interns under a key equal to its `TypeID`
  (`prim:num`, `reader#Reader`, `unknown`), so `KindType` equality (interned
  pointer) coincides with ObjectModel's "equal iff `ID`s match" — and
  `(typeof 1) == Number` is `True` for free.
- `Stringify` renders a type via `(*PhoType).Name()`: the registered display name
  for a single nominal type, else a structural rendering (`Number | Nil`,
  `[Number]`, `(Number -> Boolean)`). `pkg/builtins/helpers.go` gets `KindType` in
  `tvalEqual` (pointer identity) and `scalarKey` (interned pointer is a safe dict
  key).

## 3. Runtime surface (algebra additions)

`typeof`, `Is?`, `In?`, and the dot-dispatch that makes `(x.Is? T)` work are
**owned by ObjectModel** (its built-in module + dispatch). This plan adds the
algebra builtins in `pkg/builtins/typeval.go` (merged in `register.go`). All
install as capitalized builtins in `Env.Globals` (never re-exported, never
shadowable — safe per ObjectModel's analysis).

| Builtin | Kind | Meaning |
|---|---|---|
| `None` `Dynamic` `Type` `Collection` | constant `KindType` | ⊥ / gradual / type-of-types / `(Or List Dict String)` |
| `(subtype? S T)` | fun | → `Boolean`; `S ∧ ¬T ≡ ∅` |
| `(Or …) (And …) (Not A) (Diff A B)` | fun | connectives over `KindType` args (else `ErrType`) |
| `(List T) (Dict K V) (Fun (A B) R) (Tuple …)` | fun | parametric constructors → `KindType` (Phase F) |

(The base nominal names — `Number String List Dict Boolean Char Atom Function
Nil Unknown` — are bound by ObjectModel's built-in type bindings.)

**Membership = `subtype?(typeof x, T)`.** ObjectModel's `Is?` ships as
`(method Unknown.Is? (self type) (== (typeof self) type))`; when this plan's
`subtype?` lands, **upgrade it to** `(method Unknown.Is? (self type)
(subtype? (typeof self) type))` so `(1.Is? (Or Number String))` is `True`
(`==` only handles exact-nominal tests). See [Coordination.md](Coordination.md)
§4.

**Arrow types are `(Fun (A B) R)`, never `(-> …)`** — `->` lexes as two tokens
(`-` then `>`), so it cannot head a call. The readable `-> ` survives only as
macro-split sugar *inside* `sig!` (§4).

## 4. Annotation surface & the harvest seam

Type annotations are the existing `--@` macros, upgraded to carry first-class
type *values* instead of strings:

```pho
--@ (sig! Number Number -> Number)        ; function signature
--@ (methodsig! Unknown Type -> Boolean)  ; method sig — first type is the receiver
--@ (type! (Or Number Nil))               ; a var/binding type
--@ (sig! (List Number) -> Number)        ; parametric param
```

Today `script/std/annot/annot.phl`'s `sig`/`type` funs attach **strings** via
`(meta/Attach key value)` (the `!` form quotes args, the `->` arrives as the two
tokens `-` `>`). The one change that realizes "annotations are evaluated type
expressions": **make `sig`/`type` evaluate each type-expression argument** to a
`*PhoType` value and attach the value. Keep `sig`'s hand-split on the `-` `>`
pair; on each side, a bare name (`Number`) resolves to the bound type value and a
nested form (`(Or A B)`/`(List T)`) is `resume`-evaluated. Annotation eval
already has the builtins env in scope, so resolution is just evaluation — **no
type-name-string parser anywhere.**

> **Two signature annotations.** `sig!` is for functions — every argument is a
> regular param. `methodsig!` is for methods and **assumes the first type is the
> receiver**: `(methodsig! Recv P1 -> R)` → `receiver=Recv, params=[P1],
> result=R`. The checker cross-checks a method's `methodsig!` receiver against the
> type named in `(method Recv.Name …)`. Both are stdlib funs in
> `script/std/annot/annot.phl` (`methodsig` is added alongside the existing
> `sig`). See [Coordination.md](Coordination.md) §4.

**Harvest into the linter** (pipeline: `--@` → `PBranch.Annotations` →
`pkg/annot` isolated/memoized evaluator → `walkAnnotations` in
`pkg/lint/walker.go`):

- `pkg/lint/scope.go` `Definition`: add `Type *core.PhoType`; for
  `DefFun`/`DefMethod` a `Sig *FunSig{Params []*core.PhoType; Result *core.PhoType}`.
  Un-annotated positions = `Dynamic`. **This `Sig` is the same record ObjectModel's
  per-package method table carries** (Coordination §3) — member resolution and
  type-checking read one signature.
- `walkAnnotations`: read `params`/`result`/`type` entries (their `Value` is
  already `KindType`) and stamp `Definition.Type`/`Sig`; add a lexical-order
  pre-pass (mirror `assignDeclShapes`). Keep all harvesting single-threaded
  (`annot` eval is process-global).
- `cmd/pho-lsp` hover renders `Definition.Type`/`Sig` via `(*PhoType).Name()`.

## 5. The gradual static checker

**Shape and Type coexist (layered).** Keep `Shape` (`infer.go`) — a cheap,
false-positive-free mirror of runtime dot-dispatch feeding member/completion/nav.
Add `inferType(scope, flow, n) *core.PhoType` alongside `inferShape`; no
annotation ⇒ `Dynamic` (the `ShapeUnknown` analog), deriving base types from
literals (`5 → Number`).

**The one gate — the gradual guarantee made mechanical:**

```go
func ProvableMismatch(actual, expected *core.PhoType) bool {
    if actual.IsGradual() || expected.IsGradual() { return false } // Dynamic anywhere ⇒ silent
    return !core.Subtype(actual, expected)
}
```

**Every** type diagnostic routes through this. A property test (strip all `--@` ⇒
zero `type-mismatch`) is the tripwire and runs in CI.

**Constraint sites** (`pkg/lint/walker.go`): call args (`checkBranch` default arm
— arity respecting spread/optional, each arg vs the callee's `Sig` param);
`return`/tail vs annotated result; annotated `(= x v)` (`checkAssign`, reusing its
cross-frame soundness gate); **member access on a statically-typed receiver**
(builds on ObjectModel's import-scoped member resolution — the resolved member's
`Sig` types the call; a `Dynamic` receiver accepts unconditionally).

**Occurrence typing / narrowing** (`pkg/lint/ifform.go` consumers): recognize a
type-test guard over a bound name and install a narrowed flow-env per arm:

- `(x.Is? T)` / `(== (typeof x) T)`: then-arm `x : typeof(x) ∧ T`; else-arm
  `x : typeof(x) ∧ ¬T` (so `(Number|String) ∧ ¬Number = String`).
- `Dynamic` degrades correctly: then-arm pins to `T` (now checkable); else-arm
  stays `Dynamic` (no false positives).
- `unless`/negation swaps arms; predicate builtins are guards too. Reuse the
  `inBranch` save/restore; narrow only the tested var, only on the canonical
  unshadowed `Is?`/`typeof`, only in the linear owning scope.

**Diagnostics:** reuse `diag.ErrType = "type-mismatch"`, GCC-style, single-quoting
names/types (`argument 1 has type 'String', but 'add' expects 'Number'`). Finer
codes go in `pkg/diag/diag.go`, never inlined.

## 6. Phased rollout (this plan's phases)

These slot into the cross-plan sequence in [Coordination.md](Coordination.md) §5.

- **GT-1 · Algebra over base kinds.** `Or/And/Not/Diff`, `subtype?`/`IsEmpty`
  over `base`+`atoms`; `None`, `Collection`, `Dynamic`. Upgrade `Is?` to
  `subtype?`. *Files:* `core/phtype.go`, `builtins/typeval.go`,
  `script/std`/builtin `universal.phl`. *DONE:* De Morgan / `t∧¬t=None` property
  tests; `(Or Number String)∧¬Number==String`; `(1.Is? (Or Number String))`.
- **GT-2 · Annotation harvest + first static checks.** `sig!`/`methodsig!`/`type!`
  attach type values; flag provably-wrong call args & annotated assigns; gradual
  guarantee live. *Files:* `script/std/annot/annot.phl`, `lint/{scope,walker,
  infer}.go`, `+ProvableMismatch`. *DONE:* `(add "x" 1)` flags only when
  annotated; strip-`--@` ⇒ 0 diagnostics.
- **GT-3 · Return checks + occurrence typing.** *Files:* `lint/{walker,
  ifform}.go`, `narrow()`. *DONE:* union & `Dynamic` narrowing; `unless` swap; no
  false positives.
- **GT-4 · Structured types.** `(List T)`, `(Dict K V)`, records, nominal
  structs (keyed by `TypeID`); component-wise emptiness (Φ). *Files:*
  `core/phtype.go`, `builtins/typeval.go`, `lint/member.go`. *DONE:* open/closed
  record subtyping; struct nominal distinctness vs a brute-force oracle.
- **GT-5 · Function arrows.** `(Fun (A B) R)`; arrow emptiness via the
  function-subtyping decomposition (memoized). *DONE:* contravariant dom /
  covariant cod emerge; memo caps runtime.
- **GT-6 · Singletons/cofinite + recursive types.** Specific atoms/ints/bools as
  types; recursive types via coinductive emptiness. *DONE:* `rec L=Nil|(Number,L)`
  decided; `(typeof :ok)` can refine to the `:ok` singleton.

## 7. Risks & non-goals

- **Φ (Phase GT-4) and arrow decomposition (GT-5) are the bug nest.** Port
  verbatim from CDuce/Elixir `descr.ex`; back with randomized algebra-law tests +
  a brute-force finite oracle. Ship them late and isolated.
- **Interning is mandatory** — without canonicalization, structurally-equal types
  are distinct dict keys and unequal under the pointer path.
- **Exponential blow-up** from DNF expansion — pointer-keyed memoized `IsEmpty`
  required.
- **Transient (lint-only) gradual typing — no runtime casts, no soundness.**
  Annotations are static claims; a `Dynamic` value entering a typed context is not
  runtime-checked.
- **Runtime arrow membership is undecidable.** A closure carries no domain/codomain
  witness, so `typeof f` is always the opaque `Function` and
  `(Is? f (Fun (Number) Number))` degrades to "is it a Function". Precise arrow
  types exist only in the static layer.
- **No full inference** (no Hindley-Milner). Flow-light: declared + literal types,
  narrowing, provable mismatch. Un-annotated stays `Dynamic`.
- **Prefix connectives only** (`Or`/`Not`/`(Fun …)`); operator sugar (`|`, `~`)
  deferred because multi-char operators don't lex atomically.
