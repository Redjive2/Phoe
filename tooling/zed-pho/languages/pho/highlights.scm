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
   "fun" "method" "struct"
   "var" "const"
   "if" "for" "do" "block"
   "return" "break" "continue"
   "import" "goimport"
   "="))


; ----- Boolean operators that look like identifiers -----

((identifier) @keyword.operator
 (#any-of? @keyword.operator "and" "or"))


; ----- Builtin functions (head of a list) -----

((list . (identifier) @function.builtin)
 (#any-of? @function.builtin
   "get" "has" "slice" "map" "len" "append" "drop" "range"
   "pause" "resume" "inspect" "spread"))


; ----- Macro calls -----
; The first identifier inside a macro_call is the macro name.

(macro_call . (identifier) @function.macro)


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
