# Pho Codebase Overview

Pho is a Lisp-flavored language with non-Lispy extras (Go-style structs +
methods + privacy, file-scoped imports, `a.b` accessor + slicing, dict /
array literals, `'`/`!` macro tier, Go-interop). The implementation is a
tree-walking interpreter written in Go, structured as an embeddable library
plus a thin binary. Source files use the `.pho` extension.

The Go module name is `pho` (`go.mod`). The binary at `./main.go` is a ~20
line entry point — every meaningful piece of the interpreter lives under
`pkg/`.

---

## Top-level layout

```
pho/
├── go.mod                   module pho (Go 1.24)
├── main.go                  binary entry: register stdlib Go module, load "main"
├── std/                     Pho standard library
│   ├── core/convert.pho     (currently empty)
│   ├── fmt/format.pho       Format
│   └── io/{read,write}.pho  ReadLine, PrintLine
├── main/main.pho            the program the binary loads
└── pkg/                     embeddable library
    ├── core/                types + evaluator + scope/bindings + Context
    ├── syntax/              lex, parse, sugar passes, Derepr
    ├── goop/                Go-interop: PhoModule, Expose, Call, stdDependencies
    ├── builtins/            built-in functions + NewEnv (per-category files)
    └── modload/             package loader with cache + cycle detection
```

External users embed Pho by importing `pho/pkg/builtins` (for the
side-effecting `init` that wires the loader) and `pho/pkg/modload` (to load
packages).

---

## Pipeline

```
.pho source
   ↓ syntax.Lex            text → []string tokens
   ↓ syntax.Parse          tokens → core.Branch tree, then four sugar passes
   ↓ modload.LoadPackage   files in a directory → *core.Package, evaluated
   ↓ core.Branch.Evaluate  recursive descent dispatching on Kind
core.Value
```

Evaluation threads a `core.Context` value through every call. There are no
package-level variables holding interpreter state.

---

## Core types (`pkg/core/types.go`)

The package uses a "lowercase internal, uppercase exported alias" convention:
internal code uses `ttbranch`, `tval`, `tenv`, etc.; the bottom of `types.go`
exports `type Branch = ttbranch`, `Value = tval`, `Env = tenv`, etc., so
external packages reference them through these aliases. They're alias
declarations (`=`) not new types — assertions and conversions cross freely.

| Internal | Exported alias | What it is |
|---|---|---|
| `ttnode` | `Node` | interface: `Evaluate(ctx Context) tval` |
| `ttbranch` | `Branch` | `[]ttnode` — a call or list |
| `ttleaf` | `Leaf` | `string` — atom |
| `tval` | `Value` | `{Val any; Kind string}` — runtime value |
| `tStackEntry` | `StackEntry` | `{Val tval; IsConstant bool}` |
| `tfun` | `Fun` | `func(ctx Context, argv []ttnode) tval` |
| `tcontext` | `ScopeCtx` | closure-capture: `{Captures; MaxStackDepth}` |
| `tenv` | `Env` | `{Globals; Stack; CtxStack; Structs; InstStack}` |
| `tpackage` | `Package` | `{Path; Files; Exports; Env}` |
| `tfile` | `File` | `{FileName; Pkg; Imports; Tree}` |
| `tstruct` | `Struct` | struct definition: fields, methods, origin env |
| `tinstance` | `Instance` | struct value: `{Struct; Fields; Privileged}` |
| `tmethod` | `Method` | `{Struct; Fun}` |
| `tconstructor` | `Constructor` | `{StructName; StructData; Constructor}` |

`Kind*` constants tag each `tval`: `KindNum`, `KindArray`, `KindDict`,
`KindStr`, `KindChr`, `KindBool`, `KindNil`, `KindFun`, `KindInstance`,
`KindMethod`, `KindPackage`, `KindGoPackage`, `KindConstructor`.

**Two different "context" types live in this package, despite similar
names.** `Context` (in `context.go`) is the value-typed evaluator state
threaded through every call. `tcontext` / `ScopeCtx` (in `types.go`) is the
closure-capture entry pushed onto `tenv.CtxStack` when entering a function or
block. Don't conflate them.

---

## Context (`pkg/core/context.go`)

```go
type Context struct {
    Env     *Env
    Package *Package
    File    *File
}
```

Passed by value through every function call and every `Evaluate`. The
fields are pointers, which gives lexical save/restore essentially for free:

- Mutations *through* `ctx.Env` (push frames, declare bindings, write to
  `Imports`) propagate to the caller because Env is shared.
- Rebinding `ctx.Env = something` in a callee has no effect on the caller
  because `ctx` is a value-typed parameter.

`WithEnv`/`WithFile`/`WithPackage` return a copy with one field swapped —
used wherever the old code did the `prevEnv := activeEnv; activeEnv = X;
defer activeEnv = prevEnv` dance.

---

## Evaluator (`pkg/core/eval.go`)

Two `Evaluate(ctx Context) tval` methods plus one helper:

- **`(ttbranch).Evaluate(ctx)`** — dispatches on `br[0]`:
    - leaf head → `ctx.Resolve(name)`, then switch on Kind: `KindFun` calls
      the tfun, `KindConstructor` calls its constructor, anything else
      errors gracefully.
    - branch head → recursively evaluate, same Kind switch.
- **`(ttleaf).Evaluate(ctx)`** — regex-based literal recognition: numbers
  (`-?[0-9]+`), `"strings"`, `` `c` `` chars, `Nil`, `True`/`False`, then
  identifier lookup via `ctx.Resolve`. Falls back to a final `Resolve` for
  symbolic-named functions like `+`.
- **`DistributeSpreadExpressions(ctx, branch)`** — pre-pass over a call's
  args: any `(spread expr)` form expands to the array `expr` evaluates to.

Arguments to a call arrive as **unevaluated** `[]ttnode`. Each builtin
chooses when (or whether) to call `Evaluate(ctx)` on them — this is what
lets special forms (`if`, `while`, `var`, `=`, `'`, …) be ordinary entries
in the global table rather than parser-baked syntax.

---

## Scope and bindings (`pkg/core/scope.go`)

All scope operations are methods on `Context`:

- `ctx.Resolve(name) (Value, bool)` — search order: the current frame
  (bounded by the active closure context's `MaxStackDepth`), then globals,
  then `ctx.File.Imports` (file-scoped imports, à la Go).
- `ctx.Declare(name, val, isConst) bool` — refuses redeclaration anywhere
  visible.
- `ctx.Set(name, val) bool` — refuses to mutate constants (returns false
  and does not modify).
- `ctx.PushFrame()` / `ctx.PopFrame()` — frame management, mutating
  `ctx.Env.Stack` in place.
- `ctx.PushContext(code)` / `ctx.PopContext()` — push/pop a `tcontext`
  closure-capture entry. `findIdentifiers` walks `code` and seeds the
  capture map; `capture` is a small helper that produces a getter/setter
  pair tied to the current ctx.

Frames are stored as a `[][]map` slice with the innermost frame at index 0
(prepend-style push). The bottom of every package's stack is the builtin
map, exposed via `Env.Globals` for fast access during Resolve fallback.

---

## Bindings (`pkg/core/bind.go`)

Three flavors, all returning `tfun`:

- **`BindFun(repr, argList, defCtx) Fun`** — produces the closure for a
  user-defined `(fun ...)`. `defCtx` is captured at definition time so the
  body resolves identifiers and file-scoped imports against the function's
  source. At call time the closure receives `callCtx` (used only to
  evaluate the argv); the body runs under `defCtx` with a fresh frame and
  closure context pushed.
- **`BindMethod(repr, argList, defCtx) Fun`** — same, plus self-injection:
  `argList[0]` is bound from `callCtx.Env.InstStack[0]` (pushed by the Dot
  accessor's wrapper), and `Privileged` is toggled on the instance for the
  duration so private fields/methods become accessible inside the body.
- **`BindCallback(repr) Fun`** — does *not* capture a definition site. The
  body runs under the caller's `ctx`. Used by `block`, `if`, and `while`
  for inline expressions.

Trailing rest-args (`#name`, set up by `fun`/`method` from a `(spread name)`
last param) are stripped from `argList` before the closure runs and bound
to whatever's left in `args`.

---

## Tv constructors (`pkg/core/tval.go`)

Trivial wrappers — `tval{val, kind}` — for every `Kind*`. They are pure;
they do not read any global state.

The interesting one is `TvUnknown(data any)` which reflectively converts an
arbitrary Go value into a `tval`. The `reflect.Func` case wraps the Go
function as a `tfun` whose body evaluates the call's argv under the
caller's ctx and feeds the results to `CallDirect`. `CallDirect` lives in
`tval.go` (rather than `goop`) so `core` doesn't have to import `goop`.

---

## Inspect, mangle (`pkg/core/inspect.go`, `pkg/core/mangle.go`)

- `Inspect(node) string` — the printer. Special-cases the mangled `Dot`
  (renders as `a.b`) and the `slice` head (renders as `[a b c]` or `[]`).
  Used in error messages and by the `inspect` builtin.
- `mangle.go` defines `ManglerSuffix` — a per-process random tag — plus the
  internal operator names `Dot` and `WithEnv`. Mangling makes these names
  unreachable from user code; the parser rewrites `a.b` syntactically into
  a call to the unguessable `Dot` symbol.

---

## Syntax (`pkg/syntax/`)

Three files. No interpreter state — just text↔AST.

### `lex.go`

`Lex(input)` runs:

1. **`SkipComments`** — strips lines whose first non-whitespace is `--`.
2. **`Escape`** — replaces special characters inside string literals (and
   inside backtick-escapes outside of strings) with random per-process
   sentinels (`#LBRK:NN#`, `#SP:NN#`, …) so they survive whitespace-based
   tokenization.
3. **String-replace** rewrites `[…]` → `(slice …)`, `{…}` → `(map …)`,
   and isolates `(`, `)`, `'`, `&`, `!`, `.` as standalone tokens.
4. **Regex split** on `\s+`, then `UnEscape` each token.

### `parse.go`

`Parse(lexed)` calls `ParseTreeInner` to build a `Branch` from the token
list, then runs the four sugar passes in order, then strips the leading and
trailing empty leaves Lex padded the input with.

### `sugar.go`

The four sugar passes plus the listification system:

| Pass | Transformation |
|---|---|
| `CompressBlockLiterals` | `&x` → `(block 'x)`, `&(...)` → `(block '(...))` |
| `CompressDotLiterals` | `a . b . c` → `(Dot a (Dot b c))` (with mangled name) |
| `CompressMacroLiterals` | `(name! a b c)` → `(resume (name 'a 'b 'c))` |
| `CompressCodeLiterals` | `'leaf` → `"leaf"`, `'(a b)` → `(slice "a" "b")` |

Plus:

- `ListifyTree(node)` / `ListifyVal(val)` — convert tree/value into the
  quoted-list representation.
- `TreeifyVal(val)` — value back into AST.
- `Derepr(node)` — strip the `slice` head added by listification and
  unquote string-literal leaves. Used by builtins (`fun`, `method`, `block`,
  `var`, `=`, `if`, `while`) to recover argument trees from quoted forms.

---

## Module loader (`pkg/modload/load.go`)

`LoadPackage(path)` loads a directory of `.pho` files as a single package:

1. Cache lookup — return cached `*Package` if already loaded.
2. Cycle detection — error if the path is in `loadingPackages`.
3. Read directory, collect `.pho` files, sort lexicographically.
4. Construct a fresh package env via `EnvFactory()` (see below).
5. Build a `pkgCtx` for loading; `PushFrame()` once so package-level
   declarations land above the builtin globals.
6. For each file: parse → derive `fileCtx := pkgCtx.WithFile(file)` →
   evaluate each top-level form against `fileCtx`.
7. Walk `pkg.Env.Stack[0]` and copy capitalized identifiers (functions /
   methods / constructors only) into `pkg.Exports`.
8. Cache, return.

### `EnvFactory` (cycle resolution)

`builtins` needs `modload` (to implement the `(import …)` form), and
`modload` needs `builtins.NewEnv` (to construct a package's env).
Resolved with a function pointer:

```go
// pkg/modload/load.go
var EnvFactory func() core.Env
```

`builtins.init()` populates `EnvFactory = NewEnv`. The binary's blank
import of `pho/pkg/builtins` triggers that wiring at startup. `modload`
itself doesn't import `builtins`, so the dependency stays one-way:

```
builtins → modload → core
       ↘    ↘
        →  syntax → core
```

---

## Go-interop (`pkg/goop/`)

### `module.go`

- `PhoModule { Name, Children, Data }` — a tree of named Go modules.
  `Data` is any Go struct whose **exported** methods are callable from
  Pho.
- `GoModules` — global registry, populated by `Expose(*PhoModule)`.
- `Call(origin, funcName, args)` — reflective method invocation; refuses
  lower-case names (which can't be exported in Go anyway). Single returns
  unwrapped, multi-return becomes `[]any`.

### `stddeps.go`

The concrete struct `stdDependencies` provides the `ReadLine`, `PrintLine`,
`Print`, `Sprint` methods that the Pho stdlib (`std/io`, `std/fmt`)
imports via `goimport`. `StdDependenciesModule()` packages it as a
`*PhoModule` for `Expose`.

The Pho binary registers exactly one Go module at startup —
`stdDependencies`. Embedders can register more before calling
`modload.LoadPackage`.

---

## Builtins (`pkg/builtins/`)

Every builtin has the signature `func(ctx core.Context, argv []core.Node)
core.Value`, wrapped via the package-private `global(fn)` helper as a
constant `core.StackEntry`. Each category file defines a function returning
a `map[string]core.StackEntry`, and `register.go::NewEnv` merges them all.

| File | Builtins |
|---|---|
| `arith.go` | `+`, `-`, `*`, `/`, `==`, `~=`, `<`, `<=`, `>`, `>=` |
| `coll.go` | `slice`, `map`, `has`, `len`, `append`, `drop` |
| `ctrl.go` | `if`, `while`, `do`, `and`, `or`, `~` |
| `decl.go` | `fun`, `method`, `struct`, `var`, `const`, `=`, `block` |
| `dot.go` | the mangled `Dot` accessor (its own file — ~200 lines) |
| `meta.go` | `pause`, `resume`, `inspect`, `spread` |
| `modimport.go` | `import`, `goimport` |

`helpers.go` houses package-private helpers used across categories:
`asBool`, `asNum`, `tvalEqual` (deep equality on values),
`parseImportRequests` (shared by `import` and `goimport`), and `ParseArgs`
(typed positional-arg validation, used by `struct`).

`register.go` also contains the `init()` that sets
`modload.EnvFactory = NewEnv`.

---

## Cross-cutting

### File-scoped imports

`tfile.Imports` is a per-file `map[string]Value` keyed by alias. The
`import` and `goimport` builtins write to `ctx.File.Imports`. `ctx.Resolve`
falls back to it after stack frames and globals. This matches Go's
semantics exactly — file A in a package can alias `io` differently from
file B in the same package, and the alias doesn't leak.

### The Dot operator

`a.b` becomes `(Dot a b)` (mangled) at parse time. `Dot` dispatches on
`a.Kind`:

- **dict** — key lookup, returns `Nil` on miss.
- **array** — integer index, or slice forms (`[a:b]`, `[:b]`, `[a:]`,
  `[:]`) when the rhs is a `slice`-headed branch.
- **str** — index → `chr`.
- **instance** — field access (with public/private check based on the
  identifier's first letter) or method dispatch. Method lookup uses
  `ctx.WithEnv(inst.Struct.Origin)` so the method's name resolves in the
  struct's source env, not the caller's. The returned wrapper pushes the
  receiver onto `Env.InstStack` before invoking the method's `Fun`, then
  pops on `defer`.
- **package** — uppercase export lookup.
- **gopackage** — wraps the named Go method as a `tfun` that calls
  `goop.Call` and runs the result through `core.TvUnknown`.
- **num** — fractional-decimal hack: `12 . 34` is recombined into `12.34`
  (with sign correction for negative integer parts). The number lexer
  doesn't accept `.`; decimals come through this path.

### The quote system

`'expr` is the listification prefix. `CompressCodeLiterals` rewrites it
during parsing: leaves get their content wrapped in quotes (`'foo` →
`"foo"`); branches become `(slice "head" "child1" …)`. `pause` does the
same to runtime values. `resume` is the inverse — turns a listified value
back into AST and evaluates it. `Derepr` strips listification when
unwrapping argument trees (so `(fun 'name '(a b) '(...))` can recover the
original arg list from its quoted form).

### Macros

`(name! a b c)` rewrites at parse time to `(resume (name 'a 'b 'c))` — the
function `name` runs at compile-ish-time receiving its args as quoted lists,
and whatever it returns is `resume`'d (evaluated). `CompressMacroLiterals`
implements the rewrite.

### Spread arguments

`(spread x)` is a marker form. `DistributeSpreadExpressions` walks an
argument list pre-evaluation and splices any `(spread x)` into the
argument array (where `x` evaluates to a `slice`). `fun` and `method`
recognize a trailing `(spread name)` parameter and turn it into a `#name`
rest-arg in the function's argList.

---

## Standard library (`std/`)

Three small packages, each `goimport`s `stdDependencies` and re-exports a
function:

- `std/io/read.pho` → `ReadLine`
- `std/io/write.pho` → `PrintLine`
- `std/fmt/format.pho` → `Format` (wraps `Sprint`)
- `std/core/convert.pho` — empty placeholder

`main/main.pho`:

```
(import "std/io")
(io.PrintLine "a" "b" "c")
(io.ReadLine "heyo: ")
```

Demonstrates the surface area: a directory `import`, file-scoped `io`
alias, dot-call into a Pho-side function which itself dot-calls into a
`goimport`'d Go-side function.

---

## Embedding

```go
import (
    "pho/pkg/goop"
    "pho/pkg/modload"

    _ "pho/pkg/builtins"  // init() wires modload.EnvFactory
)

func main() {
    goop.Expose(myGoModule)             // optional: add Go-interop modules
    pkg, err := modload.LoadPackage("path/to/pkg")
    // ...
    // Optional: invoke an exported function
    fn := pkg.Exports["MyFunc"]
    ctx := core.Context{Env: pkg.Env, Package: pkg}
    result := fn.Val.(core.Fun)(ctx, []core.Node{...})
}
```

The interpreter is reentrant — distinct `Context` values can run
concurrently in the same process without interfering, since none of the
runtime state lives in package-level variables.

---

## History

The codebase passed through three reshapes to reach the current shape:

1. **File split** — `environment.go` (~900 lines) and `parser.go` were
   broken up into per-concern files within `package main`.
2. **Subpackage extraction** — files moved under `pkg/{core, syntax, goop,
   builtins, modload}` with `module pho`. Cycles avoided via
   `modload.EnvFactory` being populated by `builtins.init()`. Lowercase
   internal names kept; uppercase aliases at the bottom of `core/types.go`
   form the public surface.
3. **Globals removed** — three `active*` package-level variables (env,
   package, file) replaced with a value-typed `Context` threaded through
   every `Evaluate` and every builtin. `tval` lost its dead `SourcePkg` /
   `SourceFile` fields. Save/restore patterns disappeared because rebinding
   a value-typed `Context` field doesn't propagate to the caller.
