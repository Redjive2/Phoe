; Outline view items.
;
; Recognizes the top-level definition forms in the post-cutover bare syntax
; (names are plain identifiers, not `'quoted` symbols; a method/property name
; is a `Owner.Name` dot_chain):
;   (fun Name (args) body)         function
;   (struct Name field ...)        struct
;   (method Owner.Name (args) …)   method on a struct
;   (property Owner.Name get …)    computed property
;   (macro ~Name (params) body)    macro
;
; Capture groups:
;   @item     — the entire define form
;   @context  — dimmed prefix: the head keyword (fun/struct), or the owner
;               struct name for a method/property
;   @name     — the symbol that identifies the definition

; (fun Name (args) body) — named only; an anonymous (fun (args) body) has a
; parameter list, not an identifier, in the name slot, so it is not an item.
(list
  .
  (identifier) @context
  .
  (identifier) @name
  (#eq? @context "fun")
) @item

; (struct Name field ...)
(list
  .
  (identifier) @context
  .
  (identifier) @name
  (#eq? @context "struct")
) @item

; (trait Name member ...) — a named trait; members follow as nested lists.
(list
  .
  (identifier) @context
  .
  (identifier) @name
  (#eq? @context "trait")
) @item

; (template Name) / (template Name (bounds ...)) — a generic type parameter.
; The optional bounds list follows the name and is ignored here.
(list
  .
  (identifier) @context
  .
  (identifier) @name
  (#eq? @context "template")
) @item

; (method Owner.Name (args) body) — named only (an anonymous (method Owner …)
; delegate has a bare receiver, not a dot_chain). The owner shows as context.
(list
  .
  (identifier) @_kw
  .
  (dot_chain (identifier) @context (identifier) @name)
  (#eq? @_kw "method")
) @item

; (property Owner.Name get getter [set setter]) — the owner shows as context.
(list
  .
  (identifier) @_kw
  .
  (dot_chain (identifier) @context (identifier) @name)
  (#eq? @_kw "property")
) @item

; (macro ~Name (params) body)
; A macro_definition is its own grammar node, not a list; the name is a bare
; identifier after the `~` prefix, and the `macro` keyword is the @context head.
(macro_definition
  "macro" @context
  name: (identifier) @name
) @item

; (static method Owner.Name (args) body) / (static property Owner.Name get …)
; The `static` modifier leads; the owner shows as context, the member as name —
; so a static member appears in the outline like a plain method/property.
(list
  .
  (identifier) @_static
  .
  (identifier) @_kind
  .
  (dot_chain (identifier) @context (identifier) @name)
  (#eq? @_static "static")
  (#any-of? @_kind "method" "property")
) @item
