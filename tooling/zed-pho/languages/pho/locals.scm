; ============================================================
;  Pho locals queries  (post-cutover bare syntax)
;
;  Tells tree-sitter-aware editors which identifiers open scopes,
;  bind names, and reference them. Post-cutover the '/& sigils are
;  gone: declaration names and parameter lists are bare, and a
;  method/property name is an `Owner.Name` dot_chain.
;
;     @local.scope                — node that opens a fresh scope
;     @local.definition.<kind>    — where a name is bound
;     @local.reference            — every other identifier use
;
;  Pho also ships a full LSP (cmd/pho-lsp), which is the authoritative
;  source for go-to-definition / references; these queries are the
;  best-effort fallback for tree-sitter-only scope coloring. Two cases
;  the grammar can't express here and that the LSP covers instead:
;  multi-binding `(var a 1 b 2)` (only the first name is anchorable,
;  since a bare name is structurally identical to a value) and
;  spread/optional parameters wrapped in `(spread name)` / `(optional name)`.
; ============================================================


; ----- Scopes -----
;
; The whole file is the outermost scope. Function and method bodies
; introduce inner scopes. `block` (&expr) does NOT — the runtime runs
; it in the caller's env, so an `if`/loop arm sees the enclosing bindings.

(source_file) @local.scope

((list . (identifier) @_kw)
 (#any-of? @_kw "fun" "method")) @local.scope


; ----- Function / method / struct names -----

; (fun Name (args) body) — named only; anonymous (fun (args) body) has a
; parameter list, not an identifier, in the name slot.
((list . (identifier) @_kw . (identifier) @local.definition.function)
 (#eq? @_kw "fun"))

; (method Owner.Name (args) body) — named only (an anonymous delegate's
; receiver is a bare identifier, not a dot_chain).
((list . (identifier) @_kw . (dot_chain (identifier) (identifier) @local.definition.method))
 (#eq? @_kw "method"))

; (struct Name field ...)
((list . (identifier) @_kw . (identifier) @local.definition.constructor)
 (#eq? @_kw "struct"))


; ----- Bindings: var / const -----
;
; (var a 1 b 2) interleaves bare names and values. Only the first name is
; positionally anchorable (a bare name looks just like a value), so a
; multi-binding form captures only its first name here; the LSP resolves
; the rest. The common single-binding (var x 5) works.

((list . (identifier) @_kw . (identifier) @local.definition.var)
 (#any-of? @_kw "var" "const"))


; ----- Function / method parameters -----
;
; The parameter list is the bare (list ...) sitting just after the name;
; each direct identifier child is a parameter.

; (fun Name (params) body)
((list . (identifier) @_kw . (identifier) . (list (identifier) @local.definition.parameter))
 (#eq? @_kw "fun"))

; (fun (params) body) — anonymous
((list . (identifier) @_kw . (list (identifier) @local.definition.parameter))
 (#eq? @_kw "fun"))

; (method Owner.Name (params) body)
((list . (identifier) @_kw . (dot_chain) . (list (identifier) @local.definition.parameter))
 (#eq? @_kw "method"))

; (method Owner (params) body) — anonymous delegate
((list . (identifier) @_kw . (identifier) . (list (identifier) @local.definition.parameter))
 (#eq? @_kw "method"))


; ----- Imports -----
;
; (import "path")            — alias is the path basename (no binding node here)
; (import ("path" alias))    — explicit bare alias

((list . (identifier) @_kw . (list (string) (identifier) @local.definition.namespace))
 (#any-of? @_kw "import" "goimport"))


; ----- References -----
;
; Every identifier not captured as a definition above. The resolver picks
; the nearest enclosing definition — local params first, then enclosing
; scope, then file scope.

(identifier) @local.reference
