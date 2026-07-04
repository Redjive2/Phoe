# Declaration / Implementation split: `fun`/`method` sigs vs `(= …)` impls

Status: **Phases 1+2 (core fun/method) + the property delegate change, all TOLERANT,
IMPLEMENTED — suite green** (in the shared working tree, on top of the sibling's
uncommitted Phase-0 work). **Phase 3 codemod BUILT + validated (not yet RUN)** —
`tooling/snakecase/split.go` adds a `-split` pass (`-split` for `.phl`/`.pho`,
`-split -go` for embedded Go strings); 15 unit tests green (`split_test.go`);
read-only dry-runs on the real tree confirm correct fun/method impl→`=` head-swap,
signature preservation (trait-body sigs stay `method`), property delegate unwrap
(incl. multiline bodies), and **idempotence** (re-run = 0 edits). The RUN is still
gated on Phase 0: it would rewrite ~118 files including the sibling's 64 uncommitted
`pkg/*` files. Remaining: **run the codemod (after Phase 0) + Phase 4 (intolerant
hardening + jump-to-impl + tree-sitter)** — which still wait on Phase 0 (the
sibling's type-sig/template/effects work committing to `main`).

Property delegate (both forms coexisting):
- runtime: `property` builtin reads the target first, then `propertyDelegates` computes
  getter/setter from EITHER the new `(get (params) body)` sub-forms (bind via
  BindMethod/BindFun) or the old flat `get GETTER` values. Struct + free-standing +
  typed-target all run; get-only (read-only) supported.
- lint: `declOf` target unchanged; `checkProperty` walks each accessor body in a scope
  (self shape from the owner), `checkPureBody` for getter purity; `shape.go` accepts
  the 2-or-3 arity; `semProperty` paints get/set + params + body; `propertyBodyScope`/
  `accessorBodyScope` open completion scopes (verified `self.field` completes).
- tests: `pkg/builtins/declimpl_property_test.go`, `TestDeclImplPropertyLint`.

Done so far (fun/method `(= …)` impls, both forms coexisting):
- `declOf` `case "="` → 4-child normalizes to Head fun/method impl; 3-child stays reassign.
- reference-walk dispatch routes 4-child `=` → `checkFun`/`checkMethod` before `checkAssign`
  (the critical silent-drop/ordering guard); `shape.go` accepts the 2-or-3 arity.
- editor: semantic tokens, `DocumentSymbols`, hover (`renderDeclHeader`/`declFormContaining`),
  and completion (`bodyScopeFor`) all handle the `=` impl.
- runtime: the `=` builtin is arity-overloaded (`defineFunOrMethod`); impls run, recurse,
  sigs erase; old `(fun …)` impls + 2-arg reassign still work.
- tests: `pkg/lint/declimpl_phase1_test.go`, `pkg/builtins/declimpl_phase2_test.go`;
  `testdata/surplus_arity.pho` updated (`=` is now 2-or-3 arity).

This plan was produced by a 6-surface mapping + adversarial critic pass against the
current (dirty) working tree.

## Goal

Separate the **declaration** (type signature) of a function/method from its
**implementation**. `fun`/`method` keep the current syntax but become
declaration-facing; implementations move to a new `(= …)` form.

| | Declaration (signature) — CURRENT syntax | Implementation — NEW `(= …)` form |
|---|---|---|
| function | `(fun add (Number Number) Number)` | `(= add (a b) (+ a b))` |
| method   | `(method Pair.add (Self) Number)`   | `(= Pair.add (self) (+ self.a self.b))` |
| generic  | `(template ((From String) Parseable))` + `(method Parseable.parse (Self) Parseable)` | `(= Parseable.parse (self) (Parseable.from self))` |

`(= target value)` — the 2-argument form — **stays reassignment**, unchanged
(`(= x 5)`, `(= obj.#field v)`). The `=` builtin becomes **arity-overloaded**.

## Decisions (locked)

1. **No anonymous functions OR methods.** Both `(fun (args) body)` and
   `(method Recv (self) body)` value forms are removed entirely (see §"`fun`/`method`
   are two-state").
2. **Scope.** `fun` + `method` impls move to `(= …)`. `static` keeps its syntax.
   **`property` get/set delegates gain a new inline form** (forced by decision #1 —
   they were anonymous fun/method values; see §"New property delegate syntax").
   **Method forms inside `(trait …)` bodies follow the same split** — sigs stay
   `method`, named impls → `(= …)` (decided 2026-07-01).
3. **Full atomic codemod** migration of every impl, including the
   `tooling/tree-sitter-pho/examples/*` files (which are also fully de-staled from the
   older pre-cutover syntax).
4. **Go-to-def / hover lands on the SIGNATURE** when one exists (decided
   2026-07-01): a reference to `add` resolves to `(fun add …)`; the `(= add …)` impl
   provides the binding, body, and effects. Impl-only names (no sig) resolve to the
   impl.
5. **Timing:** plan now, implement after the sibling's type-sig/template/effects
   work lands (their `(template …)`, inline sigs, gradual checker, and effects
   phases live only in the working tree today).
6. **`(spread …)` / `(optional …)` live in the SIGNATURE, not the impl** (added
   2026-07-01). When a signature declares a variadic or optional parameter, the
   implementation names it *plainly* — the modifier is NOT repeated:
   ```
   (fun add ((spread Number)) Number)       -- sig: variadic
   (= add (numbers) …)                       -- impl: plain `numbers`, NOT ((spread numbers))
   (fun greet (String (optional String)) String)
   (= greet (name suffix) …)                 -- impl: plain `suffix`, NOT (optional suffix)
   ```
   **Current behavior (CONFIRMED BROKEN — the gap this decision fixes):** sigs are
   erased at runtime (`isFunSig`→`TvNil`), so a plain-param impl gets NO variadic/
   optional info. Probed: `(fun add ((spread Number)) Number)` + `(= add (numbers)
   numbers)` + `(add 1 2 3)` → returns **`1`** (numbers = first arg only; 2,3 silently
   dropped), identical to having no sig at all. Only the explicit `(= add ((spread
   numbers)) numbers)` form collects `[1,2,3]` today.
   **Mechanism required (why it's Phase-0 integration, not a quick fix):** the runtime
   derives variadic/optional ONLY from the impl's `parseArgList` encoding (`(spread
   name)`→`"name…"`, `(optional name)`→`"?name"`; `pkg/builtins/decl.go`). To let the
   impl omit the modifier, the runtime must read it from the matching signature at
   impl-definition time — which needs a **runtime sig-stash** the interpreter does not
   have today (inline sigs are read only by the checker). Approach: the `fun`/`method`
   sig branch records each param's modifier + position keyed by name (instead of fully
   erasing); `defineFunOrMethod` (+ the `fun`/`method` impl branches) look it up and
   splice the `…`/`?` markers into the impl's param encoding. Requires sig-BEFORE-impl
   in source/lift order. This belongs with the sibling's TypeSignatures infrastructure
   (a parallel stash would duplicate/conflict it) → **do at Phase-0 integration.**
   *Fallback (no sig):* the impl still carries `(spread)`/`(optional)` standalone, as
   today. *Related latent bug:* surplus args to a non-variadic impl are currently
   dropped silently (no arity error) — worth an arity-strictness check alongside.

## The `=` arity-overload is unambiguous (by child count)

Verified empirically: **every** existing `(= …)` reassign in real `.phl` is a
3-child branch `(= NAME value)`; a define is a 4-child branch `(= NAME (params) BODY)`.
No 4-child `=` exists today (the runtime hard-errors on `argv != 2`). So:

- **3 children** → reassignment (`(= x 5)`, `(= obj.#field v)`, `(= x (f y))`).
- **4 children** → define: **bare-ident** child1 ⇒ function; **`Owner.name` dot**
  child1 ⇒ method.

Dispatch on **`len(children)`**, never on "does child1 look like a dot" or "does
child2 look like a param list" — the count is the only reliable discriminator.
A bodyless define `(= f (a b))` collapses to a 3-child reassign (a near-miss —
see §Diagnostics).

## `fun`/`method` are two-state (all anonymous forms removed)

Per decision #1, ALL anonymous functions and methods are removed. So `fun`/`method`
have exactly **two** states:

1. **Named + all-type params** ⇒ **SIGNATURE** (erased at runtime; feeds the checker).
   `(fun add (Number Number) Number)`, `(method Pair.add (Self) Number)`.
2. **Named + non-type params** ⇒ **ERROR** (post-cutover) pointing at `(= …)`.
   This catches un-migrated code. (Tolerant during the rollout window.)

No `(fun (args) body)` value, no `(method Recv (self) body)` value. The
`(fun (I) O)` **function-TYPE** in a sig param position is NOT a value and is
untouched (still recognized by `looksLikeTypePNode`, `decls.go:113`).

### New property delegate syntax (FINAL — user-specified)
Property get/set delegates were anonymous `(fun …)`/`(method …)` values — now gone.
The get/set operands become **parenthesized `(get (params) body)` / `(set (params) body)`
sub-forms** (self-delimiting; no keyword-position parsing):

    (property (Number temp)
        (get () backing_temp)
        (set (new_temp) (= backing_temp new_temp)))

    (property Animal.has_legs?
        (get (self) (not self.is_fish?))
        (set (self is_fish?) (= self.is_fish? is_fish?)))

- **Target** (`argv[0]`) is **unchanged**: a typed free-standing `(Type name)`, a struct
  dot `Owner.name`, or a typed struct `(Type Owner.name)`. `stripPropType` +
  `declOf`'s `PropType` detection (the sibling's typed-property work) are **reused as-is**.
- **`argv[1]` = `(get (params) body)`; `argv[2]` = `(set (params) body)`** (optional —
  read-only = get only). Struct getter param0 is `self` (bind via `BindMethod`);
  free-standing has no `self` (`BindFun`).
- Verified: parses with **no grammar.js change**. `folds`/`indents` need nothing.

> **Ergonomic ceiling:** with no anonymous functions, a **multi-arg inline callback**
> has no form — only `&`-blocks (one implicit `it`) survive for inline use. Verified
> **zero** such callbacks exist today; every current anonymous fun is 0/1-arg and
> migrates to an `&`-block or a named top-level `(fun …)` + `(= …)`.

## Surface-by-surface changes

### Runtime — `pkg/builtins/decl.go`
- **`=` builtin (~L752):** split on `len(argv)`. Keep the entire `len==2` reassign
  body verbatim. Add `len==3` → new `defineFunOrMethod(ctx, argv)`. Relax the arity
  guard from `!= 2` to `!= 2 && != 3`.
- **`defineFunOrMethod`:** `methodTarget(argv[0])` → `named==true` (dot) ⇒ method
  define (move `decl.go:554-623` **wholesale** — the `StructOf`/`MemberKeys`
  union-loop/`typeKey==""` universal branch must survive); `named==false` (bare leaf)
  ⇒ function define (`declName` + `parseArgList(argv[1])` + `ctx.Declare(name,
  TvFun(BindFun(name, argv[2], argList, ctx)), true)`). Use `Declare` (not `Set`) so
  redeclaration still fires; `argv[1]`=params, `argv[2]`=body.
- **`fun` builtin (~L449):** remove the anonymous 2-arg branch; the named 3-arg
  branch requires `isFunSig` (erase → `TvNil`) else error "'fun' declares signatures
  only; write the implementation as (= name (params) body)".
- **`method` builtin (~L529):** named+sig → erase; named+non-sig → error at `(= …)`;
  **remove** the anonymous (bare-receiver) branch (decision #1 — no anonymous methods).
- **`property` builtin (~L649-730):** replace the flat `argv[1]=='get'` keyword +
  `argv[2]`=getter-value reading with: parse the target **first**
  (`methodTarget(stripPropType(argv[0]))` — so `onStruct` is known); then read
  `argv[1]` as a `(get (params) body)` branch (validate head `get`; `parseArgList`;
  `BindMethod(recv.name, body, args, ctx)` if struct — validate `args[0]` is the `self`
  name — else `BindFun`); `argv[2]` likewise for `set` (optional). The downstream
  `core.Property{Getter,Setter,HasSetter}` storage branches are **unchanged** (they take
  pre-built values). New arity: 2 (get only) or 3 (get+set).
- **`static property` (~L314-350):** apply the **same** sublist rewrite — it shares the
  flat get/set-value grammar and must migrate in lockstep (anonymous methods are gone
  globally), even though `static` is otherwise out of scope.
- Impl standalone: `(= name …)` needs **no** prior sig at runtime; sig↔impl pairing
  is a lint, not a runtime constraint.

### Lint / effects — property (`pkg/lint/{decls.go, walker.go, effects.go}`)
- **`declOf case "property"` (~L314-337):** target parse **unchanged** (`PropType`
  reused). ADD normalized get/set fields to `topLevelDecl` (`GetArgList`/`GetBody`/
  `SetArgList`/`SetBody`) by scanning `br.Children[2:]` for `(get …)`/`(set …)` sublists
  — so consumers stop positional-indexing children 3/5.
- **`checkProperty` (walker.go ~L1276-1289):** reference-check each get/set BODY in a
  body scope binding its params (`walkFunctionBody(scope, getArgList, getBody, d.Owner)`
  — `d.Owner` drives the privileged `self` shape for a struct property).
- **`checkPureContext` (effects.go ~L439-463):** it currently does `declOf(getter).Body`
  — but a `(get …)` sublist has **no declOf case**, so purity checking silently dies.
  Change its signature to take `(argList, body)` directly (extract from the sublist).
- **Trait requirement members** `(property self.name get)` use **bare `get`/`set`
  leaves** (no body — a requirement flag, `typecheck.go:391`) and stay parsed as-is;
  only property IMPLEMENTATIONS and trait DEFAULT bodies use the new delegate form.

### Lint — declOf / scope — `pkg/lint/{decls.go, walker.go, shape.go}`
- **`declOf` new `case "=":`** — 4-child + bare-ident child1 ⇒ `Head="fun"`,
  Name/ArgList/Body, `IsSig=false`; 4-child + `Owner.name` PDot child1 ⇒
  `Head="method"`, Owner/Name/ArgList/Body. 3-child ⇒ `ok=false` (stays reassign).
  **Normalize Head to `"fun"/"method"`** (mirrors the `let`→const/var normalization
  at `decls.go:388-411`), keeping `d.Branch` as the `=` form for rendering. **This is
  the key lever** — declOf-driven consumers (`collectOne`, `checkMissingImpls`,
  `checkFun`, `checkMethod`) then need ~zero change.
- **`declOf` `case "fun"/"method"`:** force `IsSig=true` for the named forms (impls
  moved to `=`); drop the anonymous-`fun` arm.
- **`shape.go checkSpecialFormShape` `case "=":`** relax the hard `nargs != 2`; accept
  `nargs==3` (validate child1 = name or `Recv.name`, child2 = param list).
- **Contract note (publish it):** `topLevelDecl.Head` is now the *normalized* kind,
  not the source keyword. The **headIdent-driven** consumers that bypass declOf each
  need an explicit `=` case (see §Critical points).

### Type-checker + templates — `pkg/lint/{typecheck.go, methodsig.go}` (sibling's area)
- The `IsSig` guards already do the right thing once declOf normalizes: sig loops
  (`typecheck.go:930`, `methodsig.go:58`) fire only for the keyword sigs; the impl
  loops (`1008`, `1138`) select the `=`-normalized `!IsSig` forms.
- Move the nested impl-body descent from `checkFlow`'s `case "fun"/"method"` into
  `case "="`.
- `collectTemplateVars` unchanged — template method **sigs** keep the `(method …)`
  head; the impl `(= I.bind (self fn) …)` has no type slots.
- **Preserve** `isFunSigForm`/`looksLikeTypePNode`/`typeConnectives` — still needed to
  recognize a `(fun (I) O)` **function-type param** inside a surviving sig
  (`decls.go:113`) and by the codemod. Only the *sig-vs-impl dispatch* use is retired;
  only simplify the empty-param strict branch **after** the codemod.

### Editor — `pkg/lint/{semantic.go, nav.go, completion.go}`
Each **bypasses declOf via `switch headIdent(br)`** and needs an explicit `=` case:
- **`semantic.go`** dispatch (~L217): 4-child `=` define → paint name @function/@method
  + params @parameter + body as code (reuse `walkFunctionLike`); 3-child → `semAssign`.
- **`nav.go`** `renderDeclHeader` (~L348) and `DocumentSymbols` (~L679): treat a
  4-child `=` as a fun/method decl (outline symbol, hover header); `declFormContaining`
  (~L315) must include define-shaped `=`. **Go-to-def target = the SIGNATURE** when one
  exists (decision #4): `collect` must record the sig's name span as the
  `Definition.Span` used for navigation, so a reference to `add` jumps to `(fun add …)`;
  the `(= add …)` impl still binds the value/body/effects but does NOT override the nav
  span. When there is no sig, the impl's span is the target. Hover renders the sig's
  type; the impl link can be a secondary "implementation" location.
- **`completion.go`** dispatch (~L49) + `bodyScopeFor` (~L295) + the two nested-recursion
  sites: open a body scope + bind params for a 4-child `=`. Drive `bodyScopeFor` off
  `declOf` (`d.ArgList`/`d.Body`/`d.Owner`) rather than positional indexing. Property
  get/set body scopes now read the inline `(params) body` form directly (no
  fun/method wrapper).

### Effects — `pkg/lint/effects.go` (sibling's area — cross-surface leak)
`scanEffects` early-returns for nested `fun`/`method`/`macro` (L318) to stop
descending into nested callables — **but a nested `(= …)` define has head `=` and is
NOT caught**, so its effects get attributed to the **outer** callable. Add:
`if head=="=" && len==4 && child2 is a param-list: return`.

### Jump-to-implementation (LSP `textDocument/implementation`)
Complements decision #4 (go-to-def lands on the SIG). The impl span is **already
recorded by name** — no new bookkeeping needed:
- **Function impl:** `(= add …)` → declOf Head=`fun`, `collectOne` calls
  `w.define(scope, "add", DefFun, implNameSpan)`. Look up via `scope.Lookup("add")`.
- **Method impl:** `(= Owner.name …)` → `w.define` + `si.Methods[name]=implSpan` +
  `si.MethodFiles[name]`. The SIG arm **never writes `si.Methods`**, so the structInfo
  entry is unambiguously the IMPL. **Route method lookups through `structInfo`, NOT
  `scope.Defs`** (whose `DefMethod` span becomes the sig under #4) — the single most
  error-prone point.
- New `lint.ImplementationAt(path, src, line, col)` mirrors `DefinitionAt`: resolve the
  token → qualified name → impl span by name. Impl-only ⇒ def==impl (harmless);
  sig-only ⇒ return nothing. Cross-file works via `PackageScope`. Property members have
  no separate sig/impl (the getter/setter body *is* the impl, co-located) — feature
  applies to fun/method only.
- **`cmd/pho-lsp`:** add `implementationProvider: true` to `initializeResult`
  capabilities; dispatch `textDocument/implementation` → a `handleImplementation`
  modeled on `handleDefinition` (same params/result shape; use `rangeInFile` for
  cross-file UTF-16 columns).

### tree-sitter — `tooling/tree-sitter-pho` (queries only; NO grammar.js change)
`(= add (a b) body)` / `(= Pair.add (self) body)` already parse as plain lists
(name as a `dot_chain`). Add **arity-anchored** query patterns to
`highlights/outline/locals.scm`. The **`. (_)` trailing anchor after the param `(list)`
is load-bearing** — it requires a 4th (body) node so `(= x (f y))` reassign-to-a-list is
excluded. Function highlight example:
`((list . (operator) @_eq . (identifier) @function . (list) . (_)) (#eq? @_eq "="))`.
**Property delegate queries:**
- **highlights** — paint the `get`/`set` heads of a `(get …)`/`(set …)` sublist inside a
  `(property …)` (and `(static property …)`) as `@keyword`. **Append these AFTER the
  builtin-functions block** — otherwise the existing builtin rule (which lists `get`)
  paints `get` `@function.builtin` (a real regression). Two anchored rules; verified
  0 over-fire on a plain `(get coll key)` call.
- **locals** — a `(get/set (params) body)` sublist opens a scope + binds its params;
  use the `. (list) . (_)` anchor so `(get coll key)` is excluded.
- **outline** — 3 new item rules for the typed free-standing `(property (Type name) …)`,
  typed struct `(property (Type Owner.name) …)`, and bare free-standing `(property tally
  …)` targets (the existing rule only catches bare struct `Owner.name`). Do NOT nest
  getter/setter as items (only label is the literal `get`/`set`).
- `folds`/`indents` unchanged. **Sync note:** `zed-pho` now has **two** query dirs
  (`languages/pho/` AND `grammars/pho/queries/`) — mirror edits into both + the
  tree-sitter copy (a 3-way sync; add a drift check). Query-only ⇒ no grammar SHA bump.
  Add corpus entries for each property shape.

### Codemod — extend `tooling/snakecase`
Classify sig-vs-impl with the **same** `isFunSigForm`/`looksLikeTypePNode` predicates
(import from `pkg/lint`, don't copy — avoid drift; add a golden drift test). Steps:
1. **fun/method IMPL → `=`:** overwrite **only the head token** `fun`/`method` → `=`
   (name/receiver/params/body untouched). Leave signatures.
2. **Property delegate unwrap (STRUCTURAL, not a head-swap):**
   `(property T get (method Recv (params) body) set …)` →
   `(property T (get (params) body) (set …))`. Per accessor, two span-edits: replace
   `get (` (through the delegate's open paren) with `(get `; delete the delegate head
   `method Recv `/`fun ` up to the `(params)` list. The delegate's own `)` closes the
   new `(get …)`. Handle get-only (no set). **Rewrite ONLY top-level `(property …)`
   forms — never descend into `(Trait …)`/`(struct …)` bodies — and SKIP any property
   under a `static` head.** (Trait requirement `(property self.name get)` uses bare
   `get`/`set` FLAGS, and `static property` keeps the flat form — corrupting either is
   the highest-risk mistake; negative golden tests are mandatory.)
3. **Non-property anonymous funs:** the remaining single-arg value callbacks
   (`(5.bind (fun (n) (+ n 1)))`) → `&`-blocks (`&(+ it 1)`) where `it` suffices;
   flag the rest for manual naming (`-n` report). `(fun (I) O)` function-TYPES are left.
4. **Glob:** `script/std`, `pkg/builtins/pho`, `testdata`, `pkg/**/*_test.go`, **and
   `tooling/tree-sitter-pho/examples/*`** (decision #3 — also de-stale their older
   pre-cutover PascalCase / bare-receiver syntax). Reuse `MigrateGoFile` for the
   embedded `_test.go` impls. `-n` dry-run; refuse on lex/parse error.

**Structure it as a new `-split` pass** (parallel to `-recase`/`-migrate`), run
**after** the casing migration and **never interleaved with `Recase`** (the property
unwrap removes the fun/method wrapper that `Recase` relied on to scope delegate params;
re-running `Recase` over unwrapped output would mis-recase `self`/`v`).

> **BUILT (not yet run) — `tooling/snakecase/split.go`.** `SplitTransform` (and
> `SplitGoFile` for embedded Go strings) walk top-level forms + trait bodies,
> head-swapping named non-sig `fun`/`method` impls to `=` and unwrapping
> `get/set (method Owner (params) body)` / `(fun (params) body)` delegates to
> `(get/set (params) body)`. Sig-vs-impl detection mirrors `decls.go`'s
> `isFunSigForm`/`looksLikeSigParam`/`looksLikeTypePNode` **verbatim** (copied,
> not imported — keep in sync); `static` subtrees are skipped; trait *requirement*
> `(property self.x get)` flags are left bare. Flags: `-split`, `-split -go`, `-n`.
> Refuses on lex/parse error; span-anchored (format-preserving); **idempotent**
> (re-run = 0 edits, so double-application is safe). 15 tests in `split_test.go`.
> The actual RUN is gated on Phase 0 (would rewrite the sibling's 64 uncommitted
> `pkg/*` files). Re-run the census (below) against the post-Phase-0 tree before
> firing — the sibling's `disc`/template/typed-prop work shifts some counts.

## Census (verified against the current dirty tree — re-run after Phase 0)
- **336 impls to rewrite** (head-swap → `=`): 35 in real `.phl`, 6 in `testdata`, 295
  embedded in `_test.go`.
- **84 signatures to leave** (70 fun + 14 method).
- **Property delegates to unwrap: ~10 standalone `(property …)` forms / ~15 accessor
  operands** (7 method-based struct/union, 6 fun-based free-standing; 2 typed targets;
  3 read-only): `pctl.phl` (3, multiline), `collections.phl` (3), `{objmodel,property,
  typedprop}_test.go` (4). *(Corrected — the earlier "~36" wrongly folded in trait-
  requirement and `static property` forms, which are NOT delegate-unwraps.)* `typedprop_
  test.go` is a NEW untracked file; re-baseline after merge.
- **Remaining single-arg anonymous fun callbacks** → `&`-blocks / named (a handful, in
  tests).
- **4 `(fun (I) O)` function-type forms** — leave (types, not values).
- **`tooling/…/examples/*`** — migrate fully (decl/impl split + older stale syntax).
- Known leave-behind: `testdata/badlib/bad.phl` (intentional parse-failure fixture —
  parser refuses it, codemod skips the file).

## End-to-end validation (2026-07-01) — codemod proven correct; flip needs an integration pass

The `-split` codemod was validated end-to-end **in an isolated copy of the dirty working
tree** (rsync minus `.git`/`target`; zero writes to the real tree): migrate all
`.phl`/`.pho` (`-split`, 69 edits) + all embedded `_test.go` (`-split -go`, 379 edits) =
**448 edits**, then `go build ./...` + `go test ./...`. Result:

- ✅ **The codemod produces valid, semantically-correct Pho.** Build passes; every
  RUNTIME/semantic package is GREEN on migrated code: `pkg/builtins`, `pkg/core`,
  `pkg/goop`, `pkg/syntax`, `cmd/pho-lsp`. So the head-swap + property unwrap are sound.
- The failures are **downstream consumers that key on the literal `fun`/`method` head**
  (so a `(= …)` impl is invisible to them) plus tests asserting old-form output. Three
  classes:

  **(A) Self-migration / negative-test fixtures — exclude from the run.** The `-split -go`
  pass rewrites fun/method/old-property strings that some tests INTENTIONALLY hold in the
  OLD form: (i) the codemod's own corpus `tooling/snakecase/*_test.go` (`split_test.go`,
  `casing_test.go`, `recase_test.go`); (ii) NEGATIVE tests asserting an old form is REJECTED —
  `pkg/lint/property_form_test.go` (`TestOldFlatPropertyRejected` feeds an old flat property and
  expects a diagnostic; unwrapping its fixture to the new valid form defeats it). **Both are
  excluded by `split-migrate.sh`; the negative-test list will grow with Phase-4 hardening
  (any "old impl form → error" test).** Not bugs.

  **(B) Correct code, but golden / position / header assertions need coordinated update.**
  `semantic_golden_test.go` (token cols shift left — head `fun`→`=` is −2 chars; regen the
  golden), `nav_test.go:102` (hover now renders the `(= …)` impl — reconcile with decision
  #4: hover→SIGNATURE), etc. Expected atomic-flip churn.

  **(C) REAL integration gaps — literal-head consumers that must grow an `=` case.** These
  are the actual blocker, and they span BOTH domains:
  - `pkg/lint/typecheck.go` `checkFlow` (`case "fun":`/`case "method":` ~L1370) + the
    bound-member checker → **in-body type inference & bound checks skip `=` bodies**
    (`inbody_test.go`, `generics_bound_member_test.go` ×5). **SIBLING-DIRTY.**
  - ~~`pkg/core/eval.go` runtime stack-frame/span attribution~~ — **DISPROVEN (2026-07-01):**
    re-checked the migrated copy directly. `eval.go` handles `=` impls **perfectly** — named
    frames (`0: half`, `0: double`/`1: tally`) and correct body carets (`^^^^` under the erroring
    form). The `main_test.go` body-span + trace failures are pure **Category-B position-shift**:
    columns move −2 (`(fun half`→`(= half`) and the golden source line text changed. NOT a
    functional gap; `eval.go` needs no change. (`defineFunOrMethod` already passes `funName` to
    `BindFun`, exactly like the `fun` builtin.)
  - `pkg/modload/load.go` `isLibraryForm` → **`=` was NOT in the library allow-list**, so a
    top-level `(= name (params) body)` impl in a `.phl` was rejected as a side effect and
    SKIPPED (never evaluated). **✅ PRE-TAUGHT (2026-07-01):** `isLibraryForm` now accepts a
    4-child `=` (define); a 3-child `=` (reassignment) stays rejected. `liftDefinitions`
    already lifts `=` defines correctly. Tests: `pkg/modload/declimpl_test.go`.
  - annotation path (`annot_phl_test.go` ×5, `reload_test.go`) → **root cause was the SAME
    `isLibraryForm` gap**: `annot.phl` defines its ops as `(fun doc …)`/`(fun type …)` impls;
    migrated to `(= …)` they were skipped by the loader, so `doc`/`type`/… were undefined
    (`(type str)` then fell through to the `type` builtin → "requires a name and a type").
    **✅ RESOLVED by the modload fix — `pkg/annot` is now GREEN on migrated code, no annot-
    package change needed.**
  - `pkg/modload/reorder.go` lift ORDER is functionally correct for `=` (an impl lifts
    exactly where `method`/`fun` did); the remaining `reorder_test.go` failures are the
    `want []string{"method",…}` label arrays, which migrate together with their embedded
    source in the flip (Category B).
  - There are **~34 `case "fun"/"method"` / `head.Value == "fun"` dispatch sites in
    `pkg/lint` alone** — audit EACH for an `=` case (some, like `collectReturns`, must
    ADD `=` to the *skip* set — the nested-define leak, §Effects).

**Upshot: the integration is DONE and the flip is functionally READY (2026-07-01).** All 9
consumer gaps were APPLIED in the shared tree (user-authorized), plus a codemod fix; the real
tree stays green (edits are inert pre-flip) and a fresh isolated migration + `go test ./...`
leaves **ZERO functional failures** — every remaining migrated-copy failure is Category-B
(position/golden/label assertions that update *with* the flip). See the audit section below.
Sequence for the flip: (1) land Phase 0; (2) ~~teach the 9 consumers~~ **DONE**; (3) run
**`tooling/snakecase/split-migrate.sh`** (mechanical migration — correct file-set + exclusions
baked in; `-n` dry run; validated end-to-end); (4) regen goldens + fix position/header/label
assertions (the only remaining reds); (5) Phase 4 hardening + tree-sitter. **`pkg/modload` +
annot pre-taught; `eval.go` clear.**

## Consumer audit (2026-07-01) — the exact `=` integration punch-list

A 6-way parallel audit classified **102 literal-head decl dispatch sites**; **11 are functional
gaps** that miss the 4-child `(= …)` impl form (raw-head dispatch that bypasses `declOf`
normalization). Two roles: CONSUME (add `case "=":`) and SKIP-NESTED (add `=` to the skip-set, so
a nested `(= …)` impl's returns/locals/effects don't leak into the outer callable).

**✅ 3 gaps FIXED this session (my own Phase-1/2 code, additive, non-regressive):**
- `completion.go:432` `bodyScopeFor` recursion + `completion.go:479` `forBodyScope` — the NESTED-form
  selectors omitted `=` (the top-level `case "="` was already present). Added `case "=":` (declOf →
  fun/method) to both; regression test `nested_declimpl_completion_test.go`.
- **`decl.go` `defineFunOrMethod` (runtime) — the FUNCTION branch skipped discriminant handling.** The
  `fun` builtin (and `defineFunOrMethod`'s own METHOD branch) route `(disc X)` params through
  `discInfo`→`storeDiscFun`/`storeDiscMethod`, but the `=` FUNCTION branch did a plain `ctx.Declare`,
  so a migrated `(= conv ((disc X) x) …)` collapsed its overloads to one binding (dispatch returned
  the wrong impl — `TestDiscFunRuntime`). Added the `discInfo`→`storeDiscFun` mirror. **This was a
  RUNTIME gap the lint-focused audit could not see — surfaced only by the flip-runner's end-to-end
  migration** (a reminder that the audit + the migration test are complementary).

**✅ ALL 9 gaps APPLIED (2026-07-01, user-authorized, in the shared tree; real tree green — inert
pre-flip).** Line numbers below are as-audited (they shifted with sibling churn; edits were
re-located by content). Fixes as listed:

| file:line | fn | role | fix |
|---|---|---|---|
| `typecheck.go:1337` | `collectReturns` | skip-nested | `case "fun", "method", "=": return` |
| `typecheck.go:1390` | `checkFlow` `case "="` | consume-body | if `declOf` ok & `Body!=nil`, route to `checkBody` (bind params, swap body scope) then `return`; else fall to `checkAssignFlow` |
| `effects.go:230` | `collectLocalNames` | skip-nested | add `case "=": if len(br.Children)==4 { return }` |
| `effects.go:325` | `scanEffects` `case "="` | skip-nested | prepend `if len(v.Children)==4 { return }` (before the len==3 reassignment logic) |
| `effects.go:375` | `callableEffectLabel` | classify (hover) | gate off `declOf`'s `d.Head` not the raw leaf |
| `checkers.go:55` | `checkPhlSideEffects` | classify | after `libraryForms[head]`, add `if head=="=" && len(br.Children)==4 { continue }` (mirror of my `modload.isLibraryForm` fix; untested — all declimpl tests are `.pho`/ModeProgram) |
| `trait.go:118` | `addTraitMember` | consume-body | `case "method", "=":` (body layout identical); update error strings 121/203 |
| `trait.go:69` | `isTraitMemberForm` | classify | `case "method", "property", "static", "=":` (else a leading `(= …)` default is misread as the extends-list) |
| `typecheck.go:354` | `isTraitMemberNode` | classify | lint-side mirror of `trait.go:69` — same `=` addition (applied) |

**+ 1 CODEMOD fix (my `split.go`, surfaced by the post-integration migration).** The `-split` pass
was wrongly migrating trait sig-style REQUIREMENTS written with a lowercase-`self` receiver —
`(method self.area (self) Number)` → `(= …)` — because `isFunSigForm` (all-params-are-types)
doesn't recognize `self` as a type. Fixed: inside a trait body, a method whose RETURN slot is a
type is a requirement, left as `method` (mirrors the runtime's `!isTypeNode(br[3])` rule in
`addTraitMember`). Test `TestSplitTraitRequirementVsDefault`. This single fix resolved
`TestBoundBadMemberDetection`, `TestTraitBoundMethodAccess`, AND `TestDiscTraitSatisfaction`
(all were failing because the trait's requirement set lost `area`/`conv`).

**Post-integration result (fresh isolated migration + `go test ./...`): ZERO functional failures.**
The 9 consumer fixes resolved 6 migration failures; the codemod fix resolved 3 more. The only
remaining migrated-copy reds are **Category-B** (update *with* the flip, not code gaps):
`TestSemanticTokensGolden` (regen), `TestCLIDiagnostics` (caret/trace columns shift −N), `TestLift
DefinitionsStable` (`want [method]`→`[=]` label), `TestHoverShowsMutatesSelf` (VERIFIED position-
shift: `method`→`=` moves the name left 5 cols, so the test's hard-coded col lands past it; hover
at the real column works and shows mutates-self — the `callableEffectLabel` fix is confirmed good).

**Confirmed already-correct (route through `declOf`, do NOT re-check):** `declOf` itself, `walker.go`
(`checkBranch`/`collectOne`/`checkFun`/`checkMethod`), `imports.go`, `nav.go`, `semantic.go`,
`shape.go`, `scope.go`, `methodsig.go`, the runtime `=` builtin (`decl.go`), and `eval.go`/`dot.go`/
`typeval.go`/lsp (Kind-based, no decl-head dispatch). *(The earlier "eval.go is a gap" was WRONG:
verified against the migrated copy — `eval.go` gives `=` impls correct named frames + body carets;
the `main_test.go` failures are position-shift assertions, not a functional gap. `eval.go` is fully
clear.)*

**Note — sibling's `=` effect-marker scheme:** the sibling's (untracked) `effects.go` now splits the
marker — `!` for environmental effects (`missing-bang`), `=` for mutation (`missing-equals`, "must end
in `=`"). This overloads `=` (mutation-suffix on a NAME vs. the impl head); watch for interaction when
the two workstreams merge (e.g. `(= set= (self v) …)` — an impl of a mutating method named `set=`).

## Critical correctness points (from the critic — get these right)

1. **Dispatch ordering at `walker.go:956` is THE most critical point.** `checkAssign`
   hard-returns on `len != 3`, so a 4-child define reaching it is **silently dropped**
   (body never checked, no diagnostic). The `case "="` **must** route
   4-child+valid-shape to `checkFun`/`checkMethod` **before** `checkAssign` — this also
   prevents a false `set-on-constant` when a `(= add …)` name collides with a const.
   Regression-test both.
2. **Enumerate the 4 headIdent-driven consumers** (`nav.go:348`, `nav.go:679`,
   `semantic.go` dispatch, `completion.go:49`). declOf normalization does NOT reach
   them; without an explicit `=` case each, go-to-def works while outline/hover/param-
   highlight are silently blank — the "partial migration" trap.
3. **Effects nested-`=` leak** (§Effects) — a real cross-surface bug.
4. **Route bare-ident `=` → `checkFun`, dotted `=` → `checkMethod` strictly**;
   `checkMethod`'s anonymous-else branch would reference-check a function name as a
   receiver type otherwise.
5. **Bind the `=` impl's `DefFun`/`DefMethod` into PACKAGE scope** (like the old impl)
   so cross-file sig↔impl pairing (`checkMissingImpls`) survives.

## Phased rollout — TOLERANT then ATOMIC (stay green)

- **Phase 0 — prerequisite (blocking):** the sibling's template/generics + inline-sig +
  gradual-checker + effects work merges to `main`. Re-run the census against the merged
  tree (sig counts + the 4 function-type forms come from their uncommitted files).
- **Phase 1 — lint + editor (tolerant):** declOf `=`-arm + forced `IsSig`; the explicit
  `=` cases in the 4 headIdent consumers; dispatch ordering; shape.go arity relax.
  **Accept BOTH** old impl-shaped `fun`/`method` (still binding) AND new `=`. Merge
  separately, keep green.
- **Phase 2 — runtime (tolerant):** arity-overload the `=` builtin; keep old
  `fun`/`method` impls working. Merge separately, keep green.
- **Phase 3 — codemod (atomic flip, part 1):** run the head-swap codemod across
  `script/std`, `pkg/builtins/pho`, `testdata`, `pkg/**/*_test.go` (+ decide `tooling/…
  examples`); hand-migrate the 24 anonymous funs / property delegates to `&`-blocks in
  the same commit.
- **Phase 4 — intolerant hardening + tree-sitter (atomic flip, part 2, same commit):**
  make old impl-shaped `fun`/`method` an **error** (runtime + lint); simplify the
  empty-param strict branch; add the tree-sitter query patterns + corpus + deploy;
  wire the effects nested-`=` skip.

> Making Phase 1/2 intolerant (old-impl = error) **before** the codemod turns the tree
> red — exactly the trap the prior snake_case/dequote cutovers avoided.

## Diagnostics to add
- Near-miss **bodyless define**: a 3-child `(= f (a b))` where `f` is undefined and
  child2 is a param-list-shaped call → hint "a definition needs a body: (= f (a b) body)"
  instead of "f is not defined".
- 5+-child `=` arity handling in `shape.go`.

## Resolved policy (rulings — all decisions locked, 2026-07-01)
- **ALL anonymous functions and methods removed.** `fun`/`method` are two-state
  (sig / error→`=`).
- **Property get/set** use the FINAL parenthesized `(get (params) body)` /
  `(set (params) body)` sub-forms (user-specified); target reuses the typed
  `(Type name)`/`(Type Owner.name)`/`Owner.name` shape. `static property` migrates in
  lockstep.
- **Trait bodies** follow the same split (method sigs stay `method`, named impls →
  `=`); trait *requirement* flags `(property self.name get)` stay bare.
- **`tooling/…/examples/*`** migrated fully (incl. older stale syntax).
- **Go-to-def / hover lands on the SIGNATURE**; **jump-to-implementation** (LSP
  `textDocument/implementation`) added, routing methods through `structInfo`.
- **declOf Head-normalization** (Head=`fun`/`method` for `=`-impls; `d.Branch` keeps
  the `=` form).
- **Preserve** `isFunSigForm`/`looksLikeTypePNode` through the codemod.
- **Tolerant-then-atomic** sequencing.

## Remaining open questions
None blocking — all design decisions are locked. Two items to re-verify at Phase 0
(after the sibling's work merges), not decisions:
- **Re-baseline the census** against the merged tree (`typedprop_test.go` and the
  template/sig/effects files are untracked today; the sig counts + property-delegate
  count will shift).
- **Confirm the tree-sitter deploy path** for query-only changes across the two
  `zed-pho` query dirs (`languages/pho/` + `grammars/pho/queries/`).
