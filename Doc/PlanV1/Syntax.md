# Syntax overhaul: casing, visibility, declarations, collections

Status: **active**. Replaces the capitalization-based public/private rule and
the implicit type-name rules with an explicit, strictly-enforced scheme.

This is a **hard cutover**, run the same way as the prior `desigiling` and
`dequote` migrations: build the new reader/runtime/tooling tolerant of both
old and new for the duration, run a codemod over every source, then flip on
the hard rejection and green the suite.

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
6. **Reassignment is `(name = value)`** (and `(obj.field = value)`,
   `(obj.[i] = value)`). The standalone `(= target val)` builtin is removed.
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
| Reassign           | `(= x v)`                        | `(x = v)`                                    |
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

### Phase 3 — `let` / `let var` + `(name = value)`
Landed as **parse-time sugar** in `pkg/syntax/positioned.go`, so the runtime and
the *entire* linter keep seeing the existing `const`/`var`/prefix-`=` forms — no
runtime builtin and no lint fan-out during the migration.
- `rewriteLet`: `(let [var] name = value …)` → `(const name value …)` /
  `(var name value …)` (strips the `var` modifier and the `=` markers). **Done.**
- `rewriteInfixAssign`: `(lhs = rhs)` → `(= lhs rhs)` (fires only when the 2nd
  element is a bare `=`, so prefix `(= …)` and `(let x = …)` are untouched).
  Handles bare-name and `obj.field`/`obj.[i]` targets via the existing `=`
  builtin. **Done.**
- Tests: `pkg/syntax/letassign_phase3_test.go` (rewrite shape),
  `pkg/builtins/let_phase3_test.go` (runtime), `pkg/lint/let_phase3_test.go`
  (const/var distinction + set-on-constant survive the desugar). **Done.**

Why sugar, not first-class: the const/var consumers (`semVarConst`,
`walker.go` check pass, `shape.go`, `infer.go assignDeclShapes`, `nav.go`) read
raw children as name/value **pairs**, so a `let` triple-shape form can't be fed
to them directly. Normalizing at the parser is the low-risk tolerant bridge.

Note (deviation from the original sketch): reassignment reuses the existing `=`
builtin via the prefix rewrite — no new `Assign` mangled head is introduced.

Deferred to the flip:
- Make `let`/`let var` first-class (real builtin + the lint fan-out above:
  `decl.go`, `scope.go` `DefConst`/`DefVar` + builtin names, `checkers.go`,
  `semantic.go`, `nav.go`, `typecheck.go`, `shape.go`, `infer.go`, `decls.go`
  `declOf`, `modload/load.go`+`reorder.go`) so tooling labels them `let`, not
  `const`/`var`.
- Remove `const`/`var` and the standalone prefix-`=` form.
- tree-sitter `let`/`var`/`=` rules — Phase 8.

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

**⚠ Critical remaining gap (blocks migration):** struct-INIT field keys
(`Process.{ pid 0 }`) and typed-struct-DECL field names become **string
literals** at parse time (the `.{` sugar quotes them), so `Recase` skips them —
leaving construction `Process.{ pid 0 }` mismatched against the recased field
decl `#pid`. Must recase these (a token-level `.{` pass) before any migration,
or struct construction breaks at runtime. Minor gaps: param/local shadowing of a
top-level private name; `property`/`static` member-name decls.

**Migration verdict — NOT ready to run on the live tree:**
1. The struct-init field-key gap above would break struct construction.
2. The concurrent sibling agent is still editing `pkg/lint` + `*.phl` (tree is
   red from its `~sig` refactor); a codemod run would collide.
3. The flip itself (StrictNames on, `{}`→`Brace`, struct-decl reorder, drop old
   syntax) is not built — casing alone doesn't complete the cutover.

**Apply surface (when executed):** `script/std/**/*.phl`,
`pkg/builtins/pho/*.phl`, `testdata/**/*.pho`,
`tooling/tree-sitter-pho/examples/*`, and the **84 `*_test.go` files** with
embedded Pho (via `-go`) — the largest surface.

**Blockers before executing the cutover:**
1. **A concurrent sibling agent** is actively editing `pkg/lint` and
   `pkg/builtins/pho/*.phl`. A full-tree codemod would collide with in-flight
   edits. The cutover must wait until the tree is quiet.
2. The casing reclassification (above) must be solved first.
3. The flip is all-or-nothing — the suite breaks until every source is migrated,
   so it needs `StrictNames`-off → migrate → flip done as one reviewed sequence.

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
- `=` overloading: `(let x = v)` vs `(x = v)` vs `(obj.f = v)` distinguished
  purely by position. Needs tight parser tests.
- Empty-collection ambiguity: `[]`=list, `[->]`=map; inspect must round-trip both.
- The reclassification name-map (Phase 7) is genuine design labor.
- Migration blast radius: 157 test files; budget real time for hand-fixing.
