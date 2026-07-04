; ============================================================
;  Pho highlight queries
;
;  Conventions follow the de-facto tree-sitter / Neovim
;  highlight-group taxonomy. Editors that map these to their
;  own theme groups should pick up sensible colors out of the
;  box.
;
;  Order matters: later patterns override earlier ones for the
;  same node. General fallbacks come first; specific overrides
;  come last.
; ============================================================


; ----- Generic identifier fallback -----

(identifier) @variable


; ----- Atoms -----

(number)    @number
(string)    @string
(character) @character
(atom)      @string.special.symbol
(bool)      @constant.builtin.boolean
(nil)       @constant.builtin


; ----- Capitalized identifiers as types/constructors -----
; Post-cutover, CASING marks the type/value split (it no longer marks
; visibility — that is the `#` prefix now): a Title_Snake_Case name is a type
; or constructor, a snake_case name is a value/function. An optional leading
; `#` (a private type) doesn't change the casing test.
;
; This is a heuristic — `locals.scm` (Phase 1b) will refine.

((identifier) @type
 (#match? @type "^#?[A-Z]"))

; A capitalized identifier at list-HEAD position is NOT a value constructor call
; — post-cutover, construction is `Name.{ field = value }`, never `(Name …)` — so
; it keeps the @type paint above. That is what makes a signature's FIRST type
; argument in `(Number …)` read as a type rather than blue. The capitalized type
; operators (Or/And/…) are still re-tagged @function.builtin in the builtin block
; below (which comes later and therefore wins for those specific names).


; ----- Symbol operators -----

(operator) @operator


; ----- Punctuation: brackets and the dot -----

"(" @punctuation.bracket
")" @punctuation.bracket
"[" @punctuation.bracket
"]" @punctuation.bracket
"{" @punctuation.bracket
"}" @punctuation.bracket
"." @punctuation.delimiter

(range_separator) @punctuation.special

; `->` is the map-literal key/value separator inside `[ k -> v ]`.
(map_arrow) @punctuation.special


; ----- Sigils for sugar forms -----

"&" @keyword.operator
"~" @keyword.operator


; ----- Package navigation (slash chains) -----
; `a/b/c` navigates imported packages / subpackages down to an export — distinct
; from value member access (`.`). The INTERMEDIATE segments are namespaces (the
; theme may italicize them); the FINAL segment is the export being referenced —
; a function (kebab-case) or a type (Title-Kebab-Case) — and must read like any
; other function/type, NOT with the italic namespace style. The `/` separators
; are delimiters; the division operator `(/ a b)` keeps @operator (its `/` is an
; `operator` node, not a slash_chain child).
;
; Order matters (later wins): first paint EVERY segment as a function call,
; retag a Capitalized final as a type, then override the non-final segments (the
; leftmost, and every inner-chain RHS at any depth) back to @namespace. The final
; segment is neither leftmost nor an inner-chain RHS, so it keeps the
; function/type paint. (An effectful `!`/`?` final is retagged @function.method
; by the trailing-sigil rule at the very bottom of the file — also non-italic.)

(slash_chain (identifier) @function.call)

((slash_chain (identifier) @type)
 (#match? @type "^#?[A-Z]"))

(slash_chain . (identifier) @namespace)

(slash_chain
  (slash_chain (_) (identifier) @namespace))

(slash_chain "/" @punctuation.delimiter)


; ----- Comments -----

(comment) @comment


; ----- Special forms (binding / control / module) -----
; When these identifiers appear as the head of a list, they are
; the language's special forms, not regular function calls.

((list . (identifier) @keyword)
 (#any-of? @keyword
   "fun" "method" "struct" "property" "static" "trait" "template" "type"
   "let" "var" "const"
   "if" "unless" "foreach" "while" "until" "do" "block" "select"
   "return" "break" "continue"
   "import" "goimport"
   "="))


; `static method …` / `static property …`: the modifier is the head (tagged
; above), so the inner method/property head is the SECOND child — tag it too so
; it matches a standalone `(method …)`/`(property …)`.
((list . (identifier) @_static . (identifier) @keyword)
 (#eq? @_static "static")
 (#any-of? @keyword "method" "property"))


; The `var` mutability modifier in `(let var x = v)` is the SECOND child after
; the `let` head, so the list-head keyword rule above misses it — tag it too.
((list . (identifier) @_let . (identifier) @keyword)
 (#eq? @_let "let")
 (#eq? @keyword "var"))


; ----- control-form keyword markers -----
; then/elif/else (if/unless), in (foreach), and then (while/until) are bare
; identifiers that mark the operands of a control form. They aren't list
; heads, so the special-forms rule above doesn't catch them; tag them
; wherever they appear (like and/or).

((identifier) @keyword
 (#any-of? @keyword "then" "in" "elif" "else" "where" "case"))

; ----- property accessor keywords -----
; `get` / `set` mark the accessors of `(property Recv.Name (get …) (set …))`.
; They are LIST HEADS in the current form, so `get` (unlike a bare marker) would
; otherwise be caught by the builtin-functions rule below — but the unused `get`
; collection builtin is intentionally NOT in that list, so `get`/`set` stay @keyword
; (purple) everywhere they appear.

((identifier) @keyword
 (#any-of? @keyword "get" "set"))


; ----- Boolean operators that look like identifiers -----

((identifier) @keyword.operator
 (#any-of? @keyword.operator "and" "or" "not"))


; ----- Builtin functions (head of a list) -----

; (slice/map are intentionally absent: they are mangled internal heads behind
;  the `[…]`/`{…}` literals, not callable builtins, so `(slice …)`/`(map …)`
;  are ordinary unresolved calls, never highlighted as builtins.)
((list . (identifier) @function.builtin)
 (#any-of? @function.builtin
   "has" "append" "drop" "range" "mod"
   "inspect" "identity" "spread" "optional"
   "list?" "atom?" "atom" "atom-name"
   ; type operators / first-class type constructors (gradual typing)
   "subtype?" "Or" "And" "Not" "Diff" "Fun" "Struct" "Trait"))


; ----- Macro calls -----
; The first identifier inside a macro_call is the macro name.

(macro_call . (identifier) @function.macro)


; ----- Macro definitions -----
; (macro ~name (params) body) — the `macro` head reads like the other
; binding special forms (fun/struct/…); the declared name (after the `~`) is
; painted like the macro it introduces, matching the call-site color above.

(macro_definition "macro" @keyword)

(macro_definition name: (identifier) @function.macro)


; ----- User-defined function calls -----
;
; NEITHER capitalized nor lowercase list heads are tagged @function.call. A
; Title_Snake_Case head keeps its @type paint from the heuristic near the top
; (post-cutover it is a type, never a `(Name …)` constructor call — construction
; is `Name.{ field = value }`). A snake_case head is left at the default
; @variable — public or private (`#`) is irrelevant to highlighting here.
;
; The reason we do NOT paint list heads @function.call at all: the param-list-
; aware ancestor filtering that would let us tag real calls safely isn't
; available, and a bare `(list . (identifier) @function.call)` mis-tags the first
; identifier of OTHER lists — notably the FIRST type in a signature's arg-list
; `(Number …)`, which is what made it read blue. Builtins and special forms are
; still picked up by their specific patterns above.


; ----- Dot-chain segments -----
; The grammar models `a.b.c` as nested binary dot_chains —
;   dot_chain(dot_chain(a, b), c)
; — so we need two queries:
;   1. Tag every identifier that appears AFTER a `.` as a
;      property (catches `b` and `c` in a.b.c).
;   2. Tag the leftmost receiver as a variable (catches the
;      `a` in a.b.c — only when the receiver is a bare ident).

(dot_chain
  "."
  (identifier) @property)

(dot_chain
  .
  (identifier) @variable)

; `Struct.{ field value … }` is construction sugar (a capitalized receiver
; before a brace builds that struct; the brace's bare keys are field names).
; Paint the receiver as the constructor it is, overriding the generic
; dot-chain receiver @variable just above.
((dot_chain
  .
  (identifier) @type
  (dict))
 (#match? @type "^#?[A-Z]"))


; ----- Decimal literals -----
; In `1.5`, paint the dot the same color as the numbers so the literal
; reads as one value instead of three tokens. The grammar sees a
; dot_chain of two numbers; the runtime reassembles it through the
; `Dot` operator's number-RHS hack. We only retag the dot when both
; sides are bare numbers — `xs.5` (array index) keeps the regular dot
; color, since it really *is* a member access.

(dot_chain
  (number)
  "." @number
  (number))


; ----- Receiver: self value + Self type -----
; `self` is the method receiver PARAMETER and `Self` is that receiver's TYPE
; (used in signatures / static bodies). Both are painted @variable.parameter so
; the type reads the SAME as the value everywhere they appear (param list, body
; reference, dot-chain LHS, signature slot) — NOT a @function color (which made
; `self` read as a call) nor the @type color the capitalized heuristic would
; otherwise give `Self`.
;
; This block sits at the very bottom of the file because the dot_chain /
; capitalized-@type rules above would otherwise win for `self.x` / `Self`. Per
; the file's "later patterns override earlier ones" convention, putting this last
; makes it the most specific match for any identifier named `self` or `Self`.

((identifier) @variable.parameter
 (#any-of? @variable.parameter "self" "Self"))


; ----- `do` keyword everywhere -----
; do-notation makes a bare `do` capture the trailing siblings of its enclosing
; form, so it reads as a keyword in every position, not just at a list head.
; Last (like `self`) so it beats the dot_chain @variable fallback.

((identifier) @keyword
 (#eq? @keyword "do"))


; ----- Effectful (!) and predicate (?) identifiers -----
; A trailing `!` marks an effectful callable and a trailing `?` a predicate
; (Effects.md + the `?` naming convention). Give both one consistent, distinct
; color in EVERY position — receiver, call head, argument, or member after a dot
; — so effects and tests read loud at each site. This also covers the universal
; is?/in? membership methods. Last in the file so it wins over the
; @variable / @property / @type fallbacks for these names.

((identifier) @function.method
 (#match? @function.method "[!?=]$"))
