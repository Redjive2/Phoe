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

"'" @keyword.operator
"&" @keyword.operator
"!" @keyword.operator


; ----- Comments -----

(comment) @comment


; ----- Special forms (binding / control / module) -----
; When these identifiers appear as the head of a list, they are
; the language's special forms, not regular function calls.

((list . (identifier) @keyword)
 (#any-of? @keyword
   "fun" "method" "struct" "property"
   "var" "const"
   "if" "unless" "foreach" "while" "until" "do" "block"
   "return" "break" "continue"
   "import" "goimport"
   "="))


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
 (#any-of? @keyword.operator "and" "or"))


; ----- Builtin functions (head of a list) -----

((list . (identifier) @function.builtin)
 (#any-of? @function.builtin
   "get" "has" "slice" "map" "len" "append" "drop" "range" "keyof" "mod"
   "pause" "resume" "inspect" "identity" "spread" "optional"))


; ----- Macro calls -----
; The first identifier inside a macro_call is the macro name.

(macro_call . (identifier) @function.macro)


; ----- Macro definitions -----
; (macro name! (params) body) — the `macro` head reads like the other
; binding special forms (fun/struct/…); the declared name is painted like
; the macro it introduces, matching the call-site color above.

(macro_definition "macro" @keyword)

(macro_definition name: (identifier) @function.macro)


; ----- User-defined function calls (deliberately not tagged) -----
;
; The natural pattern would be:
;
;     (list . (identifier) @function.call)
;
; but Zed's tree-sitter resolves the `.` anchor permissively, matching
; the first IDENTIFIER child rather than the first named child of any
; kind. That tags parameter names inside quoted arg lists like
; `(fun 'name '(arg1 arg2) ...)` and `(fun 'name '((spread x) y) ...)`
; as if they were function calls.
;
; Until we have a way to filter by ancestor (e.g. "list, but not under
; a quote"), we let user-defined call heads inherit the default
; identifier color via `(identifier) @variable` near the top of this
; file. Builtins and special forms are still picked up by their
; specific patterns above.


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
; sides are bare numbers, which is exclusively the decimal case — a
; dynamic index is bracketed (`xs.[5]`), so the inner number lives in an
; array node and never matches this two-number dot_chain pattern.

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
