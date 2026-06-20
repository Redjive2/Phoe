# v1 Plan — Universal Object Model & Import-Scoped Methods

Status: **proposed** · Owner: language core · Scope: `pkg/core`, `pkg/builtins`,
`pkg/goop`, `pkg/modload`, `pkg/lint`, `cmd/pho-lsp`, `script/`, `Doc/`

> This plan is the **substrate** for a **set-theoretic gradual type system**
> ([GradualTyping.md](GradualTyping.md)), which layers the type algebra, the
> gradual `Dynamic` type, and the static checker on top of the object model
> defined here. The design is deliberately structured so that types are
> first-class values with stable identities and an explicit top type
> (`Unknown`), and so that method visibility is a lexical/import property the
> type checker can reason about statically. See §13 — and
> [Coordination.md](Coordination.md), which reconciles the two plans into a
> single execution track (it supersedes a few details here: `TypeValue` becomes a
> `PhoType` descriptor + `TypeID`-keyed registry, `==` compares interned pointers,
> and `MethodEntry` carries a `Sig`. `methodsig!` stays as specified in §4.5).

## 1. Goal

Make **every Pho value an object**: any value — primitives (`Number`, `String`,
`List`, `Dict`, `Boolean`, `Char`, `Atom`, `Function`, `Nil`) and struct
instances alike — can carry **methods and properties**. Members may be defined in
the type's own module or **attached as extensions from another module**, and
extensions are **import-scoped**: a call site sees an extension only if it
imports the module that declares it. Externally-attached methods are
**non-privileged** (never reach a struct's private internals).

This realizes the "Extended object model" and "Some important builtin methods"
sections of [Doc/Features.md](../../Doc/Features.md), and replaces the free
functions `(len x)` / `(keyof x)` with `(x.Size)` / `(x.Keys)`.

## 2. Locked decisions

1. **`len` and `keyof` are deleted outright.** No aliases, no deprecation — the
   language is pre-stability. All call sites migrate to `.Size` / `.Keys`.
2. **Built-in methods live in an auto-loaded Pho module** at `pkg/builtins/pho/`
   (`.phl` files) loaded by the runtime at startup. Its members are **always in
   scope** (true built-ins, exempt from import-scoping). They are **thin bindings
   over Go**: primitive operations are implemented in Go and exposed via
   `goop`/`goimport`, with the `.phl` wiring them onto the type as methods —
   exactly the pattern the stdlib already uses
   ([pctl.phl](../../script/std/pctl/pctl.phl)). Primitive types are implemented
   in as close to pure Go as possible.
3. **Name clashes are forbidden.** A member resolving to two different
   definitions in scope, redefining a built-in member, or declaring the same
   member twice in one package is a **hard error** — caught by tooling (linter)
   first, with a runtime error as the backstop.
4. **`KindConstructor` is removed; `KindType` represents all types.** A struct's
   name evaluates to a `KindType` value (carrying its constructor); primitives
   have built-in `KindType` values. `typeof` always returns a `KindType`.
5. **Methods are import-scoped, and exported extensions are origin-restricted.**
   - To call an extension method on a value, the call site's file must import the
     module that declares it (see the example in §5).
   - A **capitalized (exported)** extension on type `T` may only be declared in
     `T`'s own module, a **sibling** module, or a **child/descendant** module.
     A capitalized extension on `T` anywhere else is a hard error (§7).

## 3. Why import-scoped, not global

Each module gets its **own fresh `Env`** — `pkgEnv := EnvFactory()`
([pkg/modload/load.go:224](../../pkg/modload/load.go)). Today methods live only on
`tstruct.Methods` ([pkg/core/types.go:86](../../pkg/core/types.go)) and dispatch
only through the `KindInstance` branch of the dot accessor
([pkg/builtins/dot.go:130](../../pkg/builtins/dot.go)) — globally reachable from
any instance.

The corrected model is the opposite of a global registry: **methods belong to the
package that declares them**, and a call site resolves a member only through its
**import scope** (own package + built-ins + directly-imported packages'
*exported* extensions). This is what makes `r.ReadAll` fail in a file that
imports only `reader` but succeed in one that also imports `readerExt` (§5).
Imports are per-file (`ctx.File.Imports`,
[pkg/core/types.go:66](../../pkg/core/types.go)), so resolution is per-file.

## 4. Target architecture

### 4.1 Unified type values (`pkg/core`)

```go
const KindType = "type"          // replaces KindConstructor

// TypeID is a stable, set-theory-friendly identity for a type.
//   primitive: "prim:num", "prim:str", "prim:list", "prim:dict",
//              "prim:bool", "prim:chr", "prim:atom", "prim:fun", "prim:nil"
//   top:       "unknown"
//   struct:    "<declaring-pkg-path>#<Name>"   e.g. "reader#Reader"
type TypeID string

// TypeValue is the runtime representation of a type (KindType's Val).
type TypeValue struct {
    ID          TypeID
    Name        string      // display name ("Number", "Reader")
    OriginPath  string      // declaring package path ("" / "root" for primitives)
    Struct      *tstruct    // non-nil for struct types; carries field layout
    Constructor tfun        // non-nil for constructible (struct) types
}
```

- `(struct Reader …)` binds `Reader` to a `KindType` value with
  `ID = "<pkg>#Reader"`, the field layout, and a constructor closure. The
  `Reader.{ field value … }` sugar and `eval.go`'s call path construct through it.
- Primitives have built-in `KindType` values bound globally (`Number`, …,
  `Unknown`).
- `typeof v` returns the `KindType` for `v` (primitive descriptor, or the
  instance's struct type value).
- **Equality**: two `KindType` values are equal iff their `ID`s match. Extend the
  `==` implementation accordingly. (`(typeof 1) == Number` → `True`.)

### 4.2 Per-package method/property tables (`pkg/core`)

Methods and properties no longer live on `tstruct`; they live on the package that
**declares** them, keyed by the target type's `TypeID`:

```go
type tpackage struct {
    // …existing fields…
    Methods    map[TypeID]map[string]MethodEntry  // declared in THIS package
    Properties map[TypeID]map[string]Property
}

type MethodEntry struct {
    Fun        tfun
    Privileged bool   // may touch the receiver's private fields (§4.4)
    Exported   bool   // capitalized name → visible to importers
    NameSpan   Span   // for tooling / clash diagnostics
}
```

- A struct's **own** methods (e.g. `Reader.Read` in `reader`) are entries in
  `reader`'s table under `reader#Reader` — they are just exported extensions
  declared in the type's own module.
- An **extension** (e.g. `reader.Reader.ReadAll` declared in `readerExt`) is an
  entry in `readerExt`'s table under `reader#Reader`.
- Primitive extensions key under `prim:num`, etc.; universal extensions under
  `unknown`.

The **built-in module** (§4.5) has its own table whose entries are always in
scope.

### 4.3 Import-scoped dot dispatch (`pkg/builtins/dot.go`)

Dispatch splits by RHS shape (as the linter already reasons):

| RHS form | Meaning |
|---|---|
| **bracket** `coll.[i]`, `coll.[a:b]` | index / slice (unchanged) |
| **numeric leaf on a number** `1.5` | fractional-decimal hack (unchanged) |
| **bare identifier** `x.Name` | **member lookup** (universal, import-scoped) |

For `x.Name` resolve in this order, collecting **all** matches across visible
sources to detect clashes:

1. **Instance field** (`inst.Fields[Name]`) — instance data, with the existing
   lowercase privacy rule. (Fields short-circuit; they cannot clash with methods.)
2. **Built-in members** for `typeof x` (the auto-loaded module + any Go
   built-ins). Always visible.
3. **Own package** `ctx.Package` members for `typeof x` (including lowercase, if
   the access is privileged — i.e. through `self` in the type's own method).
4. **Imported packages**: for each `Q` in `ctx.File.Imports`, `Q`'s **exported**
   members for `typeof x`.
5. The **`unknown` (top-type)** members at each of tiers 2–4 (so `Is?`, `In?`,
   and user universal extensions apply to every value).

Resolution rules:
- **> 1 distinct definition** visible for `Name` → **clash error** (§6).
- a **property** → invoke its getter with the receiver pushed on
  `Env.InstStack` (the existing instance pattern at
  [dot.go:153](../../pkg/builtins/dot.go), generalized to any `Tval`);
- a **method** → return a bound `Fun` that pushes the receiver on call (the
  wrapper at [dot.go:165](../../pkg/builtins/dot.go), generalized);
- **none** → "`Name` is not defined on `<type>`" (the `r.ReadAll` error in §5).

Because a method body runs under its **defining** file's context
(`BindMethod` captures `defCtx`), calls *inside* a method (e.g. `self.Read`
inside `ReadAll`) resolve through the **defining** module's imports — which is why
`readerExt` importing `reader` lets `ReadAll`'s body call `self.Read` (§5).

Behavior change: `array.Name` / `dict.Name` / `str.Name` (bare identifier) used
to error ("use bracket indexing"); it is now a member lookup. Bracket forms keep
their indexing/slicing semantics; `=`-into-index is unchanged.

> **Performance**: resolution walks the import set per call. Cache a resolved
> `(File, TypeID) → memberTable` map, invalidated when a file's imports change
> (only during load). Correctness first; caching is a mechanical follow-up.

### 4.4 Non-privileged rule (`pkg/core/bind.go`)

`BindMethod` ([pkg/core/bind.go:114](../../pkg/core/bind.go)) currently
hard-asserts `*tinstance` and unconditionally grants `Privileged`
([bind.go:201-204](../../pkg/core/bind.go)). New rules:

1. Bind `self` from `Env.InstStack[0]` as a **raw `Tval`** — drop the
   `*tinstance` assertion so primitive receivers work.
2. Grant privilege **only when** the receiver is a `*tinstance` **and** the
   `MethodEntry.Privileged` flag is true; otherwise skip the privilege dance.
3. Set `Privileged` at declaration time:
   `Privileged = isStructType && ctx.Package.Path == typeValue.OriginPath`.
   A method declared in the type's **own module** is privileged; an **extension**
   from any other module (sibling/child included) is **non-privileged**; a
   **primitive** method is non-privileged (no private state). So even a
   permitted, exported extension cannot read a struct's lowercase fields.

### 4.5 The built-in module: auto-loaded thin bindings (`pkg/builtins/pho/` + `pkg/goop`)

- **Go side**: add a `goop` module (extend `stdDependencies`, or a dedicated
  `prim` module exposed via `goop.Expose`) with capitalized methods implementing
  primitive ops over a `core.Value` receiver — `Size`, `Keys`, etc. `BuildCallArgs`
  passes `core.Value` with its `Kind` intact, so a Go method can switch on kind:

  ```go
  func (s *stdDependencies) Size(v core.Value) float64 { /* len of array/str(runes)/dict */ }
  func (s *stdDependencies) Keys(v core.Value) []core.Value { /* indices or dict keys */ }
  ```

- **Pho side** (`pkg/builtins/pho/*.phl`): thin bindings + pure-Pho universals.

  `pkg/builtins/pho/collections.phl`
  ```pho
  (goimport ("stdDependencies" prim))

  (method List.Size  (self) (prim.Size self))
  (method String.Size (self) (prim.Size self))
  (method Dict.Size   (self) (prim.Size self))
  (method Dict.Keys   (self) (prim.Keys self))
  (method List.Keys  (self) (prim.Keys self))
  ```

  `pkg/builtins/pho/universal.phl`
  ```pho
  --@ (methodsig! Unknown Type -> Boolean)
  (method Unknown.Is? (self type) do
      (== (typeof self) type))

  --@ (methodsig! Unknown Collection -> Boolean)
  (method Unknown.In? (self collection) do
      (foreach value in collection
          (if (== self value) then (return True)))
      False)
  ```

- **Loading**: embed via `//go:embed pho/*.phl`; a `builtins.LoadBuiltinModule()`
  parses + evaluates them (reusing the `LexPos → ParsePos → Lower` pipeline,
  [load.go:252-262](../../pkg/modload/load.go)) into a dedicated **built-in
  package** whose member tables the dot accessor treats as always-in-scope
  (tier 2 of §4.3). Called once at bootstrap, before user code, by `main.go` and
  `cmd/pho-lsp`.
- Built-in members are **exempt** from import-scoping and the export restriction;
  user code may **not** redefine them (doing so is a clash error, §6).

### 4.6 `typeof` and `Is?`

- **`typeof`** — new Go builtin (`pkg/builtins/typeops.go` or `meta.go`). Reads
  the value's kind / struct and returns the corresponding `KindType` value via
  the built-in type bindings. Available everywhere (it's a builtin, like the old
  `len`).
- **`Is?` / `In?`** — pure-Pho universal methods in the built-in module on
  `Unknown` (§4.5). They rely on `typeof`, `==`, and `foreach`.

## 5. Cross-module extension semantics (worked example)

```pho
-- file: reader.phl  (package "reader")
(struct Reader buffer)

(method Reader.Read (self) do
    (if (self.buffer.Empty?) (return Nil))
    (const result self.buffer.[0])
    (= self.buffer self.buffer.[1 :])
    result)

-- file: readerExt.phl  (package "readerExt", sibling of "reader")
(import "reader")

-- exported extension on reader.Reader; legal because readerExt is a sibling.
(method reader.Reader.ReadAll (self) do
    (var result []
         read   (self.Read))             -- self.Read resolves via readerExt's import of reader
    (until (== read Nil) do
        (= result (append result read))
        (= read (self.Read)))
    result)

-- file: main.broken.pho
(import "reader")
(const r reader.Reader.{ buffer [1 2 3] })
(r.ReadAll)            -- ERROR: ReadAll is not defined on Reader (readerExt not imported)

-- file: main.works.pho
(import "reader" "readerExt")
(const r reader.Reader.{ buffer [1 2 3] })
(r.ReadAll)            -- [1 2 3]
```

Semantics demonstrated:
- `ReadAll` is reachable **only** where `readerExt` is imported.
- An extension stores in its **declaring** package's table (`readerExt`), keyed by
  the **target** type's id (`reader#Reader`).
- A method body resolves member calls through its **defining** file's imports.
- `ReadAll` is **non-privileged**: it may call `self.Read` (public) but could not
  touch a lowercase field of `Reader` — only `reader`'s own methods can.

**Receiver-pattern parsing.** `methodTarget`
([pkg/builtins/decl.go:93](../../pkg/builtins/decl.go)) splits the receiver form
at its **last** dot: everything before is the **type expression**, evaluated
normally to a `KindType` (`Number` → a leaf; `reader.Reader` → a package
member access); the final segment is the member name. A bare leaf is the
anonymous-method form. The linter's `expectMethodPattern`
([pkg/lint/shape.go:256](../../pkg/lint/shape.go)) must accept the qualified
`pkg.Type.Name` shape.

## 6. Name-clash rules (hard errors)

Detected by the linter first (tooling error), enforced at runtime as a backstop:

1. **Ambiguous resolution** — at a call site, a member resolves to ≥2 distinct
   definitions across visible sources (e.g. two imported modules both export
   `List.Foo`, or an import collides with the built-in module). Error at the
   call site (and flagged at the importing file's import set).
2. **Redefining a built-in member** — declaring `(method List.Size …)` (or any
   member named like a built-in for that type / `Unknown`) is rejected at the
   declaration.
3. **Duplicate in one package** — the same `(TypeID, member)` declared twice in a
   single package is rejected at the second declaration.

(Distinct types may share a member name freely; clashes are per `TypeID`, with
`unknown` members participating in every type's namespace.)

## 7. Export restriction (origin rule)

A **capitalized** extension `(method T.Name …)` / `(property T.Name …)` is legal
only when the declaring package `D` is the type's own module, a sibling, or a
descendant of the type's origin `O = typeValue.OriginPath`:

```
allowed = D == O
       || parent(D) == parent(O)        -- sibling
       || hasPrefix(D, O + "/")         -- descendant
```

- **Primitives** have `OriginPath == "root"` (declared by the language); every
  package is a descendant of root, so any module may export primitive extensions
  (`Number.Square` is always legal). `Unknown` extensions follow the same
  primitive rule.
- A **lowercase** (private) extension may be declared in any package for that
  package's own use; it is never exported, so the origin rule does not apply.
- Violations are a **hard error** (linter + runtime).

## 8. Deleting `len` / `keyof`

- Remove the builtins ([pkg/builtins/coll.go:150,197](../../pkg/builtins/coll.go)).
- Remove linter references
  ([pkg/lint/infer.go:199,203](../../pkg/lint/infer.go),
  [pkg/lint/scope.go:179](../../pkg/lint/scope.go)).
- Migrate call sites `(len x)` → `(x.Size)`, `(keyof x)` → `(x.Keys)`:
  `script/rps.pho`, `script/std/core/lists.phl`, `script/std/random/random.phl`,
  `script/std/annot/annot.phl`, `script/std/debug/debug.phl`,
  `testdata/optional.pho`, `testdata/surplus_arity.pho`, plus anything from
  `grep -rn "(len \|(keyof " script testdata`.
- Optional one-shot codemod under `tooling/` (mirroring `tooling/desigil`).

## 9. Linter & LSP (`pkg/lint`, `cmd/pho-lsp`)

Today the linter **hard-rejects** member access on primitives
([pkg/lint/member.go:57,82](../../pkg/lint/member.go)) and on array/dict/string
bare-identifier RHS. That becomes an import-scoped known-member check.

1. **Per-package extension surface.** Extend the struct collector
   ([pkg/lint/imports.go:136](../../pkg/lint/imports.go)) to record, per package,
   methods/properties keyed by target `TypeID` (struct, primitive, or `unknown`),
   with export flag and span. Seed the **built-in module** surface by scanning
   `pkg/builtins/pho/*.phl` (so built-ins are known without an import) plus the
   `typeof` builtin.
2. **Import-scoped resolution in `member.go`.** For a bare-identifier RHS, resolve
   `Name` over: built-ins ∪ own package ∪ imported packages' exported members for
   `typeof(LHS)`'s id ∪ `unknown`. Resolved → drive hover/nav/references; missing
   → `unknown-member`; ambiguous → `member-clash`. Keep bracket-form rules. Remove
   blanket primitive rejections.
3. **Declaration checks.** Flag export-restriction violations (§7), built-in
   redefinition and duplicate members (§6), at the `(method …)`/`(property …)`
   site.
4. **`scope.go` / `infer.go`.** Drop `len`/`keyof`; add `typeof` and the type
   names. `.Size`→`ShapeNum`, `.Keys`→`ShapeArray` (nice-to-have; Unknown is the
   safe default).
5. **`completion.go`** — dot-completion offers the in-scope members for the
   inferred type (built-in ∪ own ∪ imported-exported ∪ `unknown`).
6. **`nav.go` / hover** — resolve type members to their `(method …)` site
   (built-in module file or workspace file), reusing `MethodFiles`-style tracking.
7. **`semantic.go`** — type names tokenize as `type`; member names as `method`.
8. A drift test (like `builtins_drift_test`) asserts the linter's built-in member
   set matches `pkg/builtins/pho/*.phl`, so runtime and tooling never diverge.

## 10. Other touch points

- **`eval.go`**: the `KindConstructor` call branches
  ([eval.go:166,181](../../pkg/core/eval.go)) become `KindType` (construct iff
  `Constructor != nil`, else "not constructible").
- **`dot.go` package export** of a constructor ([dot.go:191](../../pkg/builtins/dot.go))
  returns the `KindType` value.
- **`decl.go`**: `struct` builds a `KindType`; `method`/`property` accept a
  `KindType` receiver, write into `ctx.Package`'s tables, and enforce §6/§7.
  `Env.Structs` ([types.go:49](../../pkg/core/types.go)) is subsumed by the
  package type table (or kept as a name→TypeID index).
- **`modload`**: export pass ([load.go:344](../../pkg/modload/load.go)) must also
  expose a package's **exported extensions** so importers can pull them into scope
  (alongside the existing capitalized value exports).

## 11. Phased work breakdown

Each phase compiles and passes tests before the next.

- **Phase 0 — Type values.** `KindType`, `TypeID`, `TypeValue`, `TvType`,
  built-in type bindings; remove `KindConstructor` (struct → `KindType`); update
  `eval.go`/`dot.go`/`decl.go` call+construct sites; extend `==`. Behavior
  otherwise unchanged. *Tests:* construction, `typeof`, type equality.
- **Phase 1 — Per-package tables + dispatch.** Move methods/properties to
  `tpackage`; rewrite the dot accessor for import-scoped resolution (§4.3);
  generalize `BindMethod` self/privilege (§4.4). *Tests:* native struct methods
  still work via import; primitive method attached + called in one file.
- **Phase 2 — Clash + export rules.** Enforce §6/§7 at runtime. *Tests:* the §5
  broken/works pair; clash and origin-violation errors.
- **Phase 3 — `typeof` + built-in module.** `typeof` builtin; `goop` primitive
  ops; `//go:embed` + `LoadBuiltinModule`; `pkg/builtins/pho/*.phl`; wire
  `main.go` + `cmd/pho-lsp`. *Tests:* `.Size`/`.Keys`/`Is?`/`In?` end-to-end.
- **Phase 4 — Delete `len`/`keyof` + migrate.** §8. *Tests:* full suite +
  `script/` clean.
- **Phase 5 — Linter & LSP.** §9. *Tests:* golden diagnostics (valid member,
  unknown member, clash, origin violation, cross-module privacy),
  completion/nav/hover snapshots, built-in drift test.
- **Phase 6 — Docs & grammar verify.** §12.

## 12. Tooling & docs

- **Tree-sitter**: qualified receiver `reader.Reader.ReadAll` and `Number.Square`
  parse as nested dot patterns — **verify** with corpus cases; no grammar change
  expected (a grammar change requires commit + SHA bump in
  `tooling/zed-pho/extension.toml` + extension rebuild — avoid if unneeded).
- **Docs**: rewrite the object-model section of
  [Doc/Language.md](../../Doc/Language.md); mark relevant
  [Doc/Features.md](../../Doc/Features.md) items DONE.

## 13. Forward-compatibility with the gradual type system

The coming set-theoretic gradual type system builds on this object model and the
annotation system. This design supports it by:

- **First-class types with stable ids.** `TypeID` is a stable string
  (`prim:*`, `<pkg>#Name`, `unknown`) — a natural atom for set operations (unions,
  intersections, negations) the checker will introduce.
- **Explicit top type.** `Unknown` is the top type and the universal-method
  receiver, aligning with the lattice's ⊤.
- **Lexical method visibility.** Import-scoped resolution (§4.3) makes "which
  members does this value have *here*" a static, file-local question the checker
  can answer without whole-program analysis.
- **Annotation hooks already present.** `methodsig!` annotates methods (§4.5);
  the annotation harness stashes per-form results
  ([pkg/core/types.go:75](../../pkg/core/types.go)), so method signatures flow to
  the checker without new plumbing.
- **No global mutation of type surface.** Because extensions are import-scoped and
  origin-restricted (§7), a type's member set is well-defined per module — a
  prerequisite for sound checking.

## 14. Resolved assumptions (confirm if any differ)

- **Non-transitive imports.** Importing `M` brings `M`'s *own* exported
  extensions into scope, not the extensions of `M`'s imports — matching how value
  exports already work (you import what you use).
- **Built-in module is exempt** from import-scoping and the origin rule; its
  members are always visible and cannot be redefined.
- **Primitives' origin is `root`**, so any module may export primitive/`Unknown`
  extensions; the origin rule meaningfully constrains only struct extensions.
- **"Sibling or child" includes the type's own module** (native methods) and
  descendants at any depth.

## 15. Definition of done

- Every value accepts methods/properties; primitives included; literals work
  (`([1 2 3].Size)`); `typeof`/`Is?`/`In?` work.
- Extensions are import-scoped (the §5 broken/works pair behaves exactly as
  shown); exported extensions obey the origin rule; clashes are hard errors.
- Externally-attached methods are non-privileged; same-module methods retain
  private access.
- `KindConstructor` is gone; `KindType` covers all types with stable ids.
- Built-in methods live in `pkg/builtins/pho/*.phl` as thin bindings over Go,
  auto-loaded and always in scope; `len`/`keyof` deleted and all call sites
  migrated.
- Linter/LSP: correct member resolution, clash/origin/privacy diagnostics,
  completion + hover + go-to-def; built-in drift test green.
- `Doc/Language.md` updated; `Doc/Features.md` items marked DONE; full suite and
  `script/` run green.
