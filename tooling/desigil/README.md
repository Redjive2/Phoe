# desigil — the de-sigiling codemod

One-time migration tool for the cutover that makes the bare form the *only*
legal syntax in the declaration and control builtins, removing the `'` and
`&` sigils that were previously mandatory:

```
(fun 'add '(x y) '(+ x y))   ->   (fun add (x y) (+ x y))
(if c &then &else)           ->   (if c then else)
(for 'e xs &body)            ->   (for e xs body)
```

## The rule it encodes

A sigil is removed **only** when it sits in a *structural* slot of a
recognized form — a slot that is always a static name, parameter list,
field list, or deferred body, never a runtime value. The affected forms and
slots are: `fun`/`method`/`struct` (name, params, fields, body), `var`/
`const` (names), `=` (target), `if` (arms), `for` (loop var, cond, body).

Every **value position** is left exactly as written, because something else
(a number, a string, an expression) could legitimately appear there:

- map / dict keys and values, array elements
- import / goimport aliases (`'dep` inside `["mod" 'dep]`)
- struct-initializer dict keys (`(Point {'X 10})`)
- `'symbol` values passed as arguments (`(Split xs 3 'strict)`)
- any genuine quote-as-data, and `&thunk` values passed to functions

The subtle case the rule gets right: `(if c &'overwrite &mode)` becomes
`(if c 'overwrite mode)` — the structural `&` (deferral) is dropped, but the
`'overwrite` value-quote stays.

## How it works

Surgical, format-preserving deletion: the source is parsed with the real
positioned parser (`pkg/syntax`), and each target sigil's single byte is
deleted by source position. Nothing else — whitespace, comments, layout —
is touched. A file with any parse error is refused (returned unchanged), and
every deletion is verified to actually land on a `'`/`&` byte, so a position
bug can never silently corrupt a file. Running twice is a no-op.

## Usage

```
go build -o /tmp/desigil ./tooling/desigil
find script -type f \( -name '*.pho' -o -name '*.phl' \) -print0 | xargs -0 /tmp/desigil      # dry run
find script -type f \( -name '*.pho' -o -name '*.phl' \) -print0 | xargs -0 /tmp/desigil -w   # apply
```

The `-go` flag migrates the Pho embedded in Go string literals (test
fixtures): it parses each Go file, and rewrites only the string literals
whose contents parse cleanly as Pho and change. Concatenated multi-part
strings (each fragment doesn't parse alone) and cursor-marker fixtures
(e.g. `p.`) are skipped — those were hand-migrated.

## Status: cutover complete

The de-sigiling cutover landed. Builtins read bare forms, the linter
accepts (and requires) them, the stdlib and all test fixtures are
migrated, and `go test ./...` is green.

- Unit tests in `desigil_test.go` cover every structural strip and pin the
  value-position no-ops (map keys, import aliases, struct-init dicts, data
  quotes, `&` thunks). `go test ./tooling/desigil/`.
- The whole stdlib migrated cleanly (no parse errors, no mismatch aborts);
  surviving sigils are all value-position.
- **Runtime golden (`testdata/conformance.{pho,golden}`):** a deterministic
  program exercising every de-sigiled form plus the value-position cases
  (map keys, `'symbol` values, struct-init dicts) and a `(identity do …)`
  body. Run it from a directory with `std/` alongside; its stdout must match
  `conformance.golden` byte-for-byte. This is a **manual** check — no test in
  `go test ./...` wires it up, so it won't catch regressions on its own.
