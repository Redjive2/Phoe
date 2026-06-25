# v1 Plan — Inline Type-Signature Syntax

Status: **proposed** · Replaces the *type-carrying* parse-time annotations
(`~sig`, `~type`, `~methodsig`) with first-class, position-independent
declaration forms that feed the gradual checker
([GradualTyping.md](GradualTyping.md) Stage E,
[Coordination.md](Coordination.md) §3–§4). The annotation **system** stays —
only its type macros are superseded; `doc`/`desc`/`pure`/`flag`/`macrohint`
keep working through `--@` unchanged.

**Verdict up front:** this is a front-end (surface-syntax) change. The runtime
type machinery (`*PhoType`, `Or`/`And`/`subtype?`/`Is?`, traits) is already
built; these forms are a new, ergonomic *source* of the declared-type info the
checker needs — replacing the annotation harvest at the same integration point.

## 0. Confirmed design decisions

1. **Case-based disambiguation** of signature vs implementation (§3).
2. Typed struct fields use the **`.{` overload only** (no `(struct Name (Field Type))` alternative) (§2.5).
3. **Keep the annotation system** for the non-type annotations (`doc`/`desc`/`pure`/`flag`/`macrohint`). Only `sig`/`type`/`methodsig` are retired in favor of inline syntax (§5).
4. **No generics yet** — `%A` type-variables are out of scope here; method sigs use concrete types plus `Self`/`Unknown`. Generics land later (and need the `%`-lexing the stdlib currently can't parse).

## 1. The five forms

```pho
;; 1. Typed binding (type optional — usually inferred)
(const (String x) "hi!")
(const x "hi!")                        ; common: inferred

;; 2. Function signature (forward decl) + separate implementation
(fun add (Number Number) Number)       ; signature
(fun add (a b) (+ a b))                ; implementation

;; 3. Method signature + implementation
(method Unknown.IsString? (Self) Boolean)
(method Unknown.IsString? (self) (self.Is? String))

;; 4. Trait — a named bundle of method signatures, with super-traits
(trait MyTrait (ExtendsThisTrait)
    (method Self.Something (Number) Number))

;; 5. Typed struct fields (via the .{ overload)
(struct MyStruct.{
    PublicField  Number
    privateField String
})
```

Two hard rules across all forms:

- **Signatures are hoisted to the top** — their source position is irrelevant; a sig may appear before, after, or apart from its impl (even in another file of the same package).
- **A signature without a matching implementation is a static error** (the linter, which runs before execution).

## 2. Syntax & semantics per form

### 2.1 Typed bindings — `const` / `var`
```
(const (Type name) value)   ; typed
(const name value)          ; inferred (unchanged)
```
- Disambiguation is **structural, not case-based**: the first argument is a bare
  leaf (untyped) or a two-element `(Type name)` list (typed). No ambiguity.
- Multi-binding (`(var a 1 b 2)`) extends naturally: each name slot may be
  `name` or `(Type name)`.
- **Runtime:** binds `name = value`; the type is erased (no runtime effect).
- **Checker:** records `Definition.Type` and verifies `value` against it
  (`provableMismatch`, already drafted in `typecheck.go`).

### 2.2 Function signatures — `fun`
```
(fun NAME (ParamType…) ReturnType)   ; signature  (types capitalized)
(fun NAME (param…) body)             ; implementation (names lowercase)
```
- **Runtime:** the `fun` builtin (`pkg/builtins/decl.go`) recognizes a sig form
  and **no-ops** — it must not try to evaluate `Number` as a body. The impl form
  creates the binding as today.
- **Checker:** the sig populates `Definition.Sig` (`funSig{Params, Result}`);
  call sites and the impl's return are checked against it.

### 2.3 Method signatures — `method`
```
(method Recv.Name (ParamType…) ReturnType)   ; signature  (first param = receiver type, e.g. Self/Unknown)
(method Recv.Name (param…) body)             ; implementation (first param = self)
```
- The first param slot is the **receiver**: `Self`/`Unknown`/a type in the sig,
  `self` in the impl. (This differs slightly from the old `~methodsig`, which
  split the receiver out before a `->`; here it is param 0, matching the impl.)
- **Runtime:** sig form no-ops; impl attaches the method to the receiver struct
  / primitive table as today.
- **Checker:** the sig populates `structInfo.MethodSigs[Name]`
  (`pkg/lint/infer.go:102`), exactly where `harvestMethodSigs` writes today.

### 2.4 Traits — `trait` (new head keyword)
```
(trait NAME (SuperTrait…)
    (method Self.M (T…) R)
    (method Self.N (T…) R)
    …)
```
- A new top-level declaration that builds a `core.TraitType`
  (`pkg/core/phtype.go` `TraitInfo`/`TraitType`/`TraitSatisfiedBy`) bound to
  `NAME`, gathering the required member signatures (and folding in the
  super-traits' requirements). `Self` is the implementing type.
- Member entries are method **signatures** (no body); a trait may later allow
  default bodies, but that is out of scope here.
- **Runtime:** registers the trait type value; **Checker:** `(x.Is? MyTrait)` and
  subtype checks already work via `TraitSatisfiedBy`.

### 2.5 Typed struct fields — `(struct Name.{ … })`
```
(struct Name.{ Field Type  field2 Type2  … })   ; typed
(struct Name f0 f1 …)                            ; untyped (unchanged)
```
- The `.{` construction sugar (`positioned.go` ~595–625) already turns
  `Name.{ F T … }` into the call `(Name "F" T "field2" T2 …)`. Inside a
  `(struct …)` head this is recognized as **field-name / field-type pairs**.
  The bare `(struct Name f0 f1)` form remains for untyped fields (type
  `Unknown`).
- **Runtime:** the `struct` builtin (`decl.go` ~560) extracts field *names* from
  the pairs (ignoring the types, which are erased) and builds the same
  `core.Struct`.
- **Checker:** records each field's declared type on `structInfo.Fields` for
  member-access checking.

## 3. Disambiguation — signature vs implementation (the crux)

`fun`/`method` sigs and impls share head + name + arity, so the split is
**content-based**, leaning on Pho's existing casing convention (Capitalized =
type/struct/export; lowercase = value/param/private — `isExportedMember`,
`startsLower`).

A `fun`/`method` form is a **signature** when:
- every element of its parameter list is a **type expression** — a capitalized
  identifier (`Number`, `Self`, `Unknown`, a user struct/trait), or a type-form
  (`(Or …)`, `(Fun …)`); **and**
- the trailing slot is a **return type** (same shape).

It is an **implementation** when the parameter slots are **names** — a lowercase
identifier, `(optional x)`, or `(spread x)` — and the trailing slot is a body.

**Matching** is by qualified name: among all `(fun add …)` / `(method R.m …)`
forms, the type-shaped one is the sig, the body one is the impl.

**Enforcement** (new diagnostics): a lowercase identifier in a sig param slot, or
a capitalized identifier used as an impl param name, is flagged — keeping the
two worlds cleanly separated.

**The one genuine wrinkle — 0-arg forms.** `(fun foo () Number)` is ambiguous:
`Number` could be a return type (sig) or a body yielding the type value
(impl). **Rule:** an empty param list `()` with a single capitalized/type-form
trailing slot is read as a **signature** *only if no body-form impl of the same
name exists*; otherwise it is the impl. A lone such form with no companion impl
therefore correctly triggers the "signature without implementation" error. (If
this proves surprising in practice, fall back to requiring an explicit marker
for 0-arg sigs — deferred unless needed.)

## 4. Hoisting & "signature without implementation"

- **Hoisting:** signatures are pure definitions, so they ride the existing
  definition-hoist in `liftDefinitions` (`pkg/modload/reorder.go`) — sorted ahead
  of `var`/`const`. The linter's collect pass already treats top-level
  definitions as hoisted within a file, so order-independence is largely free;
  the only new work is to **collect sig forms** in that pass.
- **Missing-impl check:** during the linter collect pass, build the sets of sigs
  and impls keyed by qualified name (`name`, `Recv.name`, `Self.x` for trait
  members). Every sig must have an impl, else emit a **`missing-implementation`**
  error at the sig's span. (The loader may mirror this as a backstop, but the
  linter is the "parse-time" surface.)
- **Duplicate-sig / sig-after-impl-type-conflict** become natural follow-on
  diagnostics.

## 5. Relationship to the annotation system (kept, trimmed)

The annotation **infrastructure stays** — `--@` lexing (`positioned.go`),
`ast.PAnnotation`, the isolated harvester (`pkg/annot/`), and the LSP hover path
(`nav.go annotationHover`). What changes:

| Annotation | Fate |
|---|---|
| `~sig` | **Disconnected, not deleted** (see note). Replaced as the checker's input by the inline `fun`/`method` signature; the checker reads `Definition.Sig` from the new form instead of `sigFromEntries`. |
| `~type` | **Disconnected, not deleted** (see note). Replaced as the checker's input by `(const (Type x) v)`; checker reads `Definition.Type` from the binding. |
| `~methodsig` | **Disconnected, not deleted** (see note). Replaced by the inline `method` signature; `structInfo.MethodSigs` is populated from the new form instead of `harvestMethodSigs`. |
| `~doc`, `~desc`, `~pure`, `~flag`, `~macrohint` | **Kept & active** in `script/std/annot/annot.phl`, surfaced in hovers exactly as today. |

> **Decision (kept-but-disconnected):** `~sig`/`~type`/`~methodsig` are **left
> in `annot.phl`, not removed**, in case they're needed again — each is marked
> with a `⚠ LEGACY … KEEP, DO NOT DELETE` banner. They stay **functional until
> Phase 3** (they are the gradual checker's only declared-type input today;
> disconnecting earlier would blind it and turn ~30 green tests red — the §8
> lockstep hazard). In Phase 3 they are **disconnected — made inert, source
> retained** — at the same moment the inline forms become the checker's input.

So `pkg/annot` and the `--@` syntax are **not** removed; the three type
macros **stay present but inert** after Phase 3, and the linter's *type*
harvest switches source from annotations to the new sig forms in lockstep. The `pkg/lint` consumers to rewire:
`methodsig.go` (`harvestMethodSigs`), `typecheck.go`
(`harvestEntries`/`sigFromEntries`/`typeFromEntries` → read inline sigs), and the
`MethodSigs`/`Definition.Sig`/`Definition.Type` population points.

## 6. Integration with the type system

The substrate is already in place (see [GradualTyping.md](GradualTyping.md)):
- `*core.PhoType` set-theoretic descriptor, interned (`pkg/core/phtype.go`).
- Type-value builtins `Number/String/…/Or/And/Not/subtype?/Is?`
  (`pkg/builtins/typeval.go`).
- Traits (`TraitInfo`, `TraitType`, `TraitSatisfiedBy`).
- The checker scaffolding: `funSig`, `flowEnv`, `provableMismatch`, the
  `declared` flow env on the walker (`walker.go`), `checkTypes` (`typecheck.go`).

These sigs supply the missing input:
- Add `Type *core.PhoType` and `Sig *funSig` to `Definition` (`scope.go:62`) —
  populated from the inline forms during collect.
- Type expressions in a sig are **evaluated to `*PhoType`** using the same env
  that already holds `Number`/`Or`/etc. (no `%` type-vars yet).
- This is precisely **Stage E** in the unified roadmap
  ([Coordination.md](Coordination.md) §5), which the gradual-typing track is
  mid-implementing — so Phases 3–4 below must be co-sequenced with that track
  (§8).

## 7. Phased implementation

Each phase keeps the suite green.

1. **Surface syntax, runtime-erased.** — ✅ **DONE (2026-06).**
   - Parser/shape: `shape.go checkSpecialFormShape` + `decls.go declOf` recognize
     the sig forms and the `(Type name)` binding shape; classify sig vs impl
     (§3). `decl.go` builtins skip sig forms (no-op) and handle `(const (Type x) v)`
     by binding `x` and discarding the type.
   - New `trait` builtin → `TraitType`. `(struct Name.{ … })` → field/type pairs.
   - No type checking yet; this is pure recognition + erasure.
   - **What landed:** typed bindings `(const/var (Type x) v)` (`declBindName` in
     decl.go, `bindName` in decls.go); `fun`/`method` SIGNATURE detection +
     runtime no-op (`isFunSig`/`isTypeNode` in decl.go) + lint skip (`IsSig` on
     `topLevelDecl`, `isFunSigForm`/`looksLikeTypePNode` in decls.go, guarded in
     `collectOne`/`checkFun`/`checkMethod`). `trait` + typed struct fields were
     landed by the gradual-typing track. Tests: `pkg/lint/typesig_test.go`,
     `pkg/builtins/typesig_test.go`. Suite green.
   - The sig/impl casing heuristic: a `(…)` form is a TYPE only when headed by a
     connective (`Or`/`And`/`Not`/`Diff`/`List`/`Map`/`Fun`/`Struct`/`Trait`) —
     a capitalized CALL `(Helper)` is an impl body, not a return type; the
     capitalized VALUE literals `Nil`/`True`/`False` are values, not types (the
     nil TYPE is `NilT`). This resolves most of the §3 0-arg wrinkle; only a
     bare 0-arg `(fun f () SomeType)` returning a type VALUE stays ambiguous.
   - **Not yet (deferred to later phases):** the §3 *enforcement* diagnostics
     (lowercase-in-sig / capitalized-impl-param) → Phase 2; semantic-token
     `@type` painting of sig type slots → Phase 5; feeding the checker → Phase 3.

2. **Hoist + missing-implementation.** — ✅ **DONE (2026-06).**
   - Collect sig forms in the linter; wire `liftDefinitions`; emit
     `missing-implementation`. Enforce the casing split (§3 enforcement).
   - **What landed:** `collectOne` records each fun/method sig into
     `walker.sigSites` (qualified name + span + kind); `checkMissingImpls`
     (run in `walkFile` after `collect`) emits **`missing-implementation`** when
     a sig's qualified name doesn't resolve to a matching `DefFun`/`DefMethod`
     through the full scope chain — so an implementation in a SIBLING file of
     the same package satisfies it (via `PackageScope`), while a lone sig is
     flagged. Hoisting is free: sigs collect in the order-independent collect
     pass and the check runs after, so sig-before-impl and impl-before-sig both
     lint clean; `liftDefinitions` needs no change (a sig is a runtime no-op,
     so its position is irrelevant). §3 enforcement: `flagCapitalizedParam`
     emits **`capitalized-param`** for a Capitalized identifier used as an impl
     parameter name (a probable mistaken sig), excluding `Self` (the
     conventional receiver name) and the value literals `Nil`/`True`/`False`.
     Tests in `pkg/lint/typesig_test.go`. Suite green.
   - **Deferred (the plan's "natural follow-on" diagnostics):** duplicate-sig
     and sig-vs-impl type-conflict detection — left for when the checker reads
     the sig types (Phase 3).

3. **Feed the checker (Stage E, co-owned).** — ✅ **DONE (2026-06), additively.**
   - Add `Definition.Type`/`Sig`; populate `structInfo.MethodSigs` from inline
     method sigs; evaluate type expressions to `*PhoType`; turn on
     call/return/assign checking against declared types. Switch the checker's
     input from annotations to sigs.
   - **What landed:** inline sigs now feed the SAME checker structures the
     `--@` harvest does, **alongside** the annotations (additive — the
     annotation path stays intact, so nothing is blinded). In `checkTypes`, a
     pre-loop resolves each inline `(fun … (T…) R)` to a `funSig` via
     `inlineFunSig`→`resolveTypeNode` (into the `sigs` map → call-arg + return
     checking) and each `(const/var (T x) v)` via `checkInlineTypedBinds` (into
     `base` → value checking); the return-check loop skips sig forms
     (`!d.IsSig`). `harvestMethodSigs` gains an inline pass: `inlineMethodSig`
     (param 0 = receiver type, dropped) → `structInfo.MethodSigs` → method-call
     checking. A legacy annotation for the same name still wins if both exist.
     Tests in `pkg/lint/typesig_test.go`. Suite green.
   - **NOT done — the disconnect is Phase 4.** Because the feeding is additive,
     `~sig`/`~type`/`~methodsig` stay **active**. Making them inert can only
     follow migrating their ~30 in-repo call sites (and stdlib) to inline forms
     — otherwise those sites lose their checker input. That migrate-then-
     disconnect is Phase 4 below, and it must co-sequence with the in-flight
     gradual-typing track (it edits that track's own test sites).

4. **Disconnect the three type annotations (do NOT delete them).** — ✅ **DONE (2026-06).**
   - Make `sig`/`type`/`methodsig` in `annot.phl` **inert** (kept in source, marked
     — see §5 note); delete the annotation-based type harvest (`harvestMethodSigs`,
     `sigFromEntries`/`typeFromEntries`). Migrate `pkg/builtins/pho/universal.phl`,
     `collections.phl`, and stdlib `--@ (~sig …)` sites to inline sigs. (Defer any
     site that needs `%` generics — keep its `--@ (~methodsig …)` until generics
     land.) **This must happen in lockstep with Phase 3** so the checker never goes
     blind.
   - **What landed:**
     - **Migrated 55 `--@ (~sig …)`/`--@ (~type …)` sites → inline** across 13
       checker-test files (typecheck/stagee/typedecl/trait/traitdecl/methodsig/
       record/propagation/nominalstruct/typedstruct/shapebridge/inbody, +
       atomtype prose only). Patterns: `(~type T)\n(var x v)`→`(var (T x) v)`;
       `(~sig (P…) R)\n(fun f …)`→ inline `(fun f (P…) R)` sig form; `(~sig Recv
       (P…) R)\n(method …)`→ `(method O.M (Recv P…) R)`. No site needed `%`
       generics; no test asserted spans, so the extra sig line was safe.
     - **Fixed a real latent §3 bug the migration exposed:** a signature whose
       RETURN is `Nil` (e.g. `(fun f (Drawable) Nil)`) was never recognized as a
       sig (Nil/True/False were excluded as value literals), so it mis-parsed as
       an impl (spurious capitalized-param + redeclaration). `isFunSig` /
       `isFunSigForm` now relax the return slot to admit Nil/True/False **when
       the params are non-empty and all types** (which already marks it a sig);
       the empty-param case stays strict so `(fun f () Nil)` is still a
       nil-returning impl. Fixed in both `pkg/builtins/decl.go` and
       `pkg/lint/decls.go` (+ `isReturnTypeNode`/`looksLikeReturnTypePNode`).
     - **Disconnected `~sig`/`~type`:** their `annot.phl` bodies are now inert
       (`Nil`), with the original harvest body preserved in a comment for revival
       and the banner updated to "DISCONNECTED (inert)". `~methodsig` and the
       other annotations (`~doc`/`~desc`/`~pure`/`~flag`/`~macrohint`) stay live.
       The harvest code (`sigFromEntries`/`typeFromEntries`/`harvestMethodSigs`)
       was KEPT (it now reads no sig/type entries and returns nil — harmless),
       rather than deleted, to avoid further churn on the in-flight checker.
     - **Collateral updated:** the annotation-system tests that drove the now-inert
       macros — `annot_phl_test.go` (type/sig subtests now assert inertness),
       `pkg/lint/annotation_test.go` `TestHoverShowsAnnotations` (switched its
       vehicle annotation `sig!` → the still-live `~doc`). Lexing/parsing tests
       (`pkg/syntax/annotation_test.go`, `stash_test.go`) were untouched — inert
       macros don't change tokenization. `traitdecl_test.go`'s lone `--@ (~type
       Point)` on a trait static-property (no inline form exists for that) was
       left in place; it's in a lints-clean test, so an inert annotation passes.
     - Full suite + `go vet` green.

5. **Traits + typed struct fields end-to-end, then docs.** — ✅ **DONE (2026-06).**
   - Full trait checking, struct-field typing in member resolution, semantic
     tokens (`SemTokType`), and `Doc/Language.md` / `Doc/Features.md` updates.
   - **Docs:** `Doc/Language.md` gained a "Type signatures" section (gradual
     model, typed bindings, fun/method signatures, the casing rule,
     `missing-implementation`/`capitalized-param`, and a note that the
     `--@ (~sig/~type)` annotations are disconnected).
   - **Trait checking + struct-field typing through member resolution** are
     owned by the in-flight gradual-typing track and already pass through the
     inline forms — the Phase 4 migration moved trait/typed-struct/record/
     nominalstruct tests onto inline sigs and they're green.
   - **Semantic tokens:** `pkg/lint/semantic.go` now paints inline signature
     type slots as `@type`. `semFun`/`semMethod` short-circuit on `d.IsSig` to
     `emitSigTypes` (params + return), and the typed-binding case uses the new
     `emitTypeNode` (a leaf type or a compound `(Or …)`/`(List …)` recursed) —
     so a method sig's receiver/param/return and a `(const ((Or …) x) …)` union
     all read as types, not parameters/code. The `"type"` legend entry already
     existed, so no LSP wiring change. Test: `semantic_sig_test.go`; the
     `TestSemanticTokensGolden` pin is unaffected (its forms are impls).

## 8. Risks & coordination

- **Heavy overlap with the in-flight gradual-typing track.** The checker
  (`typecheck.go`, `PhoType`) is owned by that track and is mid Stage D/E.
  Phase 3 (feed the checker) and Phase 4 (remove the type annotations the checker
  currently *reads*) must switch the checker's input from annotations to sigs in
  **lockstep**, or the checker goes blind. Co-sequence with
  [Coordination.md](Coordination.md) §5.
- **Concurrent parser cutovers.** This lands alongside the string-syntax
  migration (`'`-sigil removal, `"`→`'`) and the `slice`/`map` mangling. Three
  parser-level changes at once is fragile — **serialize** them; never run two
  parser cutovers concurrently.
- **`%` type-variable lexing** is half-landed (`unrecognized character '%'`);
  generic stdlib sigs (`universal.phl` `To`/`Guard`) stay on `--@ (~methodsig …)`
  until generics are a deliberate later phase (decision 4).
- **The 0-arg disambiguation wrinkle** (§3) is the one rule most likely to need
  revisiting under real usage.

## 9. Open items (non-blocking)

- Exact spelling of the receiver type in method sigs (`Self` for the
  implementing type vs the concrete receiver name) — settle when wiring Phase 3.
- Whether the loader should mirror the missing-impl check as a runtime backstop,
  or leave it lint-only.
- Trait default-method bodies (deferred).
