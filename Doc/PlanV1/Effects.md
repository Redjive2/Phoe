# v1 Plan — Effect Tracking (`!`-functions and `(var self)`)

Status: **proposed** · Builds on [TypeSignatures.md](TypeSignatures.md) (inline
sigs), [ObjectModel.md](ObjectModel.md) (methods/`self`/dispatch), and
[Coordination.md](Coordination.md) (`*PhoType`, the gradual checker). Supersedes
the standalone `~pure` annotation by inferring purity instead of declaring it.

## 0. The rule, in one paragraph

> **UPDATE (2026-06): the effect suffix is SPLIT into two.** Self/value mutation
> and environmental effects are now spelled differently:
> - **`=`** — SELF/value mutation: the callable mutates a value it was given, a
>   `(var self)` receiver or a `(var arg)` parameter (`append=`).
> - **`!`** — ENVIRONMENTAL effect: io, randomness, or a module-global write
>   (`print-line!`, `random/float!`, `chdir!`).
>
> A callable may need both, spelled in the fixed order **`?!=`** (predicate,
> then bang, then equals; any subset keeps that order — `?=`, `!=`, `?!=`). The
> **key rule for propagation**: a `=`-method call `x.m=` is classified by its
> RECEIVER, exactly like an assignment `(= x …)` — mutating a **local** is
> *contained* (the caller stays pure), mutating `self` propagates a `=`,
> mutating a module var propagates a `!`. So `(result.append= x)` on a local
> `result` leaks no effect, but `(self.data.append= x)` makes the enclosing
> method `=`. The diagnostics are `missing-equals`/`spurious-equals` (the `=`
> half) alongside `missing-bang`/`spurious-bang` (the `!` half). The rest of
> this doc predates the split: read every "must end in `!`" for a *self*-mutator
> as "must end in `=`".

A function or method is **effectful** iff its inferred effect set is non-empty.
**An effectful name MUST end in `!`; a pure name MUST NOT.** Effects propagate by
calling: a function that calls an effectful function is itself effectful. The
first dedicated effect is **self-mutation**, written by declaring the receiver
`(var self)` (mutable) instead of `(self)` (read-only); a method that mutates
`self` must declare `(var self)` and therefore ends in `!`. The linter is the
enforcer; the runtime is (almost) unchanged.

```pho
-- signature: receiver type (var Self) is a MUTABLE List; rest-arg : Unknown; returns none
(method List.Append! ((var Self) (spread Unknown)) None)

-- implementation
(method List.Append! ((var self) (spread elements)) do
    (self.grow_by! elements.size)              -- calls an effectful method  → effect
    (foreach i in elements.keys do
        (= self.[(+ self.size i)] elements.[i]) -- mutates self               → mutates-self effect
    )
)
```

`Append!` is effectful because (a) it mutates `self` and (b) it calls `grow_by!`.
The `!` is mandatory and the receiver must be `(var self)`. A `(self)` receiver
here would be an error ("cannot assign through a read-only receiver"), and a
missing `!` would be an error ("effectful function must end in `!`").

## 1. Semantics

### 1.1 Effect kinds

> **UPDATE (2026-07-03): freeform per-function effects, `!` trusted, aggregated
> on the signature.** The environmental model is now purely name-driven:
> - **Freeform, no primitive table.** An environmental effect is ONLY a call to a
>   `!`-function, recorded by *that function's name* (`print-line!`). There is no
>   Go-side classification of primitives (`effectfulPrimitives` deleted) — io,
>   randomness, etc. enter solely through the stdlib's `!`-named wrappers, which
>   are trusted. The tracked set is `!`-function names + `mutates-self/arg/free`.
> - **`!` is effectful by declaration.** A `!`-named function is trusted as
>   effectful; the checker never second-guesses it, so **`spurious-bang` is
>   removed**. Only *missing* marks are flagged (`missing-bang`/`missing-equals`).
>   (`spurious-equals` stays — self-mutation is structurally observable.)
> - **Per-callable, reported on the sig.** A callable's effect set is the UNION
>   over ALL its `let` clauses, checked ONCE and anchored on its SIGNATURE — not
>   per clause. So a callable with one pure clause and one effectful clause is
>   consistent, and every diagnostic lands where the name+`!` are declared. Impl:
>   `checkClauseSetEffects` (driven from `checkClauses`), replacing per-clause
>   `checkCallableEffects`. The LSP hover (`callableEffectLabel`) likewise unions
>   over all clauses (so hovering `print-vertical!` shows the `print-line!` it
>   calls, from any position including the sig).

The coarse `!` marker means "has ≥1 effect". Underneath, the checker tracks a
named **effect set** so it can give precise messages and (later) granular sigs:

| Effect | Introduced by | Notes |
|---|---|---|
| `mutates-self` | `(= self.x v)` / `(= self.[i] v)` inside a `(var self)` method | the marquee effect; dedicated `(var self)` syntax |
| `mutates-arg` | `(= p.x v)` / `(= p v)` where `p` is a `(var p)` parameter | caller-visible mutation of an argument |
| `io` | calling a Go-side IO binding (print/read/file/proc) | from the `goimport` "dep" layer |
| `random` | calling a nondeterminism binding | |
| `mutates-free` | `(= x v)` where `x` is a captured outer/module `var` | closure/global mutation |

Start with `mutates-self` (v1, the example). Add the rest in later phases behind
the same machinery — they are all just more ways `effectsOf(node)` returns a
non-empty set. The **name only ever shows `!`** (binary); kinds live in the
inferred set and in signatures, not in the spelling. (Granular spelling, e.g.
`f!{io}`, is an explicitly deferred option — §8.)

### 1.2 Propagation (the call graph)

`effects(f)` = (primitive effects in `f`'s body) ∪ ⋃ `effects(g)` for every `g`
`f` calls. Computed as a least-fixed-point over the per-module call graph so
mutual recursion converges (a function is assumed pure while computing its own
fixpoint, then re-checked). A call to an **unknown/Dynamic** callee (an unresolved
name, a value of unknown type) is treated as **`!` iff the spelled name ends in
`!`** — i.e. the `!` convention is load-bearing precisely so effects survive
indirection without whole-program analysis. Higher-order effect polymorphism (a
`map`-like fn that is effectful only if its callback is) is **deferred** (§8); v1
treats a call through a parameter conservatively by the parameter's declared
arrow type's `!`.

### 1.3 `(var self)` and `(var p)`

- A **method receiver** is `(self)` (read-only) or `(var self)` (mutable). Only a
  `(var self)` method may assign through `self` (`(= self.x v)`, `(= self.[i] v)`,
  or call a `(var self)` method on `self`). This is purely a **static** contract:
  at runtime instances are already mutable pointers, so the bytecode is identical;
  `(var self)` is the promise that lets the checker reason and forces the `!`.
- A **value parameter** may be `(var p)` (mutable, caller-visible mutation) — the
  general case of `(var self)`. v1 may ship `(var self)` only and defer `(var p)`.
- In a **signature**, the receiver/param type is written `(var T)`: `(var Self)`,
  `(var List)`, etc. `(var T)` and `T` are the SAME type; `var` is an effect/
  mutability qualifier on the parameter slot, not a different type. The checker
  records it as "this slot is mutated".

### 1.4 What the linter enforces (the diagnostics)

| Code | Severity | When |
|---|---|---|
| `missing-bang` | error | inherits an environmental effect (a `!`-call / module write) but name lacks `!` |
| `missing-equals` | error | mutates self / a `(var arg)` but name lacks `=` |
| `effect-through-readonly` | error | `(= self.x v)`/effectful self-call when the SIG's receiver isn't `(var Self)` |
| `spurious-equals` | warning | name ends in `=` but mutates nothing it was given |
| `effect-in-pure-context` | error | an effect in a context required pure (a `property` getter; later annotation/`~pure` bodies) |

There is **no `spurious-bang`**: a `!` name is effectful *by declaration* and is
never second-guessed (a deliberately-future-proofed `init!` is fine). Only
*missing* environmental marks are flagged.

**Status (2026-07-03, freeform model):** the checker is **live** in `cmd/pho-lint`
and `cmd/pho-lsp` (they set `EffectCheck = true`; the package default stays
`false` so Go test snippets opt in explicitly). The effect set is a set of NAMED
effects (`effectSet map[string]bool`) that is **freeform**: an environmental
effect is a called `!`-function recorded by its own name — there is NO primitive
table (deleted); primitives enter only through `!`-named stdlib wrappers, which
are trusted. Structural effects `mutates-self`/`mutates-arg`/`mutates-free` are
still inferred from `(= …)`. The set is the UNION over a callable's clauses,
checked ONCE against its SIGNATURE by `checkClauseSetEffects` (from
`checkClauses`); the LSP hover (`callableEffectLabel`) aggregates the same way.
It emits `missing-bang`, `missing-equals`, `effect-through-readonly`,
`spurious-equals`, and `effect-in-pure-context` — all anchored on the sig.
`spurious-bang` and `bang-sig-mismatch` were removed. The remaining follow-on is
`~pure`-fn / annotation-body pure-context enforcement (waits on `~pure` being
harvested into lint, currently inert).

## 2. Layered implementation

### 2.1 Lexer + grammar (`!` becomes an identifier tail)

- **Go lexer** (`pkg/syntax/positioned.go`, identifier branch ~L296 + the private
  `#`-ident branch + `isIdentCont`): allow a single optional trailing `!`
  *alternatively to* `?` (a name has at most one of `? !` at the end). The runtime
  lexer path is the same `LexPos`, so this is one edit. Add `core.IsEffectName(s)`
  (`strings.HasSuffix(s, "!")`) next to the predicate `?` helper.
- **tree-sitter** (`tooling/tree-sitter-pho/grammar.js:205`): identifier regex
  `/#?[A-Za-z][A-Za-z0-9_]*[?!]?/`. Regenerate parser at **ABI 14** (`make
  check-abi`), corpus test, commit, re-pin `extension.toml`.
- **`(var name)` param**: no grammar change — it already parses as a nested
  `(list (identifier) (identifier))` inside the param list, exactly like
  `(optional x)` / `(spread x)`.

### 2.2 Runtime (`pkg/builtins/decl.go`)

- `parseArgList`: recognize `(var name)` as a fourth pattern beside `(optional …)`
  / `(spread …)`. Encode the mutability in the `[]string` slot (e.g. prefix
  `"&name"`, mirroring the existing `?`/`#` prefixes) so `BindFun`/`BindMethod`
  can bind a mutable param. For the receiver, `(var self)` and `(self)` bind
  `self` identically (the instance pointer); the `var` flag is recorded for the
  linter via the harvested signature, not the runtime.
- **No runtime enforcement of effects** in v1 (keep the runtime a thin substrate).
  Optional later: a debug mode that traps mutation through a non-`var` receiver.

### 2.3 Effect descriptor on the type (`pkg/core/phtype.go`)

- Extend `arrowType` with `Effects effectSet` and a per-slot `Mut []bool`
  (which parameter slots are `(var …)`). `ArrowType(params, result, effects, mut)`.
  `effectSet` is a tiny bitset (`uint8`). Pure = `0`. Two arrows are equal only if
  effects+mut match (so `(A) -> B` ≠ `!(A) -> B`), preserving interning.
- Subtyping/assignability: a pure arrow is assignable where an effectful one is
  expected (pure ⊑ effectful) but **not** vice-versa — assigning a `!` callback
  where a pure one is required is a `bang-sig-mismatch`. `(var T)` slot is
  contravariant the usual way; mutability must match exactly for now.

### 2.4 The effect checker (`pkg/lint` — the core work)

A new pass, `pkg/lint/effects.go`, run after the existing collect/shape passes:

1. **Harvest** per-callable: declared name (→ `!` or not), receiver mutability
   (`(var self)`?), param mutability, and any inline sig effects. Reuse the
   existing fun/method walk and `structInfo`/signature plumbing
   (`methodsig.go`, `harvestMethodSigs`).
2. **Primitive effects** of a body node — `effectsOf(scope, node)`:
   - `(= self.x v)` / `(= self.[i] v)` → `mutates-self` (and `effect-through-
     readonly` if the enclosing method isn't `(var self)`).
   - `(= p…)` where `p` is a `(var p)` param → `mutates-arg`; where `p` is a
     captured outer/module `var` → `mutates-free`.
   - a call whose resolved callee is effectful, OR an unresolved/Dynamic call
     whose spelled head ends in `!` → that callee's effects (or just `{unknown}`).
   - a member call `recv.M!` → `M`'s effects (resolved via the type-member surface).
3. **Fixpoint** over the module call graph (SCC + worklist) to get `effects(f)`.
4. **Check** each callable against §1.4. Emit diagnostics; record `effects(f)` on
   the callable's `Definition`/arrow so hovers, completion, and cross-file checks
   can read it. Cross-module: effects ride the exported signature surface
   (`PackageExports`/`structsFor`), same as types.
5. **Pure contexts**: `property` getters/setters, annotation-macro bodies, and any
   `--@ (~pure)`-marked fn must have empty effects → `effect-in-pure-context`.

### 2.5 Builtin / stdlib effect declarations

- The effect **primitives** are the Go-side bindings (`dep.Os*`, `dep.Io*`,
  random, etc.) reached through `goimport`. Add an **effect table** keyed by
  binding name (or annotate the binding) classifying each as `io`/`random`/pure.
  The checker consults it when a `goimport` member is called. Keep it small and
  data-driven (one map in `pkg/builtins` exported to the linter, drift-tested like
  `builtinNames`).
- **Stdlib `!` migration** (a codemod, like `ifmigrate`): every existing function/
  method that the new checker flags `missing-bang` gets renamed `name → name!` at
  its declaration AND every call site (across the stdlib + tests + docs). Mutating
  methods also gain `(var self)`. Do this once the checker is correct, gated so it
  only rewrites flagged names. Expect churn in `script/std/*` (List/Map/IO/os/pctl)
  and `pkg/builtins/pho/*.phl`.

### 2.6 Tooling

- **Highlighting** (`highlights.scm`): tag the trailing `!` of an identifier (or
  the whole name) with a distinct capture (e.g. `@function.macro`-sibling
  `@keyword.operator` on the `!`, or a `#match? "!$"` → `@function.method.mutating`)
  so effectful calls read differently. Highlight `(var self)`/`(var p)` — the
  `var` keyword inside a param list (a new `static`-style nested-head rule).
- **Hover** (`nav.go` `HoverAt`): append the inferred effect set / "pure" to a
  function's hover, and "mutable receiver" to a `(var self)` method. The effect
  set is already on the `Definition` from §2.4.
- **Completion**: surface `!` in the label (it's part of the name) and optionally a
  detail tag "effectful".
- **Outline / drift tests**: `name!` already flows through outline; add a checker
  drift test that the builtin effect table stays in sync, mirroring
  `TestBuiltinNamesMatchRuntime`.

## 3. Phasing (each phase keeps `go test ./...` green)

| Phase | Deliverable | Risk |
|---|---|---|
| **0** | This doc; lock the open questions in §8 | — |
| **1** | Lexer + grammar accept `name!`; `(var name)` recognized in `parseArgList` (bound, no checks) | low — additive token + param pattern |
| **2** | `arrowType` carries `Effects`+`Mut`; harvest receiver-mutability + name-`!` into signatures | low — data only |
| **3 — DONE** | `effects.go`: local self-mutation + `!`-call inference → `missing-bang` + `effect-through-readonly` (both sound); `spurious-bang` deferred to Phase 5 (needs `io`/`random` detectors). Gated behind `EffectCheck` (off) until stdlib migrates. | **med — the core**; gate behind a flag until stdlib migrates |
| **4 — DONE** | Stdlib migrated (`print!`/`print_line!`, `List.prepend!` gets `(var self)`); `EffectCheck` turned on in `cmd/pho-lint` + `cmd/pho-lsp` (package default stays off so Go test snippets opt in). `script/` audits 0 effect diagnostics. | med — wide churn (turned out tiny: `print`/`print_line` had no callers) |
| **5 — DONE** | **5a:** `io` kind + effect table (`effectfulPrimitives`, the dep-bridge writes); `mutates-arg` for `(var p)` params; `spurious-bang` turned on. Cross-module is free via the `!` name convention (no fixpoint). **5b:** `random` kind (`RandomInt`/`RandomFloat`; `random/float!`/`int-in-range!` migrated); full `mutates-free` as a positive effect with shadow-precise local tracking (`freeVarClassifier` + `collectLocalNames`). `script/` audits 0 across all codes; standing `TestStdlibEffectClean` guard. | med |
| **6 — DONE** | Highlights: `!`-effect and `?`-predicate identifiers tagged `@function.method` in every position (all three synced `highlights.scm`; grammar re-pinned). `(var self)`/`(var p)` already read as `var`-keyword via the list-head rule. LSP hover surfaces a callable's inferred effect set (`**effects**: io, mutates-self` / `pure`) via `callableEffectLabel` in `HoverAt`. | low |
| **7 — DONE** | `effect-in-pure-context`: property getters (auto-invoked on read) must be pure — `checkPureContext` runs the effect scan on the getter body. `bang-sig-mismatch`: a method impl whose receiver mutability disagrees with its inline signature's `(var Self)` is flagged (`checkBangSigMismatch` vs `w.methodSigArgs`). This required teaching `isFunSigForm`/`isFunSig` (linter + runtime) to accept `(var/spread/optional Type)` sig params so a mutable-receiver signature is expressible/erasable at all. `~pure`-fn / annotation-body pure contexts still wait on `~pure` being harvested into lint (currently inert). | med |

Recommended cut for a first usable release: **Phases 1–4 + 6** (self-mutation
only, fully enforced, with tooling). 5/7 are follow-ons.

## 4. Why `!` (and not an annotation)

`~pure` exists but is opt-in and unenforced; nobody writes it, so it rots. A
**name-level** marker that the checker enforces both directions (`missing-bang`
*and* `spurious-bang`) makes purity the default and effects loud at every call
site — you can see at the call `(self.grow_by! …)` that something mutates,
without jumping to a definition. The `!` also lets effects cross unresolved/
Dynamic boundaries (§1.2) without whole-program analysis, which a type-only
scheme can't. `~pure` is subsumed (a fn with no `!` is pure by construction).

## 5. Interaction with existing systems

- **Inline signatures** (TypeSignatures.md): the sig form already puts the
  receiver at param 0; this plan only adds the `(var …)` qualifier there and the
  `!` on the name. `bang-sig-mismatch` (Phase 7) checks impl vs sig agree.
- **Traits**: a trait method's required signature carries its effect set + receiver
  mutability; an implementor must match (a pure impl may satisfy an effectful
  requirement — pure ⊑ effectful — but not the reverse). `TraitMethod` (phtype.go)
  gains `Effects`/`Mut` alongside `Params`/`Result`.
- **Object model**: `Privileged`/`InstStack` are unchanged; `(var self)` is
  orthogonal to private-member visibility.
- **Gradual `Dynamic`**: a `Dynamic` callee contributes `{unknown}` effects gated
  on its spelled `!`, consistent with gradual typing's "trust the annotation".

## 6. Risks & mitigations

- **Migration churn** (every effectful name + call site changes). Mitigate with a
  gated codemod and a flag so Phase 3 lands dark, then flip in Phase 4.
- **False "effectful"** on benign patterns (e.g. building and mutating a *local*
  fresh collection). Mitigate: `(= local.x v)` on a binding *created in this
  function* is NOT an escaping effect — track "locally-owned" bindings and exempt
  them (a function that only mutates its own scratch state is still pure). This is
  the single most important nuance to get right; see §8 Q4.
- **Recursion / mutual recursion**: handled by the fixpoint; document the
  assume-pure-then-recheck rule.
- **`!` lexing collisions**: `!` was the *old* macro suffix (now `~`-prefix), so
  the token is free; ensure no leftover `!`-as-not-equal etc. (`~=` is not-equal;
  `!` is unused today — confirmed).

## 7. Concrete first slice (Phase 1–3, self-mutation only)

1. `positioned.go` + `grammar.js`: trailing `!` in identifiers (+ regen/commit/pin).
2. `decl.go parseArgList`: `(var name)` → `"&name"` slot; `BindMethod` binds self
   mutably (no behavior change).
3. `phtype.go`: `arrowType.Effects`/`Mut`; `effectSet` bitset; equality + assignability.
4. `pkg/lint/effects.go`: harvest + self-mutation `effectsOf` + fixpoint + the
   three core diagnostics, gated by an `EffectsCheck` flag (off in stdlib lint
   until Phase 4).
5. Tests: `effects_test.go` covering the example (Append! ✓, Append-without-`!` →
   `missing-bang`, read-only-self mutation → `effect-through-readonly`, pure fn
   with `!` → `spurious-bang`).

## 8. Open questions — DECISIONS

**Locked (2026):** Q2 = **required** (`(var self)` must be declared to mutate
self; `effect-through-readonly` otherwise). Q4 = **exempt** (mutation of a
locally-owned binding is pure). Recommendations below stand for the rest.



1. **Granular effects in the name?** **No** (upheld) — the name stays binary `!`.
   But the INFERRED set is now fully granular (2026-07-03): each effect is tracked
   by its own name — a primitive by its operation (`io-write`, `random-int`), a
   called `!`-function by its own name — so diagnostics/hovers say *which* effects
   run without the noise of spelling `f!{io}` at every declaration.
2. **`(var self)` required for self-mutation, or inferred?** Recommend **required**
   (explicit, matches the example, lets the sig state mutability) with a
   `effect-through-readonly` error otherwise.
3. **Ship `(var p)` (mutable non-self args) in v1, or `(var self)` only?**
   Recommend **`(var self)` only** first; generalize in Phase 5.
4. **Locally-owned mutation exemption** (mutating a collection you just made is
   pure): **yes**, and it's the key correctness nuance — confirm the ownership
   rule (a binding initialized in this scope from a literal/fresh constructor).
5. **Runtime enforcement** of receiver mutability? Recommend **static-only** in v1.
6. **Higher-order effect polymorphism** (effectful-iff-callback-is): **defer**;
   v1 uses the callback parameter's declared arrow `!`.
7. **`None` vs `Nil` return** of mutators: keep returning `none`; the sig's
   trailing type is `None` (the unit/none type), unrelated to effects.
