; ============================================================
;  Pho folds queries
;
;  Marks the spans an editor's "fold this block" command can
;  collapse. We fold every container — list, macro call, array,
;  dict — so any non-trivial form can be folded down to a single
;  line.
; ============================================================

[
  (list)
  (macro_call)
  (array)
  (dict)
] @fold
