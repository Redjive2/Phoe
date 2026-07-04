# Discriminants

A **discriminant** is a method/function parameter, written `(disc X)`, whose
*statically-known value* selects which implementation resolves. It turns a value
— most usefully a type used as a value — into a compile-time dispatch key, so one
method name can carry several implementations chosen by the argument you pass:

```pho
(template O)
(trait To
    (method Self.to (Self (disc O)) O))      -- requirement: to, keyed by a disc of type O, returns O

(method String.to (Self (disc Number)) Number)   -- String implements `to` for the discriminant Number

('1.0'.to Number)   -- resolves to the (disc Number) impl → a Number (1.0)
('1.0'.to String)   -- no (disc String) impl on String → static error
```

It is Pho's analogue of return-type polymorphism / the turbofish
(`"1.0".parse::<f64>()`): the dispatch key is a first-class argument rather than a
type ascription, and it is verified statically while dispatching at runtime.

## 0. The rule, in one paragraph

`(disc X)` marks a parameter as a **discriminant**. In a trait/method *signature*
`X` is a type (often a `(template …)` parameter) describing the discriminant's
domain; in an *implementation* `X` is a concrete value (`Number`) naming the one
discriminant that impl handles. A receiver may carry **several same-named methods
that differ only by their discriminant value** — they are distinct
implementations, not a redeclaration. A call `x.m(D …)` resolves to the impl
whose discriminant equals `D`'s static value; if none matches, that is a static
error. When the discriminant is bound to a `(template O)` parameter, the matched
value also *fixes* `O` (and any `O`-typed result), which is what makes
`('1.0'.to Number)` statically a `Number`.

## 1. Semantics

### 1.1 The `(disc X)` modifier

`(disc X)` joins `(var X)`, `(spread X)`, `(optional X)` as a parameter-list
modifier (parsed in `parseArgList`, pkg/builtins/decl.go:21). Its two roles:

| Context | `X` is | Meaning |
|---|---|---|
| signature (`IsSig`) / trait requirement | a **type** (a `(template …)` var, or a concrete type) | the discriminant's *domain* — "this method dispatches on a discriminant of type X" |
| implementation | a **value** (a type-value like `Number`, or — later — any static value) | the *one* discriminant this impl serves; also the dispatch key it is stored under |

Unlike the other modifiers, `(disc V)` **binds no name** (§7 Q5): the value is a
dispatch key known at compile time, and the template it satisfies already supplies
the type.

A parameter list may carry **several** `(disc …)` params (§7 Q6) — the method then
dispatches on the ordered tuple of their values (multiple dispatch). They may sit
anywhere after the receiver, interleaved with ordinary params; their *positions*
are fixed so call arguments line up.

### 1.2 Resolution — static verification, runtime dispatch

Pho is interpreted, so dispatch is fundamentally **runtime** (the discriminant
value is a real argument), with the linter performing the same resolution
**statically** to catch misses early and to type the result.

A call `(recv.m D… rest…)` resolves against the set of `m` implementations on
`recv`'s type by the **tuple** of its discriminant arguments' static values:

1. **Concrete match** — the impl whose discriminant tuple is *identical* to the
   call's wins. Identity is `discKey` equality per slot (§3.2): interned-pointer /
   `TypeKey` equality for type-values, canonical-literal equality for atoms /
   numbers / strings / bools — a single map lookup, no set-theoretic work.
2. **Template fallback** — a slot declared `(disc O)` with `O` a template var
   accepts any value in that position; a fully/partly-template impl is the
   catch-all, chosen only when no more-concrete tuple matches (concrete-before-
   template priority, per slot left-to-right).
3. **No match** — a static `no-discriminant-impl` error (§1.5); at runtime, a
   method-not-found.

Exactly-one-winner follows from **identity matching** (§7 Q2 — `(disc Number)`
catches `Number` only, never a subtype) plus distinct discriminant tuples per
`(type, method-name)` (§3.5) and the concrete-before-template order — so there is
no ambiguity and no specificity ranking to compute.

### 1.3 Interaction with templates (the dependent-ish bit)

When the signature's discriminant is a `(template O)` parameter, the matched
discriminant *value* fixes `O` for that call, and any `O` elsewhere in the
signature (notably the result) resolves to it:

```
(method Self.to (Self (disc O)) O)     -- O is a template var
('1.0'.to Number)                      -- disc value Number ⟹ O = Number ⟹ result : Number
```

Mechanically: templates already resolve inside method/trait signatures through
`resolveTypeNode`/`collectTemplateVars` (typecheck.go:139, :250) and an `O`-typed
result already flows downstream. The new work is *binding* `O` from the call's
discriminant argument value before typing the result — a local substitution in
the call-site checker, not a change to the type-var machinery.

### 1.4 Interaction with traits

A trait method requirement with a discriminant (`(method Self.to (Self (disc O))
O)`) is **open**: `O` ranges over all types, so no single implementer can cover
every `O`. Satisfaction is therefore loosened: a type satisfies the requirement
by providing **at least one** `to` discriminant implementation (concrete or
template-fallback). `structMissingTraitMembers` (typecheck.go:429) already asks
only "does `si.Methods[name]` exist?"; with discriminants that becomes "does any
`to`-overload exist?", which the re-keyed method table (§3.2) answers directly.
Whether a *specific* `disc` is supported is a call-site concern (§1.2), not a
trait-satisfaction one.

### 1.5 Diagnostics

| Code | Severity | When |
|---|---|---|
| `no-discriminant-impl` | error | `x.m D` where no `m`-impl on `x`'s type has discriminant `D` (and no template fallback) |
| `duplicate-discriminant` | error | two `m` impls on one type declare the *same* discriminant value |
| `disc-non-static` | error | a discriminant argument whose value isn't statically known (v1 requires it be a constant/type) |
| `disc-in-plain-call` | error | `(disc …)` used on a `fun`/method that isn't dispatched on it, or more than one `(disc …)` in a list |
| `disc-sig-impl-mismatch` | error | an impl's discriminant value has no matching signature discriminant (ties into the inline-sig system) |

"`('1.0'.to String)` is a parse error" in the sketch is `no-discriminant-impl`
surfaced during analysis.

## 2. Worked examples

```pho
-- 2a. The motivating case: a typed parser.
(template O)
(trait Parse (method Self.to (Self (disc O)) O))

(method String.to (Self (disc Number)) Number)     -- sig
(method String.to (self (disc Number)) (num self))  -- impl body (parses)
(method String.to (Self (disc Bool)) Bool)
(method String.to (self (disc Bool)) (== self 'true'))

('1.0'.to Number)   -- 1.0        : Number
('true'.to Bool)    -- true       : Bool
('x'.to String)     -- error: no discriminant impl of `to` for String on String

-- 2b. A template-fallback (catch-all) impl beside concrete ones.
(method Json.decode (Self (disc Number)) Number)   -- fast path
(method Json.decode (Self (disc O)) O)             -- generic fallback for any other O

-- 2c. Non-type discriminant (deferred, §7 Q1): dispatch on an atom value.
(method Socket.send (Self (disc :json) String) None)
(method Socket.send (Self (disc :raw)  List)   None)
```

## 3. Layered implementation

Grounded in the three subsystems discriminants touch: the **parameter pipeline**,
**method identity/dispatch**, and **traits+templates**.

### 3.1 Parse — the `(disc X)` modifier

`parseArgList` (pkg/builtins/decl.go:21) and the linter's `collectParam`
(pkg/lint/walker.go:1494) learn a `disc` branch beside `spread`/`optional`/`var`.
Encoding: reserve a marker (e.g. `"@"` prefix, mirroring `"#"`/`"?"`) so
`BindMethod` can pull the discriminant slot out; the *value/pattern* it wraps is
kept as a resolved key, not bound as a name. The linter's param helpers
(`paramNames`, `paramMutability` in pkg/lint/effects.go:127) skip the discriminant
slot (it introduces no binding). Grammar/tree-sitter need no change — `(disc X)`
is ordinary list structure.

### 3.2 Method identity — re-key `name` → `(name, disc)`

The backbone. Method identity is `name`-keyed at four independent points; each
must admit a discriminant sub-key, and the "one impl per name" rejections must be
relaxed to "one impl per (name, disc)":

| Point | Today | Change |
|---|---|---|
| static sig table | `MethodSigs map[string]*funSig` (methodsig.go:50) | `map[string][]discSig` — overloads per name, each tagged with its disc key |
| runtime struct table | `sdata.Methods[name]` (builtins/decl.go:616; core/types.go) | second index by disc key (`map[string]map[discKey]Fun`) |
| runtime extension table | `AddMethod(typeKey,name,…)` → `[typeKey][name]` (core/member.go:79) | add disc key: `[typeKey][name][discKey]` |
| linter redeclaration gate | `define("Owner.name", DefMethod)` (walker.go:384) | key includes disc, or exempt disc-differing methods; emit `duplicate-discriminant` on true clashes |

`discKey(v)` is a canonical, hashable string identity for a *static* value `v`,
covering any value domain (§7 Q1): `TypeKey()` for a type-value (phtype.go),
`:name` for an atom, the canonical numeral for a number, the quoted text for a
string, `true`/`false` for a bool. Non-static discriminant arguments are rejected
(`disc-non-static`). With multiple discriminants the table key is the **ordered
tuple** `discKey(v₀)‖discKey(v₁)…`; a template-fallback slot contributes a
reserved wildcard key, and lookup tries concrete-then-wildcard per slot.

### 3.3 Runtime dispatch

The dot accessor resolves `inst.Struct.Methods[ident]` (pkg/builtins/dot.go:171)
and extension members via `ctx.ResolveMember` (dot.go:465). Both gain a second
step: after finding the name, evaluate the discriminant argument, compute its
`discKey`, and select the matching overload (concrete, else template-fallback,
else method-not-found). `BindMethod` (pkg/core/bind.go:126) already strips
prefixed slots (`#`/`?`); the discriminant slot is consumed here for dispatch and
(v1) not bound into the frame.

### 3.4 Static resolution + result typing

At a call site the checker (`checkMethodCall`, typecheck.go:1482 via
`methodSigFor`, methodsig.go:108) must: (a) infer the discriminant argument's
static value — trivial for a type constant like `Number`, which `inferShape`/the
type resolver already see (pkg/lint/infer.go; typeval.go:27); (b) pick the
overload with that `discKey`; (c) bind the template `O` to the value and resolve
the (possibly `O`-typed) result (§1.3); (d) if no overload matches, emit
`no-discriminant-impl`. `methodSigFor` grows a `disc` argument.

### 3.5 Trait satisfaction

`structMissingTraitMembers` (typecheck.go:429) treats a `(disc …)` method
requirement as satisfied by the *existence of any* overload of that name (§1.4).
`duplicate-discriminant` is checked when the re-keyed table is populated.

### 3.6 Tooling

- **Hover**: on a discriminant call, show which overload resolved and the bound
  `O`; on a definition, show its discriminant key.
- **Highlight**: `disc` reads as a keyword in `(disc …)` head position, like
  `spread`/`optional`/`var` (the existing list-head keyword query already covers
  it once `disc` is added to the keyword set — no new query shape).
- **Completion**: after `x.to `, offer the discriminant values that have impls.

## 4. Phasing (each phase keeps `go test ./...` green)

| Phase | Deliverable | Risk |
|---|---|---|
| **0** | This doc; §7 locked | — |
| **1 — DONE** | `(disc X)` recognised in `parseArgList`+`isSigParamNode` (runtime) and `collectParam`+`paramNames`+`looksLikeSigParam` (linter); binds no name (consumes its slot under a synthetic `@disc` key at runtime); disc signatures erase. No dispatch. | low — additive param form |
| **2 — DONE** | `core.DiscKey(v)` for every value domain (type/atom by interned-pointer identity; number/string/bool by value). New `DiscSet` + `DiscMethods` tables on `tstruct` and `tpackage`; `AddDisc`/`AddDiscMethod`; `ResolveMember` merges disc overloads across packages (open sets, `MemberResult.Disc`). Both method-defining sites (`method` + `=`) route disc methods via `storeDiscMethod`; both dispatch paths (struct dot + `memberAccess`) return a `discDispatch` wrapper that resolves by the call's discriminant values. Coexisting overloads dispatch (struct + extension); no-match + duplicate errors. | **high — the core** |
| **3 — DONE** | Linter method collection keys disc methods by `(name, static-disc-key)` (`methodDiscKey`/`discValueKey`, walker `discSeen`): distinct-discriminant overloads coexist (define the name once so refs + a sig's missing-impl check resolve), a repeated discriminant is `duplicate-discriminant`. | med |
| **4 — DONE** | `structInfo.DiscMethods` registry (positions + implemented-key set); `checkDiscCall` (hooked in `checkMethodCall`, covering struct AND primitive receivers via `shapeTypeName`) emits `no-discriminant-impl` when a static discriminant argument matches no impl, and `disc-non-static` when a function parameter is passed as a discriminant. Gradual elsewhere (`let`-bindings/unknowns skipped to avoid false positives without constant-folding). | med |
| **5 — fallback DONE; result-typing DEFERRED** | Template **fallback**: a `(disc O)` slot (O a template ⇒ top type Unknown at runtime) registers under a reserved **wildcard** key and matches when no concrete overload does — runtime (`core.DiscDefKey` + all-wildcard dispatch fallback) and linter (`walker.templateNames` → `discDefKey`; `checkDiscCall` wildcard fallback). **Deferred:** the dependent result-typing (`(disc O)` fixing `O` so an `O`-typed result is inferred from the matched value) — it needs per-overload sigs threaded through the gradual type-var machinery; gradual (Dynamic result) holds until then. | med |
| **6 — DONE** | Runtime `TraitSatisfiedBy` also checks a struct's `DiscMethods` (any overload satisfies); the extension path already resolved via `ResolveMember.Disc`. Fixed a latent trait-builtin bug surfaced by disc requirements — a requirement's RETURN TYPE (`… (self (disc O)) O`) was misread as a default-implementation body (making the member optional); now a type 4th element is the return type, not a body. Linter satisfaction already worked (disc methods register in `si.Methods`). | med |
| **7 — DONE** | `disc` highlighted as a keyword (list-head, beside `var`) in all three synced `highlights.scm`; grammar re-pinned. Hover on a discriminant method lists its implemented discriminants (`discKeysLabel`, wildcard → `(any)`). **Completion**: in a disc call's argument region, `discArgCompletions` (via `innermostListAt`) offers the implemented discriminant values (methods + free funs; wildcard omitted), then the rest of the scope. | low |
| **8 — DONE** | Free FUNCTIONS dispatch on `(disc X)`: `tpackage.DiscFuns` + `Context.AddDiscFun` + `discFunDispatch` (no receiver; wildcard fallback); `discInfo` takes a receiver offset (0 for funs, 1 for methods). Linter fun collection coexists disc overloads + flags `duplicate-discriminant`; the call site verifies matches (`w.discFuns` registry → `checkDiscFunCall` → shared `discCallCheck`). Runtime: two overloads dispatch, template fallback, duplicate rejected. | med |

**Result-typing — DONE.** A discriminant call's static type is the MATCHED
overload's declared result: `structInfo.DiscResults` maps `[name][disc-key] →
*PhoType`, populated in `harvestMethodSigs` from each overload's inline signature;
`exprType` (the method-call result path) selects it via `discCallResult` before
falling back to `methodSigFor`. So `(x.conv Number) : Number` and
`(x.conv Boolean) : Boolean` flow into typed argument slots (struct + primitive
receivers). Concrete overloads only — a template-var fallback result (`(disc O) O`)
stays gradual (`Dynamic`); resolving it to the discriminant's type is a further
refinement. Not yet wired into `inferShape`, so member access *through* a disc
call result stays gradual.

Arbitrary-value discriminants and multiple dispatch are in the **core** (Phases
2–5), not deferred. Recommended first cut: **1–5** — discriminants of any static
value, dispatched at runtime, statically checked, with template result typing —
covering the motivating example and multiple dispatch end to end.

## 5. Why a first-class discriminant (vs. alternatives)

- **vs. one method that switches on a normal arg**: you'd hand-write the dispatch,
  lose per-case signatures/return types, and get no static "unsupported target"
  error. Discriminants make each case a real, separately-typed implementation.
- **vs. distinct method names** (`to_number`, `to_bool`): loses the uniform
  `x.to(T)` call shape and can't be abstracted by a trait (`Parse`) or driven by a
  template `O`.
- **vs. return-type inference from context**: Pho has no bidirectional
  return-type-driven overload selection; a discriminant makes the choice explicit
  and local, which the interpreter can dispatch on directly.

## 6. Interactions & risks

- **Redeclaration semantics** — today two `String.to` are an *error* at both the
  linter and runtime (`AddMethod` rejects). This is the load-bearing change; get
  it wrong and either real duplicates slip through or valid discriminant sets are
  rejected. Gate behind the `(disc …)` marker so only disc-carrying methods
  overload.
- **Effects** — a discriminant impl is an ordinary callable: the `!`/effect
  checker (Effects.md) applies per-overload unchanged; the discriminant slot binds
  nothing so it never mutates.
- **Cross-module** — overloads may be added to a type from other modules via the
  extension table (`AddMethod`). Discriminant sets are therefore *open* across
  modules; `no-discriminant-impl` is only decidable with the full method set in
  scope (like current gradual member checks — unknown ⇒ no diagnostic).
- **`disc` as a name** — `disc` becomes a soft keyword in param-list head
  position only (like `spread`); a variable named `disc` elsewhere is unaffected.
- **Sig/impl split** — a discriminant appears in both the inline signature and the
  implementation; they must agree (`disc-sig-impl-mismatch`), reusing the
  `w.methodSigArgs` pairing built for `bang-sig-mismatch`.

## 7. Decisions

**Q1 — Value domain. LOCKED: any static value from the start.** A discriminant is
any statically-known value — a type (`Number`), an atom (`:json`), a number
(`42`), a string, a bool. `discKey` canonicalises any such value (see §3.2);
`disc-non-static` rejects a discriminant argument whose value isn't a compile-time
constant.

**Q2 — Matching. LOCKED: identity only.** `(disc Number)` matches `Number` and
nothing else; `(disc 42)` matches `42` only. No subtype/coercion — resolution is a
single `discKey`-tuple lookup, never ambiguous, no specificity order.

**Q5 — Body binding. LOCKED: the discriminant binds no name.** It is a dispatch
key; the impl already knows statically which value it serves (and the template
supplies the type). No `(disc (T name))` binding form.

**Q6 — Multiplicity. LOCKED: multiple discriminants allowed (multiple dispatch).**
A method may declare several `(disc …)` params; the dispatch key is the *ordered
tuple* of their `discKey`s, matched positionally against the call's discriminant
arguments.

**Q3 — Runtime model.** *Default (accepted):* **runtime dispatch by value +
static verification** — fits the interpreter; discriminants are real arguments.
Not static monomorphization (no compile/rewrite pass exists).

**Q4 — Template fallback impl.** *Default (accepted):* **yes**, lowest priority —
a `(disc O)` (template-var) impl is the catch-all when no concrete tuple matches
(example 2b). With multiple discriminants, fallback is per-slot (a slot may be
concrete or template).

**Q7 — Scope.** *Default (accepted):* **method-only in v1** — dispatch needs a
receiver type to key on. Free-function overloading is a separate follow-on.
