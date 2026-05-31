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
  or imports — `import`, `goimport`, `fun`, `method`, `struct`, `const`.
  Side-effecting forms are rejected before they run. This makes `.phl`
  files cheap to import: loading one can't, by construction, perform
  I/O or mutate global state.

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
- A **boolean** — `True` or `False`.
- **Nil** — `Nil`.
- An **identifier** — `myVar`, `Point`. Capitalized identifiers are the
  ones a package exports.
- A **list** (a call) — `(f a b c)`. Calls `f` with arguments `a`, `b`,
  `c`.

Comments run from `--` to end of line:

```pho
-- top-level comment
(io.PrintLine "hello") -- trailing comment
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

### Dot access — `a.b`

The accessor. Behavior depends on what `a` is:

| LHS | `a.b` means |
|---|---|
| dict | look up key `b` |
| array | index, or slice (with `[i : j]` on the RHS) |
| string | the rune at index `b` |
| struct instance | field `b`, or method `b` |
| package | the export named `b` |
| Go-package | the Go method named `b`, wrapped as a callable |
| number | reassemble as a decimal (`12.34`) |

Chains naturally: `pkg.Struct.method`. The `.` is its own token, so
`12.34` and `obj.field` go through the same machinery — that's why
`1.5` parses as "number, dot, number" and arrives as `1.5`.

### Quote — `'expr`

Treats `expr` as data instead of evaluating it. Function definitions
use this:

```pho
(fun 'add '(x y) '(+ x y))
```

Three quoted forms — name, parameter list, body — handed to `fun` as
data, evaluated only when `fun` decides to.

### Block — `&expr`

Defers evaluation. Used for `if` and `while` branches:

```pho
(if (< x 10)
    &(io.PrintLine "small")
    &(io.PrintLine "big"))
```

`&body` desugars to `(block 'body)` — a no-arg function the surrounding
form invokes when it's time.

### Macros — `(name! arg1 arg2)`

A function call where `!` follows the head name passes the args
*quoted* (as data), then re-evaluates the result:

```pho
(myMacro! a b c)   -- shorthand for (resume (myMacro 'a 'b 'c))
```

Used to write functions that take code as input — the usual Lisp macro
trick.

### Spread — `(spread items)`

Splats an array into argument positions:

```pho
(io.PrintLine (spread items))   -- like JS's PrintLine(...items)
```

In a parameter list, `(spread name)` collects trailing args into
`name`:

```pho
(fun 'PrintAll '((spread args))
    '(io.PrintLine (spread args)))
```

## Main builtins

The forms you'll touch in nearly every Pho program.

### `var` / `const` — bindings

```pho
(var 'x 5)                 -- inside fun/method bodies, or at the top of a .pho script
(const 'PI 3)              -- anywhere, including top level
(var 'a 1 'b 2)            -- multiple at once
```

`var` is mutable, `const` isn't. Constants refuse mutation and report
an error if you try. **`var` at the top level is rejected in `.phl`
library files** — package-visible bindings have to be immutable so the
linter can do cross-file reasoning without tracking mutation. Scripts
(`.pho`) and function bodies have no such constraint: `var` is fine
wherever side effects are.

### `=` — assignment

```pho
(= 'x 10)        -- assign to a var
(= p.X 100)      -- assign to a struct field
```

### `fun` — function definition

```pho
(fun 'double '(n) '(* n 2))
```

Three quoted args: name, parameter list, body. The 2-arg form (no
name) returns the function as a value:

```pho
(var 'double (fun '(n) '(* n 2)))
```

A function captures the file and package it was defined in, so its body
sees the right imports even when called from another package.

### `if` / `while` / `do`

```pho
(if cond &thenBranch &elseBranch)         -- elseBranch optional
(while &cond &body)
(do expr1 expr2 expr3)                    -- evaluates each, returns last
```

The `&` on branches is mandatory — they need to be deferred so they
don't evaluate before `if`/`while` decides what to do.

### `and` / `or` / `~`

Boolean operators. `and` and `or` short-circuit; `~` is logical not.

```pho
(and (> x 0) (< x 10))
(~ True)                  -- False
```

### Arithmetic and comparison

`+ - * /` for numbers. `+` also concatenates strings if all args are
strings. `==`, `~=`, `<`, `<=`, `>`, `>=` for comparison; `==` and `~=`
do *deep* equality on arrays and dicts.

```pho
(+ 1 2 3)                          -- 6
(+ "hello " "world")               -- "hello world"
(== [1 2 3] [1 2 3])               -- True
```

### `import` / `goimport`

Bring a package into the *current file*:

```pho
(import "std/io")              -- aliased as `io`
(import ["std/io" 'myio])      -- explicit alias

(goimport ["stdDependencies" 'dep])
```

`import` loads a Pho package (a directory of `.pho` files); `goimport`
binds a Go-side module the host has registered. Either way, the alias
is visible only in this file.

## Other builtins

The rest, grouped by what they're for.

### Structs and methods

```pho
(struct 'Point '(X y))

(method Point 'Shift '(self d) '(+ self.X d))
(method Point 'tweak '(self d) '(+ self.y d))

(var 'p (Point { 'X 10 'y 20 }))
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
| `(len arr)` | Array length. |
| `(append arr x y z)` | New array with extra tail elements. |
| `(drop arr n)` | New array minus the first `n` elements. |

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
