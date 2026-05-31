; ============================================================
;  Pho indent queries
;
;  Tells tree-sitter-aware editors how to auto-indent inside
;  unfinished forms. Pressing Enter inside an open `(`, `[`, or
;  `{` indents the next line; typing the matching closer dedents
;  back to the parent level.
; ============================================================


; ----- Containers that open an indented block -----

[
  (list)
  (macro_call)
  (array)
  (dict)
] @indent


; ----- Tokens that close an indented block -----

[
  ")"
  "]"
  "}"
] @end
