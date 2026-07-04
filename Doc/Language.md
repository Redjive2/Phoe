# The Pho Language

A friendly tour. Read top to bottom; each section assumes the previous.

## Philosophy

Pho is a small Lisp-flavored interpreted language with Go-style
packages.

The Lisp side: code is parenthesized prefix calls. `(+ 1 2)` means "call
`+` with `1` and `2`". This makes the language tiny — there's only one
kind of expression, the call. Special forms like `if` and `var` are
just entries in the global table that happen to receive their arguments
unevaluated.

The non-Lisp side: arrays, dicts, methods on structs, file-scoped
imports, an `a.b` accessor — the stuff you actually want when writing
real code. Pho pays the syntactic-complexity tax for these because
they make programs shorter and more familiar.

The package model is borrowed from Go: a directory is a package, files
in the same directory share a namespace, capitalized names are
exported, lowercase ones aren't. Imports are per-file (so file A and
file B in the same package can alias `io` differently, and the alias
doesn't leak).

There are two extension layers: macros (Pho-level — code as data, with
`'` quote and `(macro! ...)` invocation) and Go-interop (`goimport`
brings in a Go module exposed via the host's `goop` package). The first
is for language extension; the second is for I/O and anything else the
runtime can't do directly.

A Pho program is just a directory of source files. There are two
flavors:

- **`.pho` files** — *scripts*. Anything goes at the top level:
  declarations, imports, and arbitrary expressions / function calls.
- **`.phl` files** — *libraries*. Top-level forms must be declarations
  or imports — `import`, `goimport`, `fun`, `method`, `struct`, `const`,
  `var`. Side-effecting forms (bare calls, I/O) are rejected before they
  run, so loading a library can't, by construction, perform I/O. A
  top-level `var` is module-level state: mutable from within the module
  but read-only from outside it.

A package can contain a mix of both extensions; they share one
namespace.

The interpreter loads `main`, evaluates each file's top-level forms in
lexicographic order of filename, collects exports, and that's it.

## Basic syntax

A Pho file is a sequence of expressions. An expression is one of:

- A **number** — `42`, `-5`. Decimals like `1.5` are parsed as a number,
  a `.`, and another number; they're reassembled by the dot operator at
  evaluation time.
- A **string** — `"hello"`. Strings support escape sequences and
  interpolation:
    - `\n`, `\t`, `\r`, `\"`, `\\`, `\0`, `\a`, `\b`, `\f`, `\v`,
      `\%` — conventional C-style escapes. An unknown escape like
      `\q` is passed through verbatim.
    - `` `X `` (backtick + char) — passthrough: the lexer accepts the
      pair so the next byte can't terminate the string, but the
      backtick itself stays in the result. Useful for `` `( `` and
      `` `) `` when you don't want to read the backslash form.
    - `%name`, `%a.b.c`, `%(expr)` — string interpolation. The
      interpolated value is stringified and spliced into the result.
      `%name` is a bare identifier; `%a.b.c` is a dot chain
      (atomics separated by `.` with no whitespace — atomics include
      identifiers, numbers, string literals, `[…]`, `{…}`, `(…)`);
      `%(expr)` is any parenthesized expression. To embed a literal
      `%`, write `\%`.
- A **character** — `` `c` ``. Three characters: backtick, char,
  backtick.
- An **atom** — `:fast`, `:01213`. A colon glued (no space) to an
  identifier or a run of digits. Atoms are interned, immutable symbolic
  values: two atoms with the same name are the *same* object, so they're
  trivial and cheap to compare. They're handy as configuration tags and
  dict keys. Digits keep leading zeros, so `:01` and `:1` are distinct.
  Note: because the colon must be glued, an array's range/slice separator
  must be spaced — write `xs.[1 : 2]`, not `xs.[1:2]` (which reads as the
  array `[1, :2]`).
- A **boolean** — `True` or `False`.
- **Nil** — `Nil`.
- An **identifier** — `myVar`, `Point`. Capitalized identifiers are the
  ones a package exports. An identifier may end in a single `?` (the
  predicate convention), e.g. `atom?`, `empty?`.
- A **list** (a call) — `(f a b c)`. Calls `f` with arguments `a`, `b`,
  `c`.

Comments run from `--` to end of line:

```pho
-- top-level comment
(io/PrintLine "hello") -- trailing comment
```

That's the whole grammar. Everything else is sugar over these forms.

## Sugar

Each of the following desugars to a plain list at parse time. They
exist so real code looks like real code.

### Array literal — `[a b c]`

```pho
[1 2 3]            -- desugars to (slice 1 2 3)
[]
```

### Dict literal — `{k1 v1 k2 v2}`

```pho
{"a" 1 "b" 2}      -- desugars to (map "a" 1 "b" 2)
{}
```

### Dot access — `a.b` and `a.[b]`

The accessor splits by the shape of the right-hand side:

- **`a.name`** — *field access*. `name` is a literal identifier, never
  evaluated. Use it to reach a struct field/method, a package export, or a
  Go-package method.
- **`a.[expr]`** — *dynamic indexing*. `expr` is evaluated and used as an
  index or key. Use it to index a dict, array, or string. Slices are the
  colon forms inside the brackets: `a.[i : j]`, `a.[: j]`, `a.[i :]`,
  `a.[:]`.
- **`T.{ field = value ... }`** — *struct construction*. A dot followed by a
  brace group builds a `T`, reading the brace as `field = value` pairs:
  `Point.{ x = 10 y = 20 }` makes a `Point` with `x` = 10 and `y` = 20. The
  field names are bare identifiers (not quoted, not strings) — they name
  fields, they aren't evaluated. The `=` is required; the older bare
  `T.{ field value ... }` form and the `(T { ... })` call form have both been
  removed.

| LHS | valid form | meaning |
|---|---|---|
| dict | `d.[key]` | look up the evaluated `key` |
| array | `a.[i]` / `a.[i : j]` | index, or slice |
| string | `s.[i]` / `s.[i : j]` | the rune at `i`, or a rune slice |
| struct instance | `inst.field` | field, or method |
| struct (constructor) | `T.{ field = v ... }` | construct a `T` from `field = value` pairs |
| package | `pkg.Export` | the export named `Export` |
| Go-package | `pkg.Method` | the Go method, wrapped as a callable |
| number | `12.34` | reassemble as a decimal |

Mixing them up is an error: `arr.i` (bare index) and `inst.["field"]`
(bracketed field) both fail, with a message pointing at the right form.

Chains naturally: `pkg.Struct.method`, `grid.[row].[col]`. The `.` is its
own token, so the number-decimal case `1.5` still parses as "number, dot,
number" and arrives as `1.5` — only collections require the brackets.

### Quote — `'expr`

Treats `expr` as data instead of evaluating it. This is the tool for
*macros* and code-as-data — building a form to hand to `resume`, or
carrying a symbol as a value:

```pho
(pause '(+ 1 2))      -- the list (+ 1 2) as data, not the number 3
(map 'role "admin")   -- 'role is the symbol "role", used here as a key
```

Declarations and control forms do **not** use it — they're written bare
(`(fun add (x y) (+ x y))`, see *Main builtins*). A quote in a name,
parameter-list, or assignment-target slot is a static error.

### Block — `&expr`

Defers evaluation: `&expr` becomes a no-arg function (a thunk) that runs
`expr` only when it's called. Use it to hand deferred code to a function
that decides when — or whether — to run it:

```pho
(retry 3 &(fetch url))   -- retry calls the thunk up to 3 times
```

`&body` desugars to `(block 'body)`. The control forms (`if`, `foreach`,
`while`, `until`) do **not** need it — their arms and bodies are plain
expressions (`(if c then x else y)`); the form controls when each runs.

### Do notation — `do`

`do` is not a function — it's a keyword. A `do` inside a form captures
every sibling *after* it and sequences them: each is evaluated in order,
and the form yields the last one's value.

At the *head* of a form, `do` sequences in place — `(do x y z)` evaluates
`x`, `y`, `z` and yields `z`:

```pho
(do
    (io/PrintLine "step one")
    (io/PrintLine "step two")
    42)
-- prints both lines, yields 42
```

After a form's fixed arguments, `do` reads as a block separator — the tail
becomes the sequenced block. This is how multi-statement bodies are written
without an extra wrapping quote:

```pho
(fun add (a b) do
    (io/PrintLine "adding %a and %b")
    (+ a b))
-- desugars to: (fun add (a b) (do … (io/PrintLine …) (+ a b)))
```

(`do …` is an internal mangled name, hidden from user code like the dot
accessor — a leading `do` is rewritten to it in place, so `(do x y)`
becomes `(do … x y)`, not a call on the block's result.) The **`identity`**
builtin just echoes its single argument; it's no longer needed to host a
do block, but remains available for forcing any value through a call slot.

### Macros — `(name! arg1 arg2)`

A function call where `!` follows the head name passes the args
*quoted* (as data), then re-evaluates the result:

```pho
(myMacro! a b c)   -- shorthand for (resume (myMacro 'a 'b 'c))
```

Used to write functions that take code as input — the usual Lisp macro
trick.

### Spread — `(spread items)`

Splices an array's elements into a call's argument list — in *any* call
(a user fun, a builtin, or an array literal), at any position:

```pho
(myFun (spread args))      -- pass each element of `args` as a separate arg
[0 (spread items) 9]       -- splice into an array literal
(+ 1 (spread nums) 2)      -- mix spread with fixed arguments
```

Only an array can be spread; spreading any other value is an error.

In a parameter list, `(spread name)` is the mirror image — it collects
the caller's trailing args into the array `name`:

```pho
(fun PrintAll ((spread args))
    (io/PrintLine (spread args)))
```

### Optional — `(optional name)`

In a parameter list, `(optional name)` marks a parameter the caller may
omit; when it's left off, `name` binds to `Nil` instead of raising an
arity error. Optionals come after all required parameters and before
any trailing `(spread rest)`; a required parameter after an optional one
is rejected.

```pho
(fun greet (name (optional greeting)) do
    (if (== greeting Nil) then (= greeting "Hello"))
    (+ greeting (+ ", " name)))

(greet "Sam")        -- "Hello, Sam"  (greeting defaulted to Nil)
(greet "Sam" "Hi")   -- "Hi, Sam"
```

### Default — `(optional Type else default)`

A defaulted optional lives in the SIGNATURE: `(optional Type else default)`
declares a parameter the caller may omit; when the argument is `none` — omitted
*or* passed explicitly — it takes `default` instead. Only `none` triggers the
substitution (a real `0`/`''` is kept: `??`-style, not `||`). The default is a
closed expression evaluated in the declaring scope.

```pho
(fun add (Number (optional Number else 0)) Number)
(let add (a b) = (+ a b))

(add 5)       -- 5   (b omitted → 0)
(add 5 3)     -- 8
(add 5 none)  -- 5   (an explicit none coalesces to the default too)
```

## Implementations — `let` clauses, patterns, and overloading

A named function or method is declared by its SIGNATURE and implemented by one
or more `let` CLAUSES that directly follow it:

```pho
(fun add (Number Number) Number)          -- the declaration (types)
(let add (a b) where (== b 0) = a)        -- a guarded clause
(let add (a 0) = a)                       -- a pattern clause
(let add (a b) = (+ a b))                 -- the catch-all clause
```

Clauses are tried in order; the first whose PATTERNS match and whose `where`
guard (if any) is true runs. When the linter cannot prove the clauses cover
every input, the last clause must be an unguarded catch-all (all bare names).
Signatures are required in libraries (`.phl`); a script (`.pho`) may omit one
when it can be inferred from the clauses' patterns.

**Patterns** (clause parameters and `select` cases):

- `name` — binds anything (a bare lowercase identifier)
- `(var name)` — binds reassignably (a plain binder is immutable); `(var Type name)` adds a type
- `0`, `'str'`, `:atom`, `true`, `none` — literals, matched by equality
- `Number` (a Capitalized name) — the TYPE VALUE itself, matched by identity —
  static dispatch on a `(const T)` signature slot
- `(Type name)` — a runtime type test that binds `name`
- `[p1 p2]` — list destructure, exact length
- `Type.{ field = pat }` — instance-of `Type` + field destructure; a `(field)`
  key also binds the field's whole value (the `()` capture operator)

**select** — the match expression; `do` results stop at the next `case`:

```pho
(select [a b]
    case [0 rhs] -> rhs
    case [lhs 0] -> lhs
    case [lhs rhs] -> (+ lhs rhs))
```

**Overloading** — one name may declare several signatures, each followed by its
clauses; a call picks the overload whose parameter types accept the runtime
arguments, preferring the MOST SPECIFIC (an ambiguous call is an error):

```pho
(fun add (Number Number) Number)
(let add (a b) = (+ a b))
(fun add (String String) String)
(let add (a b) = (+ a b))
```

**const parameters** — `(const T)` in a signature marks a slot whose call-site
argument must be a parse-time constant (a literal or type name); clauses
dispatch on its value with literal patterns and need not cover all of `T`:

```pho
(fun conv ((const Type) Number) Number)
(let conv (Number x) = (+ x 1))     -- (conv Number 5)
(let conv (t x) = 0)                -- any other type
```

## Main builtins

The forms you'll touch in nearly every Pho program.

### `var` / `const` — bindings

```pho
(var x 5)                  -- function/method bodies, .pho scripts, or .phl top level
(const PI 3)               -- anywhere, including top level
(var a 1 b 2)              -- multiple at once
```

`var` is mutable, `const` isn't. Constants refuse mutation and report an
error if you try. Both may appear anywhere — function/method bodies,
`.pho` scripts, and the top level of a `.phl` library.

A **capitalized** top-level `var`/`const` is *exported*: an importer can
read it as `pkg.Name` (the value is read live, so an exported `var`
reflects the module's own updates). Exported bindings are **read-only
from outside** — only the declaring module can mutate its own `var`
(with a bare `(= Name v)` inside that module); `(= pkg.Name v)` from an
importer is an error.

### `=` — assignment

```pho
(= x 10)         -- assign to a var
(= p.X 100)      -- assign to a struct field
(= d.[k] v)      -- assign to a dict key / array index
```

You can assign to a local var, a struct field, or a dict/array slot — but
not to a member of an imported module (`(= pkg.Name v)` is rejected: a
module's bindings are read-only from outside it), nor to a `const`.

### `fun` — function definition

```pho
(fun double (n) (* n 2))
```

Three args — name, parameter list, body — all bare. The 2-arg form (no
name) returns the function as a value:

```pho
(var double (fun (n) (* n 2)))
```

A multi-statement body sequences with `do` notation — `(fun f (x) do …)`
(see *Do notation*).

A function captures the file and package it was defined in, so its body
sees the right imports even when called from another package.

### `if` / `unless` / `foreach` / `while` / `until` / `do`

```pho
(if cond then expr                        -- yields the first matching branch's
 elif cond then expr                      --   expr; `elif`/`else` are optional.
 else expr)                               --   No match and no else → Nil.
(unless cond then expr else expr)         -- opposite of `if`: runs the `then`
                                          --   branch when cond is FALSE. No
                                          --   `elif`; `else` is optional.
(foreach x in xs body)                    -- iterate x over an array/string/dict
(while cond then body)                    -- loop while cond is true
(until cond then body)                    -- loop until cond becomes true
(do e1 e2 e3)                             -- run each in sequence, yield the last
```

`if` uses the bare keyword markers `then`, `elif`, and `else`: branches are
tried top to bottom, and the first whose condition is true yields its `then`
expression. A single-line `(if c then x else y)` reads naturally; a
multi-statement arm wraps in `(do …)` — `(if c then (do a b) else (do x y))`
— since a bare `do` would otherwise capture the `else` marker.

`unless` is the mirror image: `(unless c then x else y)` runs `x` when `c` is
false and `y` when it's true. It takes a single condition (no `elif`) plus an
optional `else`, so it reads as "do this unless the condition holds".

The loops also use bare keyword markers. `foreach` iterates — `(foreach x in
xs …)` binds `x` to each element of an array, rune of a string, or key of a
dict (the loop variable is a per-iteration constant); `in` is required.
`while`/`until` are the conditional loops — `(while c then …)` runs while `c`
is true, `(until c then …)` until `c` becomes true; `then` is required.
`foreach` is iteration only — use `while`/`until` for a conditional loop.
`break` and `continue` work inside any of them.

Arms and bodies are plain expressions — no sigils. The form itself controls
when each runs (only the taken `if` branch evaluates; a loop body runs each
iteration), so they don't need deferring. A leading `do` sequences in place —
`(do …)` works in expression position with no wrapper (see *Do notation*).

### `and` / `or` / `~`

Boolean operators. `and` and `or` short-circuit; `~` is logical not.

```pho
(and (> x 0) (< x 10))
(~ True)                  -- False
```

### Arithmetic and comparison

`+ - * /` for numbers, and `mod` for modulo. `+` also concatenates
strings if all args are strings. `==`, `~=`, `<`, `<=`, `>`, `>=` for
comparison; `==` and `~=` do *deep* equality on arrays and dicts.

```pho
(+ 1 2 3)                          -- 6
(mod 10 3)                         -- 1
(+ "hello " "world")               -- "hello world"
(== [1 2 3] [1 2 3])               -- True
```

### `import` / `goimport`

Bring a package into the *current file*:

```pho
(import "std/io")              -- aliased as `io`
(import ("std/io" myio))       -- explicit alias (a bare name)

(goimport ("stdDependencies" dep))
```

`import` loads a Pho package (a directory of `.pho` files); `goimport`
binds a Go-side module the host has registered. Either way, the alias
is visible only in this file.

## Type signatures

Pho is **gradually typed**: types are optional, and the linter checks a
form only where you actually write one. An un-annotated binding or function
stays dynamic — no constraint is invented for you, and no false positives
fire on code you left untyped.

Types are ordinary values — `Number`, `String`, `Boolean`, `Char`, `Atom`,
`List`, `Map`, `Function`, `NilT` — composed with the connectives `Or`,
`And`, `Not`, `Diff` and the constructors `(List T)`, `(Map K V)`. A struct
name is also a type. One casing convention drives everything: a
**Capitalized** name reads as a *type*, a lowercase one as a *value*.

### Typed bindings

A `let` binding groups its type **before** the name in parens — `(Type name)` —
using the same type-first order struct fields and signatures use:

```pho
(let (Number n) = 5)
(let var (String name) = 'ada')          -- `let var` for a mutable binding
(let ((Or Number String) id) = 42)       -- the type can be any type expression
(let (Number a) = 1  (String b) = 'z')   -- multiple bindings, each optionally typed
```

The initializer is checked against the declared type; a mismatch is a lint
error. The type is erased at runtime — `(let (Number n) = 5)` just binds `n`.

### Destructuring bindings

A `let` target may be a **pattern** that pulls the value apart, binding several
names at once. Bare binders are immutable; wrap one in `(var …)` to make it
reassignable, or lead the whole binding with `var` to make them all mutable:

```pho
(let [a b c] = [1 2 3])              -- a=1, b=2, c=3 (all const)
(let [(var x) y] = [10 20])          -- x is mutable, y is const
(let var [p q] = coords)             -- every binder mutable
(let [first [inner]] = [1 [2]])      -- patterns nest
(let [(Number a) (Number b)] = pair) -- binders may be typed (erased)
```

A **struct** target destructures fields. The `()` capture operator on a field
key also binds the field's whole value while its pattern pulls the field apart:

```pho
(let Point.{ x = px  y = py } = origin)    -- bind fields x → px, y → py
(let Box.{ (items) = [head tail] } = box)  -- bind `items` AND head, tail
```

A pattern that can't match — a wrong-length list, a non-matching struct — is a
runtime error (a `let` binding has no fall-through). These are the same patterns
and helpers that clause parameters and `select` cases use.

### Function signatures

A signature is a `fun` form whose parameter slots and return are **types**,
written as its own form next to the implementation:

```pho
(fun add (Number Number) Number)     -- signature: param types, then result
(fun add (x y) (+ x y))              -- implementation: param names, then body
```

The two are paired by name and are order-independent (signature may come
before or after). Call arguments *and* the implementation's return value are
checked against the signature. A signature with **no** matching
implementation is an error (`missing-implementation`).

The casing rule is what tells signature from implementation: a signature's
params are all types (Capitalized, or a connective form like `(Or …)`); an
implementation's params are value names (lowercase). A Capitalized
implementation parameter is flagged (`capitalized-param`) — it reads as a
type, so you likely meant a signature. A signature whose result is `Nil`
(e.g. `(fun log (String) Nil)`) is fine — in return position `Nil` means the
nil type.

### Method signatures

The same, with the **receiver type first** (the type of `self`):

```pho
(method Point.Dist (Point Point) Number)   -- self-type, then arg types, then result
(method Point.Dist (self other) …)          -- implementation
```

> Earlier drafts carried these as `--@ (~sig …)` / `--@ (~type …)`
> parse-time annotations. Those are **disconnected** now — the inline forms
> above are the way to type Pho code.

### Typed struct fields and properties

A struct declares its fields **type-first** inside the `.{ … }` brace — the same
`Type name` order the signatures use:

```pho
(struct Point.{ Number x Number y })                  -- two Number fields
(struct Node.{ Number value (Or Node none) next })    -- a recursive field
```

Construction stays **name-first** (you're assigning values, not declaring
types): `Point.{ x = 1 y = 2 }`. A field's declared type is checked on access — `p.x`
reads as its type. The anonymous record type `Struct.{ Number x }` — "any struct
with at least these fields" — uses the same `Type name` order.

A property can carry a declared value type by wrapping its name exactly like a
typed binding:

```pho
(property (Number Box.area) get (method Box (self) (* self.w self.h)))  -- attached
(property (Number twice) get (fun () (* n 2)))                          -- free-standing
```

An **attached** property names its receiver (`Box.area`) and its getter is a
`(method Box …)`; a **free-standing** property is a bare name whose getter is a
plain `(fun () …)`.

## Other builtins

The rest, grouped by what they're for.

### Structs and methods

```pho
(struct Point X y)

(method Point.Shift (self d) (+ self.X d))
(method Point.tweak (self d) (+ self.y d))

(var p Point.{ X 10 y 20 })   -- bare field names, no quotes
(p.Shift 5)        -- 15
```

The first parameter of a method is `self` (it's bound from the
receiver — you don't pass it explicitly at the call site). Field and
method visibility is by capitalization: `X` and `Shift` are public,
`y` and `tweak` are accessible only from within other methods of the
same struct.

### Collections

| Form | Meaning |
|---|---|
| `(slice a b c)` | Same as `[a b c]`. The bracket form is preferred. |
| `(map k v k v)` | Same as `{k v k v}`. The brace form is preferred. |
| `(has col k)` | True if `col` contains key/index `k`. |
| `(keyof col)` | Array of all indices of an array, or keys of a map. |
| `(len arr)` | Array length. |
| `(append arr x y z)` | New array with extra tail elements. |
| `(drop arr n)` | New array minus the first `n` elements. |

### Atoms

| Form | Meaning |
|---|---|
| `(atom? x)` | True if `x` is an atom. |
| `(atom "foo")` | The atom `:foo`. Errors if the string isn't a legal atom form (an identifier or digits). |
| `(atomName :foo)` | The atom's name as a string — `"foo"`. |

### Code as data

| Form | Meaning |
|---|---|
| `(pause val)` | Convert a value into its quoted-list form. |
| `(resume tree)` | Evaluate a quoted-list form (the inverse). |
| `(inspect node)` | Render an AST node as a string. |
| `(spread arr)` | See *Sugar* above. |

### Block

`(block 'expr)` wraps an expression as a no-arg callable. The `&`
sigil is sugar for this; you'll rarely write `block` by hand.

---

That's the language. For the precise grammar, see
`tooling/tree-sitter-pho/grammar.js`. For runtime semantics — how
scopes resolve, when functions capture which environment, how the
loader stitches packages together — read `pkg/core/` end to end. This
document is the friendly tour; those are the source of truth.
