# Syntax overhaul: casing, visibility, declarations, collections

Status: **DONE** — the cutover is merged to `main` and the whole suite is green.
Replaces the capitalization-based public/private rule and the implicit type-name
rules with an explicit `#`-prefix visibility + snake_case/Title_Snake_Case scheme.

The cutover ran the same way as the prior `desigiling`/`dequote` migrations:
tolerant phases first (1–6, additive), then the codemod (`tooling/snakecase`)
migrated every `.phl`/`.pho` + the embedded Pho in `*_test.go`, the runtime/lint
flipped to pure-`#` visibility, and the suite was greened (155 → 0 failures) via
two fan-out triage/fix workflows. Merged at commit `7c4f47c`.

**What's live on `main`:** snake_case values, Title_Snake_Case types, `#`-prefix
privacy (`self.#pid`), `none`/`true`/`false`, `let`/`let var` + `=`, `[k -> v]`
maps, `self` receiver, struct `.{ field = val }` init.

**Remaining (optional, non-blocking — main is green):**
- **Phase 8 tree-sitter grammar/corpus** still encodes the OLD surface
  (grammar.js, corpus, most of highlights). Only the `atomName`→`atom_name`
  builtin-name was synced (for the subset-of-lint test). Zed editor highlighting
  of new-syntax files lags until the grammar is migrated + SHA-bumped.
- **`StrictNames` is still off** and the runtime/lint still *tolerate* the old
  forms (const/var, Nil/True/False, `{k v}` maps). Nothing uses them; flipping
  `StrictNames` on + removing old-form acceptance is a future hardening step.
- **`tooling/snakecase` codemod** still has a few known bugs the triage surfaced
  (dot-headed construction keys, sig-receiver `Self` lowering, double-migration
  of already-new sources) — fixed in the affected test SOURCES directly, not all
  in the codemod. The codemod is a one-shot tool (not re-run), so this is
  posterity-only. The `##` double-private and `NilT`/builtin-preservation and
  scope/keepPublic/file-wide-types fixes ARE in the codemod.

---

## 1. The rules

1. **All value identifiers are snake_case.** `my_var`, `is_string?`, `parse_2`.
2. **Type identifiers are Title_Snake_Case.** Each underscore-separated word is
   Title-case (first letter upper, rest lower): `Type_Name`, `Integer`,
   `My_Struct`, `Num`. (PascalCase `TypeName` and acronyms `HTTP` are **not**
   valid — use `Type_Name`, `Http`.)
3. **Private members are prefixed with `#`** — at every level, including the top
   level. `#secret`, `#Secret_Type`. The `#` is part of the name token.
   This replaces capitalization as the visibility signal.
4. **`none` / `true` / `false` are lowercase keywords.** `none` replaces `Nil`;
   `Nil`/`True`/`False` are removed from the surface syntax.
5. **`const` → `let`, `var` → `let var`, both gain `=`:**
   `(let x = v)` (immutable), `(let var x = v)` (mutable).
6. **Reassignment is the prefix `(= target value)`** (and `(= obj.field value)`,
   `(= obj.[i] value)`). The infix `(name = value)` spelling is NOT used — `=`
   appears infix only as the binding marker inside `let` / struct-init.
7. **Struct declarations** drop the `.{` dot-form and go **type → field** inside
   plain braces: `(struct My_Struct { Integer field_name  Float #priv })`.
8. **Struct initialization** gains `=` per field:
   `My_Struct.{ field_name = 1  #priv = 2 }`.
9. **Maps move to `[]` with `->` arrows:** `[ :key -> :value  :other -> :v2 ]`.
10. **Enforcement is at the reader.** Non-conforming names fail to parse/load
    anywhere (runtime and tooling both refuse them).

### Old → new at a glance

| Feature            | Old                              | New                                         |
|--------------------|----------------------------------|---------------------------------------------|
| Value ident        | `myVar` / `my_var` (free)        | `my_var` — `#?[a-z][a-z0-9_]*\??`           |
| Type ident         | `MyType` (`^[A-Z]` heuristic)    | `My_Type` — `#?[A-Z][a-z0-9]*(_[A-Z][a-z0-9]*)*` |
| Private            | lowercase / capitalization       | `#` prefix                                  |
| Nil / bools        | `Nil` `True` `False`             | `none` `true` `false`                       |
| Const / var        | `(const x v)` `(var x v)`        | `(let x = v)` `(let var x = v)`             |
| Reassign           | `(= x v)`                        | `(= x v)` (prefix; unchanged)                |
| List               | `[a b c]` → `(Slice …)`          | `[a b c]` → `(Slice …)` (unchanged)         |
| Map                | `{k v …}` → `(Map …)`            | `[k -> v  …]` → `(Map …)`                    |
| Empty list / map   | `[]` / `{}`                      | `[]` / `[->]`                               |
| Struct decl        | `(struct P.{ F Type … })`        | `(struct P { Type field … })`               |
| Struct init        | `P.{ field val … }`              | `P.{ field = val … }`                       |
| Brace `{…}` alone  | dict literal                     | only a struct/type body (else error)        |
| Receiver           | `Self`                           | `self`                                      |

### Non-collisions (verified against the current lexer/parser)

- **Indexing/slicing is `xs.[i]` / `xs.[1:2]`** — a `.` then `[…]`, using `:` as
  the range separator. It never contains `->`, so the map reuse of `[]` does not
  touch it.
- **Blocks use the `&` sigil**, not `{}`. Freeing `{}` from maps is clean.
- **`->` is only used in real source inside type-sig annotations today**
  (`--@ (~methodsig … -> o)`), and lexes as two tokens `-` `>`. The single-token
  `->` lands in Phase 4 with maps, reconciling that one annotation.

---

## 2. Internal representation (mangled heads)

Existing mangled heads (`pkg/core/mangle.go`): `Dot, Do, Strinterp, Strcoerce,
Macrocall, Slice, Map`.

- **Keep** `Slice` (lists) and `Map` (maps); only their *surface* lowering moves
  (`[]` + `->` decides list vs map).
- **Add** `Brace` — what a `{…}` body lowers to (struct/type declarations). Bare
  `Brace` outside a struct/type form is an error.
- **Add** `Assign` — what `(lhs = rhs)` lowers to; replaces the deleted `=`
  builtin. Carries the const-vs-mut check and the read-only-from-outside guard.

Every new head runs the full mangled-builtins checklist: `mangle.go` → `lower.go`
→ `sugar.go` quote round-trip → `inspect.go` un-mangle → register builtin → lint
(`builtinNames` + `builtins_drift_test` skip + `infer`) → tree-sitter.

---

## 3. Phase plan

### Phase 0 — Spec doc *(this file)*

### Phase 1 — Lexer/reader (`pkg/syntax/positioned.go`, `pkg/syntax/idents.go`)
- `#`-leading identifier lexing (additive: `#` was previously an unrecognized
  character). **Done.**
- `classifyIdent` → `IdentValue` / `IdentType` / `IdentInvalid`, with `#`/`?`
  stripping. **Done** (`pkg/syntax/idents.go`, fully unit-tested).
- A `StrictNames` flag (default **off**) that, when on, makes the lexer emit a
  `ParseError` for any non-conforming identifier. This is the hard-reject
  mechanism, dormant until the codemod flip. **Done.**
- Deferred to Phase 4: the single-token `->` (needed by maps; entangled with the
  one `~methodsig` annotation).

### Phase 2 — Literals `none/true/false`
Landed **tolerantly**: the new spellings are recognized everywhere the old ones
are, and `Nil/True/False` keep working until the flip.
- `pkg/core/eval.go:213` — `none`→`TvNil`, `true`/`false`→`TvBool` (alongside the
  capitalized forms). **Done.**
- Lint value-literal recognition: `scope.go` seed list, `typecheck.go` (`litType`
  + `inferType`), `infer.go` shape, `semantic.go` keyword map, and the
  `builtins_drift_test` soft-keyword allowlist. **Done.** (`decls.go`/`walker.go`
  capitalization heuristics need no change — the new spellings are lowercase.)
- End-to-end test: `pkg/builtins/literals_phase2_test.go`. **Done.**

Deferred to the flip / Phase 8 (would break golden + grammar tests if done now):
- `pkg/core/inspect.go` + `core.Stringify` — switch rendering to `none/true/
  false`. (Internal `TvNil` value name stays; only surface spelling changes.)
- `pkg/core/phtype.go` type-name display (the `Nil` type, bool singletons) —
  part of the Phase 5/6 type-naming work.
- Drop `Nil/True/False` acceptance from `eval.go`/lint.
- tree-sitter `nil`/`bool` rules (`'Nil'` → `'none'`, `True/False` → `true/
  false`) — Phase 8.

### Phase 3 — `let` / `let var` + reassignment
**`let`/`let var` are now FIRST-CLASS** (the parse-time `rewriteLet` sugar was
removed); **reassignment is the prefix `(= target value)`** (the `rewriteInfixAssign`
sugar that accepted `(name = value)` was removed). Both rewrites are gone from
`pkg/syntax/positioned.go`.
- Runtime: a real `let` builtin (`declareLet` in `pkg/builtins/decl.go`) parses the
  optional `var` modifier + the `name = value` triples and binds each name —
  immutable for `let`, mutable for `let var` — reusing the var/const Rebind logic.
- Lint fan-out (so the AST keeps `let` and tooling labels/highlights it): `declOf`
  (`decls.go`) normalizes a `let` form to a const/var `Head` + `Binds` while keeping
  `d.Branch` as the `let` form for hover/symbols; raw-head consumers got `let`
  awareness — `semantic.go` (`semLet` + keyword), `shape.go` (triple-arity check),
  `infer.go`/`walker.go` (route through `declOf`), `nav.go` (find/render/symbols),
  `typecheck.go` (`checkInlineTypedBinds` handles the triple layout for typed
  binds), plus the allow-lists/predicate `scope.go` builtinNames, `checkers.go` +
  `modload/load.go` libraryForms, `modload/reorder.go` `isVarConst`.
- Reassignment is prefix-only and goes straight to the existing `=` builtin — no
  rewrite, no new mangled head. `=` still appears infix solely as the binding
  marker inside `let` and struct-init.
- Tests: `pkg/syntax/letassign_phase3_test.go` (forms pass through unchanged),
  `pkg/builtins/let_phase3_test.go` (runtime), `pkg/lint/let_phase3_test.go`
  (const/var distinction + set-on-constant). **Done — suite green.**

`const`/`var` and `Nil`/`True`/`False` stay accepted (tolerant) until `StrictNames`
is flipped on; nothing in-repo uses them. tree-sitter `let`/`var`/`=` highlighting
is Phase 8.

### Phase 4 — Bracket reshuffle (maps→`[]`, body→`{}`) + `->` token
Split into 4a (landed, additive/tolerant) and 4b (deferred to the flip, because
`{}` cannot mean both map and struct-body at once — a genuine hard swap that
needs the codemod).

**Phase 4a — landed:**
- `->` single token (`positioned.go` multi-char operators). Verified safe: no
  live code splits the old `-`/`>` pair — the modern `sig` macro uses
  parenthesized lists and the `~methodsig … -> o` annotation is tolerated
  unresolved. Stale `annot.phl` comment corrected. **Done.**
- `[k -> v]` → `(Map …)` (`lower.go`, `bracketIsMap`): a `[…]` with `->` lowers
  to a map (arrows dropped); arrow-free `[…]` stays a list; `[]`→empty list,
  `[->]`→empty map. The linter already ignores `->` (`looksLikeIdentifier`
  rejects it), so no lint change. **Done.**
- Struct init `.{ field = value }` (`positioned.go` `.{` rewrite,
  `structInitHasEq`/`quoteFieldKey`): accepts the new `=` triple form *and* the
  old pair form; `#field` keys accepted (`isBareWord` extended). **Done.**
- Tests: `pkg/syntax/brackets_phase4_test.go`, `pkg/builtins/brackets_phase4_test.go`.

During 4a, `{k v}` still lowers to `(Map …)` (so both map syntaxes coexist), and
`inspect`/`Stringify` still render maps as `{k v}` — a `[k -> v]` map round-trips
to `{k v}` transitionally.

**Phase 4b — deferred to the flip (needs the codemod):**
- `{…}` → new `Brace` mangled head (struct/type body); drop `{…}`-as-map.
- `structDeclShape` (`decl.go:150`): read the `Brace` body as **type→field**
  alternation (replaces the `.{` field-first typed form); `#` private fields.
- `inspect.go`: render maps as `[k -> v]`, struct body `{ Type f }`, init
  `.{ f = v }`; lint `builtinNames`/drift/`infer` for the `Brace` head.
- Migrate all `{k v}` map literals → `[k -> v]` (codemod).

### Phase 5 — Visibility via `#` (replace capitalization)
Capitalization currently does double duty (visibility *and* type-ness), so the
tolerant move is a **superset**: keep capitalization-based visibility and add
`#`-recognition on top (no existing code uses `#`).
- Key enabler: `scope.go` `identRe` → `^#?[A-Za-z][A-Za-z0-9_]*\??$` so the
  linter recognizes `#name` as an identifier at all (otherwise `#`-bindings/
  fields/refs are invisible). **Done.**
- `member.go` privacy → private if lowercase **or** `#`-prefixed; message
  updated. **Done.**
- `completion.go` visibility filter → also hide `#`-prefixed (`isHashPrivate`).
  **Done.**
- No change needed for exports/runtime: `exported()` (lint) and the runtime
  `pkg.Exports` filter (`modload/load.go:366`) both use `IsUpper(name[0])`, and
  `IsUpper('#')` is false — so `#`-names are **already** non-exported/private,
  cross-module hiding included. **Verified.**
- Tests: `pkg/lint/hashprivate_phase5_test.go`.

Deferred to the flip: switch `exported()`/member/completion to the **pure** `#`
rule (drop capitalization); rework `flagCapitalizedParam`; `Self`→`self`.

### Phase 6 — Type-name semantics in linter
- The existing `^[A-Z]` heuristic (`looksLikeTypePNode`, `decls.go:94`) already
  classifies Title_Snake_Case names as types (they're uppercase-leading); only
  added a leading-`#` strip so a private type `#Type_Name` is still a type.
  **Done.**

Deferred to the flip: replace the heuristic with the strict Title_Snake rule and
add value-where-type / type-where-value diagnostics (false-positive risk on
un-migrated code; the reader's `StrictNames` already covers the hard rejection).

### Phase 7 — Codemod + migrate all sources (bulk of the work)

**Built — `tooling/snakecase/main.go`** (mirrors `tooling/requote`: real
lexer+parser, format-preserving, refuses on lex/parse errors, `-go`/`-n` modes).
Covers the **mechanical, behavior-preserving** transforms (the ones Phases 1-6
already accept tolerantly, so applying them keeps the suite green):
`Nil/True/False`→`none/true/false`, `Self`→`self`, `(const n v …)`→`(let n = v
…)`, `(var n v …)`→`(let var n = v …)`, `{k v}`→`[k -> v]`. Unit-tested
(`tooling/snakecase/main_test.go`, 14 cases incl. nesting). Dry-run over
stdlib+testdata = **78 edits**; correctly refuses the two malformed fixtures
(`parse_err.pho`, `badlib/bad.phl`). **Not yet applied to the live tree** (see
blockers below).

**Casing reclassification — foundation built** (`tooling/snakecase/casing.go`,
tested in `casing_test.go`):
- Pure transforms: `splitWords` (camelCase/PascalCase/snake/acronym aware),
  `toSnakeCase` (values), `toTitleSnake` (types) — both preserve a leading `#`
  and trailing `?`. **Done.**
- Classifier: `collectTypeNames` (struct/type/trait decls + the builtin-type
  set) and `collectTopLevelValues` (const/var/fun + visibility). **Done.**
- Decision layer: `buildRenameMap` — each declared name → new spelling
  (type→Title_Snake; value→snake_case, `#`-prefixed when it was private-by-
  lowercase; builtins and no-ops dropped). **Done.**

**Casing reclassification — occurrence rewriter built** (`tooling/snakecase/
recase.go` + `-recase` mode, tested in `recase_test.go`):
- `recaseLeaf` (general identifiers via the package map → snake/Title) and
  `recaseMember` (members via their own capitalization: public→snake,
  private→`#`+snake, type stays Title). Struct field decls use the member rule.
- `buildGlobalMaps` unions the rename map + type set across **all input files**,
  so cross-file/cross-module references stay consistent. `-recase` runs the
  two-pass pipeline (mechanical Transform → casing Recase). **Done.**
- **Demonstrated on the full stdlib** (temp copy, not the live tree): 260 edits;
  `self.pid`→`self.#pid` consistent with the `#pid` field decl;
  `ThisProcess`→`this_process`; `Process.Cancel`→`Process.cancel`; and — only
  when the map spans the import closure — `io.Writer` correctly **stays**
  `io.Writer` (snaked to `io.writer` when `io` was absent — confirms the closure
  requirement).

**Gaps fixed (`tooling/snakecase`, all tested):** the struct-init/typed-decl
field-key pass (`recaseConstruction`), goimport-member skip (`dep.PctlSpawn`
stays — Go-module exports), `.phl`/`.pho` privacy (`#` only in libraries), and a
`-migrate` driver + `-go` casing that share ONE package-wide map across `.phl`/
`.pho` AND Pho embedded in `.go`. Recover-and-skip guards panicking snippets.

**Full cutover attempted in an isolated worktree** (branch `syntax-cutover`,
commit df2acd2; main stays green at the checkpoint): `-migrate` applied **1008
edits across 108 files** (all `.phl`/`.pho` + the embedded Pho in `*_test.go`)
and the visibility rule was flipped to pure-`#` (lint `exported()`,
`member.go`, `completion.go`; runtime `isExportedMember`, `modload/load.go`).
Pho sources + embedded test Pho migrated cleanly. Result: **155 test failures**,
two systematic root causes — **the migration is a THREE-surface coupled
operation, and only two surfaces were migratable by the codemod:**

1. **Go-side Pho-facing name registrations are a third surface.** Methods like
   `Is?` are registered Go-native in `builtinmod.go` (`AddMethod(…, "Is?", …)`),
   not in `.phl`. The codemod migrated Pho refs `.Is?`→`.is?`, but the Go
   string `"Is?"` was untouched → `.is?` resolves to nil, cascading through the
   universal object model (`<nil>` everywhere). Fix: migrate the Pho-facing
   name strings in Go runtime code (`builtinmod.go`, `typeval.go`, the
   `Nil/True/False` rendering in `inspect.go`, builtin name tables) in lockstep.
2. **Scope-aware shadowing.** A local param that collides with a global private
   name is wrongly `#`-prefixed: param `type` (in `(method Unknown.to (self
   type) …)`) became `#type` because `annot.phl` has `(fun type …)` in the
   global map. Fix: `Recase` must track param/let bindings per scope and never
   apply the global map to a locally-bound name.

**Verdict:** the codemod cleanly migrates surfaces 1–2 (Pho + embedded test
Pho); completing the cutover additionally needs (a) migrating the Go-side
Pho-facing name registrations and (b) scope-aware shadowing in `Recase`, then
grinding the residual failures. This is a multi-session effort, not a single
codemod run. The WIP lives on `syntax-cutover` for iteration; main is the clean
checkpoint.

Annotation-vocabulary caveat (found earlier, still relevant): `annot.phl`'s
`~doc`/`~flag`/`~sig` functions are lowercase-but-public protocol names — a
PARTIAL migration breaks them; the cutover must be whole-tree and atomic.

### Phase 8 — tree-sitter + Zed + LSP
- `grammar.js`: value-vs-type ident + `#`, `none/true/false`, `->`, `[]`
  map-vs-list, `{}` struct body, `.{ f = v }`, `let`/`var`.
- Rewrite all 74 corpus tests; keep 74/74 green.
- Update both synced copies of `highlights/locals/outline/indents/folds.scm` +
  `brackets.scm`; commit tree-sitter, bump SHA in `extension.toml`, rebuild
  extension (node/grammar skew kills the language).
- LSP hover/completion/semantic-token spelling + `#` visibility.

### Phase 9 — Verify
`go test ./...`, lint golden/drift suites, `tree-sitter test` (74/74), and a
couple of end-to-end `.pho` runs.

---

## 4. Sequencing & risks

**Cutover order:** land Phases 1–6 with the reader transiently tolerant
(`StrictNames` off, old heads still accepted) → run the Phase 7 codemod → flip
`StrictNames` on and remove old-syntax acceptance → green the suite. Phase 8
deploy (SHA bump → Zed rebuild) is the manual tail.

**Top risks**
- `=` roles: the binding marker in `(let x = v)` / struct-init `T.{ f = v }`
  vs the prefix reassignment builtin `(= x v)` / `(= obj.f v)`. Distinguished by
  position (marker is infix inside a decl; the builtin is the head). Resolved:
  reassignment is prefix-only, so there is no infix `(x = v)` to disambiguate.
- Empty-collection ambiguity: `[]`=list, `[->]`=map; inspect must round-trip both.
- The reclassification name-map (Phase 7) is genuine design labor.
- Migration blast radius: 157 test files; budget real time for hand-fixing.
