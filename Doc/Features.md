# Features to be added

Natural-language builtin structure:
```pho
(if   cond then expr      ---DONE --- new 'elif'/'else' branching; new 'then' keyword
 elif cond then expr
 else      expr)

(foreach element in list do ...)  ---DONE --- split up 'for': 'foreach' handles list iteration; new 'in' keyword

(while cond then do ...)          ---DONE --- split up 'for': 'while' handles conditions

(until cond then do ...)          ---DONE --- split up 'for': opposite of 'while'

(unless cond then expr    ---DONE --- new 'unless' builtin; no elif
        else expr )
```
NOTE: in both `if` and `unless`, the final `else` arm takes exactly ONE expression with NO `then` keyword after `else` (ctrl.go: `else` requires the remaining args to be `else <expr>`).

Improved method/macro syntax:
```pho
(method receiver.name (<args>) do ...)  ---DONE--- named methods use the `Receiver.Name` dot-pattern; also anonymous `(method Receiver (self) do …)` form.

(macro stuff! (<args>) do ...)          ---DONE--- uses the macro keyword with a *mandatory* `!` in the declaration; calling `(stuff! <args>)` lowers to the mangled `core.Macrocall` (no longer `(resume (stuff <args>))`). The `!` is enforced at runtime and by the linter (not-a-macro / macro-needs-bang). The mangled head carries a randomized per-process suffix, so it can never be written or invoked directly.
```

New builtins:
```pho
(property <receiver.>name get getter <set setter>) -- creates a faux field/variable backed by anonymous functions/methods; receiver and setter optional   ---DONE--- read/write delegate via leaf/dot/= ; also added anonymous methods `(method Receiver (self) do …)`. Editor: semantic tokens (@property + get/set @keyword), hover, dot-completion of property members, and document-symbol nesting all wired.

(debug ...)  -- runs a given expression only in debug mode
(assert ...) -- runs an assertion; crashes on fail
(static ...) -- runs a given expression at parse-time and fills in the result as a literal
```
NOT YET IMPLEMENTED: there is no `debug`, `assert`, or `static` builtin registered in pkg/builtins today — these remain roadmap items (no `---DONE---`).

New immutable data structures:
```pho
:[1 2 3]           -- tuple: cannot mutate, otherwise same as a list

:{ "key" "value" } -- constant map: cannot mutate, otherwise same as a map
```
NOT YET IMPLEMENTED: the lexer does not recognize `:[...]` or `:{...}`. A `:` is only glued into an atom when immediately followed by an identifier-start char or digit (positioned.go); before `[` or `{` the `:` lexes as a standalone separator token, so this syntax would not parse today.

Extended object model:
```pho
(method Number.Square (self) do   -- methods and properties can be defined on primitive types
    (* self self))

([1 2 3].Size) -- You can access these methods, and properties directly on literals. This replaces things like (len list) and (keyof list) with (list.Size) and (list.Keys)
```
NOT YET IMPLEMENTED: `method` requires its receiver to evaluate to a struct `Constructor` (decl.go type-checks `core.Constructor`), so `(method Number.Square ...)` errors with ErrType — methods cannot be defined on primitive types today. There are no `.Size`/`.Keys` member methods either: collection size/keys are the standalone builtins `len` and `keyof` (coll.go), so `[1 2 3].Size` is not implemented. The dot accessor's `KindNum` case is ONLY the fractional-decimal reassembly hack (e.g. `1.5`), not method dispatch.

Context-aware do & reworked block helper:
```pho
(if   cond then do a b     ---DONE (do part)--- a non-head `do` stops at the next elif/else boundary, so each arm splits into its own (do …) block (lower.go splitDoForm). NOTE: the if-form is `else <expr>` (no `then` after `else`); write the last arm `else do d e f`.
 elif cond then do c 
 else      do d e f)

(list.Map &(+ it 1))         ---DONE--- `&expr` is a one-arg function whose implicit param is `it` (the `block` builtin binds `?it`; lower.go/walker/semantic/completion wired).
(list.Map &Nil)              ---DONE--- literals work too — `it` is optional, so a literal block ignores its argument.
(list.Map &do                ---DONE--- `&do` captures the REST of the form into the block's do-body (splitDoForm rewrites `&do a b` → `&(do a b)`); must be the last argument.
    (const plusOne (+ it 1)
    (* plusOne 2))
```

Some important builtin methods:
NOT YET IMPLEMENTED: this whole block is illustrative future syntax. It depends on methods-on-primitives, dot dispatch on numbers, and `typeof`/`Number`/`Boolean`/`Type`/`Unknown` type values — none of which exist today (core only tracks lowercase kind strings like "num"). The annotations below use `sig!`, the real annotation macro (std/annot defines exactly: type, doc, desc, macrohint, pure, flag, sig — there is no `methodsig`).
```pho
--@ (sig! Unknown Collection -> Boolean)
(method Unknown.In? (self collection) do
    (foreach value in collection
        (if (== self value) then 
            (return True)
        )
    )
    
    False
)

--@ (sig! Unknown Type -> Boolean)
(method Unknown.Is? (self type)
    (== (typeof self) type)
)

(1.Is? Number) -- True
("hi".In? )
```

