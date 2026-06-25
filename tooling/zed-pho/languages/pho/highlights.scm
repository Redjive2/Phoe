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
; Pho convention: package exports must be capitalized, and
; structs use PascalCase. Anything starting with an uppercase
; letter is most likely a type / constructor / public function.
;
; This is a heuristic — `locals.scm` (Phase 1b) will refine.

((identifier) @type
 (#match? @type "^[A-Z]"))

; A capitalized identifier in list-HEAD position is a call to an exported
; function (Pho requires exported names to capitalize), not a type — recolor it
; from the @type heuristic above. Capitalized type-VALUE names in argument
; position keep @type; the capitalized type operators (Or/And/…) are re-tagged
; @function.builtin in the builtin-functions block below (which comes later and
; therefore wins for those specific names).
((list . (identifier) @function.call)
 (#match? @function.call "^[A-Z]"))


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


; ----- Sigils for sugar forms -----

"&" @keyword.operator
"~" @keyword.operator


; ----- Comments -----

(comment) @comment


; ----- Special forms (binding / control / module) -----
; When these identifiers appear as the head of a list, they are
; the language's special forms, not regular function calls.

((list . (identifier) @keyword)
 (#any-of? @keyword
   "fun" "method" "struct" "property" "static" "trait" "type"
   "var" "const"
   "if" "unless" "foreach" "while" "until" "do" "block"
   "return" "break" "continue"
   "import" "goimport"
   "="))


; `static method …` / `static property …`: the modifier is the head (tagged
; above), so the inner method/property head is the SECOND child — tag it too so
; it matches a standalone `(method …)`/`(property …)`.
((list . (identifier) @_static . (identifier) @keyword)
 (#eq? @_static "static")
 (#any-of? @keyword "method" "property"))


; ----- control-form keyword markers -----
; then/elif/else (if/unless), in (foreach), and then (while/until) are bare
; identifiers that mark the operands of a control form. They aren't list
; heads, so the special-forms rule above doesn't catch them; tag them
; wherever they appear (like and/or).

((identifier) @keyword
 (#any-of? @keyword "then" "in" "elif" "else"))

; ----- property accessor keywords -----
; `get` / `set` mark the accessors of `(property Recv.Name get … set …)`.
; `get` is ALSO the collection builtin `(get coll key)` — but this rule comes
; BEFORE the builtin-functions rule below, so a head-position `get` is
; re-tagged @function.builtin there while a non-head `get`/`set` marker keeps
; this @keyword tag.

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
   "get" "has" "append" "drop" "range" "mod"
   "inspect" "identity" "spread" "optional"
   "list?" "atom?" "atom" "atom_name"
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
; CAPITALIZED call heads ARE tagged @function.call (the rule next to the
; capitalized-@type heuristic near the top) — Pho requires exported names to
; capitalize, so a capitalized list head is an exported-function call.
;
; LOWERCASE user-call heads are deliberately left at the default @variable: the
; param-list-aware ancestor filtering that would let us tag them safely isn't
; available, and a bare `(list . (identifier) @function.call)` would mis-tag the
; first identifier of other lists. Builtins and special forms are still picked
; up by their specific patterns above.


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
 (#match? @type "^[A-Z]"))


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


; ----- Soft keywords -----
; `self` is the conventional method-receiver name. Painted with the
; same scope as len/drop/etc. so it gets a single distinctive color
; everywhere it appears — param list, body reference, dot-chain LHS.
;
; This block sits at the very bottom of the file because the
; dot_chain rules above tag the leftmost identifier in `a.b.c` as
; @variable, which would otherwise win for `self.x` and erase the
; soft-keyword highlight. Per the file's "later patterns override
; earlier ones" convention, putting this last makes it the most
; specific match for any identifier with text `self`.

((identifier) @function.builtin
 (#eq? @function.builtin "self"))


; ----- `do` keyword everywhere -----
; do-notation makes a bare `do` capture the trailing siblings of its enclosing
; form, so it reads as a keyword in every position, not just at a list head.
; Last (like `self`) so it beats the dot_chain @variable fallback.

((identifier) @keyword
 (#eq? @keyword "do"))


; ----- Universal membership methods -----
; `Is?` / `In?` are the universal type-test methods (x.Is? T, x.In? coll). Give
; them one consistent color in every position — without this they'd be tagged
; @property after a dot but @type via the capitalized heuristic at the prefix.
; Last so it wins over both.

((identifier) @function.method
 (#any-of? @function.method "Is?" "In?"))
