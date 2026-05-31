; Outline view items.
;
; Recognizes the three top-level definition forms:
;   (fun 'Name ...)          function
;   (struct 'Name '(fields)) struct
;   (method Owner 'Name ...) method on a struct
;
; In the tree-sitter grammar, `'Name` parses as a `quote` containing an
; `identifier`. (The runtime's CompressCodeLiterals pass turns it into a
; string at evaluation time, but tree-sitter sees the source form.)
;
; Capture groups:
;   @item     — the entire define form
;   @context  — the head keyword (fun / struct / method)
;   @name     — the symbol that identifies the definition

; (fun 'Name ...)
(list
  .
  (identifier) @context
  .
  (quote (identifier) @name)
  (#eq? @context "fun")
) @item

; (struct 'Name ...)
(list
  .
  (identifier) @context
  .
  (quote (identifier) @name)
  (#eq? @context "struct")
) @item

; (method Owner 'Name ...)
(list
  .
  (identifier) @context
  .
  (identifier) @item.context
  .
  (quote (identifier) @name)
  (#eq? @context "method")
) @item
