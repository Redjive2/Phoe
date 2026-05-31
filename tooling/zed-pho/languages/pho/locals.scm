; ============================================================
;  Pho locals queries
;
;  Tells tree-sitter-aware editors which identifiers are scopes,
;  definitions (functions, params, vars, struct/method names),
;  and references. With this in place, an editor can color
;  parameter uses differently from package-level globals, jump
;  from a use to its definition, and find all uses of a name —
;  all without needing an LSP for purely-local cases.
;
;  The standard captures are:
;     @local.scope                — node that opens a fresh scope
;     @local.definition.<kind>    — where a name is bound
;     @local.reference            — every other identifier use
; ============================================================


; ----- Scopes -----
;
; The whole file is the outermost scope. Function and method
; bodies introduce inner scopes; everything inside (params, var
; bindings, nested funs) is local to that scope.
;
; `block` (the &expr / (block 'expr) form) does NOT create a
; scope — the runtime's BindCallback runs in the caller's env,
; so the lexical model agrees: an `if` branch sees the same
; bindings as the enclosing fun.

(source_file) @local.scope

((list . (identifier) @_kw)
 (#any-of? @_kw "fun" "method")) @local.scope


; ----- Function and method definitions -----

; (fun 'name '(args) '(body))
((list . (identifier) @_kw
       . (quote (identifier) @local.definition.function))
 (#eq? @_kw "fun"))

; (method Owner 'name '(args) '(body))
((list . (identifier) @_kw
       . (identifier)
       . (quote (identifier) @local.definition.method))
 (#eq? @_kw "method"))


; ----- Struct definitions -----

; (struct 'name '(fields))
((list . (identifier) @_kw
       . (quote (identifier) @local.definition.constructor))
 (#eq? @_kw "struct"))


; ----- Bindings: var / const -----
;
; Both bind the name on the surrounding scope. `(var 'a 1 'b 2)`
; lists multiple at once — tree-sitter re-fires the pattern for
; each `'name` it finds, so all of them get captured.

((list . (identifier) @_kw
       . (quote (identifier) @local.definition.var))
 (#any-of? @_kw "var" "const"))


; ----- Function parameters -----
;
; Pho's parameter list is a quoted list of identifiers. tree-sitter
; matches the inner pattern once per identifier child, capturing
; each one as a parameter definition.

; (fun 'name '(arg1 arg2 ...) ...)
((list . (identifier) @_kw
       . (quote)
       . (quote (list (identifier) @local.definition.parameter)))
 (#eq? @_kw "fun"))

; (method Owner 'name '(self arg ...) ...)
((list . (identifier) @_kw
       . (identifier)
       . (quote)
       . (quote (list (identifier) @local.definition.parameter)))
 (#eq? @_kw "method"))

; Spread params: `(spread name)` inside a parameter list. The
; `name` identifier becomes the rest-arg binding.
((list . (identifier) @_kw
       . (quote)
       . (quote (list (list . (identifier) @_spread . (identifier) @local.definition.parameter))))
 (#eq? @_kw "fun")
 (#eq? @_spread "spread"))

((list . (identifier) @_kw
       . (identifier)
       . (quote)
       . (quote (list (list . (identifier) @_spread . (identifier) @local.definition.parameter))))
 (#eq? @_kw "method")
 (#eq? @_spread "spread"))


; ----- Imports -----
;
; (import "path")              — alias is the basename of the path
; (import ["path" 'alias])     — explicit alias
; (goimport ...)               — same shape

; Explicit alias form via array.
((list . (identifier) @_kw
       . (array (string) (quote (identifier) @local.definition.namespace)))
 (#any-of? @_kw "import" "goimport"))


; ----- References -----
;
; Every identifier that isn't captured as a definition above. The
; resolver picks the nearest enclosing definition — local params
; first, then enclosing scope, then file scope.

(identifier) @local.reference
