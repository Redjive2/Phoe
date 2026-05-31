package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasDiag reports whether any diagnostic in `diags` matches the given
// code, useful for regression checks where the exact span / message
// would be brittle to type out.
func hasDiag(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// hasDiagWithName reports whether any diagnostic in `diags` matches
// the given code and contains `name` in its message.
func hasDiagWithName(diags []Diagnostic, code, name string) bool {
	for _, d := range diags {
		if d.Code == code && strings.Contains(d.Message, name) {
			return true
		}
	}
	return false
}

// Sibling-file decls (fun / method / struct / const) should be visible
// to the linter so cross-file refs don't fire unresolved-identifier.
// Imports, however, are file-scoped — a sibling's import alias must
// stay invisible.
func TestPackageScopeResolvesCrossFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "pho-lint-pkg-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Sibling file: defines `helper`, imports `io` as `io`.
	sibling := filepath.Join(dir, "lib.phl")
	if err := os.WriteFile(sibling, []byte(`(import "std/io")
(const 'helper 42)
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Target file: refers to `helper` (visible) and to `io` (must NOT
	// be visible — sibling's import is file-scoped).
	target := filepath.Join(dir, "main.phl")
	src := []byte(`(const 'doubled (+ helper helper))
(const 'mystery io)
`)
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}

	diags := AnalyzeFile(target, src)

	// helper resolves -> no unresolved diag for it.
	if hasDiagWithName(diags, "unresolved-identifier", "helper") {
		t.Errorf("expected 'helper' to resolve via sibling file, got %#v", diags)
	}
	// io does NOT resolve -> unresolved diag for it.
	if !hasDiagWithName(diags, "unresolved-identifier", "io") {
		t.Errorf("expected 'io' (sibling-only import) to be unresolved, got %#v", diags)
	}
}

// ----------------------------------------------------------------------
// Block A regression coverage
// ----------------------------------------------------------------------

func TestUnusedImport(t *testing.T) {
	src := []byte(`(import "std/io")
(io.PrintLine "hi")
`)
	used := AnalyzeFile("test.pho", src)
	if hasDiag(used, "unused-import") {
		t.Errorf("did not expect unused-import on used import, got %#v", used)
	}

	src = []byte(`(import "std/io")
(fun 'main '() '(do))
`)
	unused := AnalyzeFile("test.pho", src)
	if !hasDiag(unused, "unused-import") {
		t.Errorf("expected unused-import on unreferenced alias, got %#v", unused)
	}
}

func TestInvalidSelfUsage(t *testing.T) {
	// `self` outside any method body is flagged.
	src := []byte(`(io.PrintLine self)
`)
	diags := AnalyzeFile("test.pho", src)
	if !hasDiag(diags, "invalid-self-usage") {
		t.Errorf("expected invalid-self-usage at top level, got %#v", diags)
	}

	// `self` inside a method body is not flagged.
	src = []byte(`(struct 'T '(x))
(method T 'foo '(self) '(do (io.PrintLine self.x)))
`)
	diags = AnalyzeFile("test.pho", src)
	if hasDiag(diags, "invalid-self-usage") {
		t.Errorf("did not expect invalid-self-usage inside method body, got %#v", diags)
	}

	// `self` inside a fun nested in a method is allowed (closure
	// captures the receiver).
	src = []byte(`(struct 'T '(x))
(method T 'foo '(self) '(do
  (fun 'inner '() '(io.PrintLine self.x))
  (inner)
))
`)
	diags = AnalyzeFile("test.pho", src)
	if hasDiag(diags, "invalid-self-usage") {
		t.Errorf("nested fun inside method should still allow self, got %#v", diags)
	}
}

func TestArityOnSpecialForms(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"fun-too-few", `(fun 'foo)`},
		{"fun-too-many", `(fun 'foo '(x) '(body) extra)`},
		{"struct-too-few", `(struct 'T)`},
		{"if-no-then", `(if cond)`},
		{"if-too-many", `(if cond &then &else &extra)`},
		{"= without value", `(= 'x)`},
		{"do-empty", `(do)`},
		{"var-odd-args", `(var 'a 1 'b)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := AnalyzeFile("test.pho", []byte(tc.src))
			if !hasDiag(d, "bad-form-arity") {
				t.Errorf("expected bad-form-arity for %q, got %#v", tc.src, d)
			}
		})
	}
}

func TestSigilShape(t *testing.T) {
	// if arms must be `&` blocks.
	d := AnalyzeFile("test.pho", []byte(`(if cond foo bar)`))
	if !hasDiag(d, "bad-form-shape") {
		t.Errorf("expected bad-form-shape on bare if arms, got %#v", d)
	}

	// fun args/body must be `'(...)`.
	d = AnalyzeFile("test.pho", []byte(`(fun 'foo (x) (body))`))
	if !hasDiag(d, "bad-form-shape") {
		t.Errorf("expected bad-form-shape on unquoted fun args/body, got %#v", d)
	}

	// for body must be `&block`.
	d = AnalyzeFile("test.pho", []byte(`(for 'x [1 2] body)`))
	if !hasDiag(d, "bad-form-shape") {
		t.Errorf("expected bad-form-shape on bare for body, got %#v", d)
	}

	// var binding name must be quoted.
	d = AnalyzeFile("test.pho", []byte(`(var x 5)`))
	if !hasDiag(d, "bad-form-shape") {
		t.Errorf("expected bad-form-shape on unquoted var name, got %#v", d)
	}

	// Top-level `var` is allowed in .pho scripts (programs); only .phl
	// libraries reject it. Both directions matter — regressions in
	// either would silently change what the LSP shows.
	d = AnalyzeFile("script.pho", []byte(`(var 'x 5)`))
	if hasDiag(d, "no-top-level-var") {
		t.Errorf("did not expect no-top-level-var on .pho top-level var, got %#v", d)
	}
	d = AnalyzeFile("library.phl", []byte(`(var 'x 5)`))
	if !hasDiag(d, "no-top-level-var") {
		t.Errorf("expected no-top-level-var on .phl top-level var, got %#v", d)
	}

	// fun/method bodies accept any quoted form, not just `'(...)`:
	// `(fun '(value) 'value)` is the identity function — perfectly
	// valid and the LSP must not flag it.
	d = AnalyzeFile("test.pho", []byte(`(fun '(value) 'value)`))
	if hasDiag(d, "bad-form-shape") {
		t.Errorf("did not expect bad-form-shape on quoted-leaf fun body, got %#v", d)
	}

	// `=` accepts bare ident, 'ident, and dot — the user's mixed
	// style across the cards/ scripts must keep linting clean.
	d = AnalyzeFile("test.pho", []byte(`(fun 'main '() '(do
  (var 'x 0)
  (= x 5)
  (= 'x 10)
))`))
	if hasDiag(d, "bad-form-shape") {
		t.Errorf("did not expect bad-form-shape on valid = LHS forms, got %#v", d)
	}
}

func TestControlFlowScoping(t *testing.T) {
	// (return) outside a function body — flagged.
	d := AnalyzeFile("test.pho", []byte(`(return)`))
	if !hasDiag(d, "return-outside-function") {
		t.Errorf("expected return-outside-function on top-level (return), got %#v", d)
	}

	// (break) / (continue) outside a loop — flagged. Inside a fun
	// body but not in a for counts as outside.
	d = AnalyzeFile("test.pho", []byte(`(break)`))
	if !hasDiag(d, "break-outside-loop") {
		t.Errorf("expected break-outside-loop on top-level (break), got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(continue)`))
	if !hasDiag(d, "continue-outside-loop") {
		t.Errorf("expected continue-outside-loop on top-level (continue), got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(fun 'f '() '(break))`))
	if !hasDiag(d, "break-outside-loop") {
		t.Errorf("expected break-outside-loop on (break) inside fun outside for, got %#v", d)
	}

	// (return) inside a fun / method body — fine.
	d = AnalyzeFile("test.pho", []byte(`(fun 'f '(x) '(do (return x)))`))
	if hasDiag(d, "return-outside-function") {
		t.Errorf("did not expect return-outside-function inside fun body, got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(struct 'P '(x))
(method P 'M '(self) '(return self.x))`))
	if hasDiag(d, "return-outside-function") {
		t.Errorf("did not expect return-outside-function inside method body, got %#v", d)
	}

	// (break) / (continue) inside a for body — fine, both shapes.
	d = AnalyzeFile("test.pho", []byte(`(fun 'f '() '(for 'i [1 2 3] &(break)))`))
	if hasDiag(d, "break-outside-loop") {
		t.Errorf("did not expect break-outside-loop inside for body, got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(fun 'f '() '(for &True &(continue)))`))
	if hasDiag(d, "continue-outside-loop") {
		t.Errorf("did not expect continue-outside-loop inside for body, got %#v", d)
	}

	// A fun nested inside a for breaks the lexical loop chain —
	// (break) inside the inner fun is still invalid.
	d = AnalyzeFile("test.pho", []byte(`(fun 'outer '() '(for 'i [1 2 3]
    &(do
        (var 'inner (fun '() '(break)))
        (inner)
    )))`))
	if !hasDiag(d, "break-outside-loop") {
		t.Errorf("expected break-outside-loop on (break) inside fun nested in for, got %#v", d)
	}

	// Arity violations.
	d = AnalyzeFile("test.pho", []byte(`(fun 'f '() '(return 1 2))`))
	if !hasDiag(d, "bad-form-arity") {
		t.Errorf("expected bad-form-arity on (return 1 2), got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(fun 'f '() '(for &True &(break x)))`))
	if !hasDiag(d, "bad-form-arity") {
		t.Errorf("expected bad-form-arity on (break x), got %#v", d)
	}
}

// String-interpolation chunks get walked by the lint just like
// normal code: an unknown identifier inside `%name`, `%a.b.c`, or
// `%(call args)` fires unresolved-identifier. Resolved names stay
// clean.
func TestInterpolationReferenceChecks(t *testing.T) {
	d := AnalyzeFile("test.pho", []byte(`(const 'name "ok")
(io.PrintLine "hi %name")`))
	if hasDiag(d, "unresolved-identifier") {
		// `name` is defined, `io` resolves via the import surface only,
		// so io may or may not be defined here — but a real test
		// expects nothing on `name`. Filter on name specifically.
		for _, diag := range d {
			if diag.Code == "unresolved-identifier" && strings.Contains(diag.Message, "'name'") {
				t.Errorf("did not expect 'name' unresolved inside %%name, got %#v", d)
			}
		}
	}

	// Unresolved bare-name interpolation.
	d = AnalyzeFile("test.pho", []byte(`(io.PrintLine "hi %who")`))
	if !hasDiagWithName(d, "unresolved-identifier", "who") {
		t.Errorf("expected unresolved-identifier 'who' inside %%who, got %#v", d)
	}

	// Unresolved name inside %(call ...).
	d = AnalyzeFile("test.pho", []byte(`(io.PrintLine "got %(len missing)")`))
	if !hasDiagWithName(d, "unresolved-identifier", "missing") {
		t.Errorf("expected unresolved-identifier 'missing' inside %%(len missing), got %#v", d)
	}

	// Bad interpolation shape — trailing %, empty %(), %X for X not a
	// valid start — surfaces as bad-interpolation.
	d = AnalyzeFile("test.pho", []byte(`(io.PrintLine "trailing %")`))
	if !hasDiag(d, "bad-interpolation") {
		t.Errorf("expected bad-interpolation on trailing %%, got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(io.PrintLine "empty %()")`))
	if !hasDiag(d, "bad-interpolation") {
		t.Errorf("expected bad-interpolation on empty %%(), got %#v", d)
	}
}

func TestNonStringImportPath(t *testing.T) {
	d := AnalyzeFile("test.pho", []byte(`(import std/io)`))
	if !hasDiag(d, "non-string-import-path") {
		t.Errorf("expected non-string-import-path on bare-ident import, got %#v", d)
	}
}

func TestUnknownExport(t *testing.T) {
	dir, err := os.MkdirTemp("", "pho-lint-imports-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// An imported package with one capitalized fun and one
	// uncapitalized helper.
	pkgDir := filepath.Join(dir, "mathx")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "lib.phl"), []byte(`(fun 'Square '(x) '(* x x))
(fun 'cube '(x) '(* x (* x x)))
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Importer references three names:
	//   mathx.Square     — real export, no diag
	//   mathx.Cube       — typo'd version of the lowercase helper; not exported
	//   mathx.cube       — exists but lowercase, not exported
	target := filepath.Join(dir, "main.pho")
	src := []byte(`(import "` + pkgDir + `")
(mathx.Square 3)
(mathx.Cube 3)
(mathx.cube 3)
`)
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}

	diags := AnalyzeFile(target, src)

	if hasDiagWithName(diags, "unknown-export", "Square") {
		t.Errorf("did not expect unknown-export on real export Square, got %#v", diags)
	}
	if !hasDiagWithName(diags, "unknown-export", "Cube") {
		t.Errorf("expected unknown-export on Cube, got %#v", diags)
	}
	if !hasDiagWithName(diags, "unknown-export", "cube") {
		t.Errorf("expected unknown-export on lowercase 'cube' (not exported), got %#v", diags)
	}
}

// When the import path can't be resolved (LSP running from a
// different cwd, missing package, etc.) the check stays silent —
// flagging every dot access as "package not found" would drown out
// the real signal.
func TestUnknownExportSilentOnUnresolvableImport(t *testing.T) {
	src := []byte(`(import "definitely/does/not/exist")
(exist.Foo)
`)
	diags := AnalyzeFile("test.pho", src)
	if hasDiag(diags, "unknown-export") {
		t.Errorf("expected no unknown-export for unresolvable import, got %#v", diags)
	}
}

// goimport aliases have no Pho-side package to read; the check
// silently skips them.
func TestUnknownExportSkipsGoImport(t *testing.T) {
	src := []byte(`(goimport ["stdDependencies" 'dep])
(dep.AnythingAtAll)
`)
	diags := AnalyzeFile("test.pho", src)
	if hasDiag(diags, "unknown-export") {
		t.Errorf("expected no unknown-export on goimport member, got %#v", diags)
	}
}

func TestSetOnImportAlias(t *testing.T) {
	d := AnalyzeFile("test.pho", []byte(`(import "std/io")
(fun 'main '() '(= 'io 5))
`))
	if !hasDiag(d, "set-on-constant") {
		t.Errorf("expected set-on-constant on import alias, got %#v", d)
	}
}

// A target file declaring a name that a sibling also declares must
// not produce a "shadows X in enclosing scope" diagnostic — that
// cross-file collision is the runtime's concern at load time, not
// the linter's.
func TestPackageScopeDoesNotShadowOnRedeclare(t *testing.T) {
	dir, err := os.MkdirTemp("", "pho-lint-pkg-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	sibling := filepath.Join(dir, "lib.phl")
	if err := os.WriteFile(sibling, []byte(`(const 'shared 1)
`), 0o644); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "main.phl")
	src := []byte(`(const 'shared 2)
`)
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}

	diags := AnalyzeFile(target, src)
	if hasDiag(diags, "redeclaration") {
		t.Errorf("did not expect cross-file redeclaration diag, got %#v", diags)
	}
}

// Empty `()` forms used to crash checkPhlSideEffects with an
// out-of-range index when accessing Children[0]. Regression check —
// the LSP runs lint on every keystroke, so any panic here kills the
// server.
func TestEmptyParenInPhl(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AnalyzeFile panicked on empty (): %v", r)
		}
	}()
	src := []byte("(import \"std/io\")\n()\n")
	diags := AnalyzeFile("test.phl", src)
	// Expect the empty form to be flagged as a side-effect.
	gotEmpty := false
	for _, d := range diags {
		if d.Code == "phl-side-effect" {
			gotEmpty = true
			break
		}
	}
	if !gotEmpty {
		t.Fatalf("expected phl-side-effect diagnostic on empty form, got %#v", diags)
	}
}

// Other malformed top-level inputs should also not panic.
func TestMalformedToplevelDoesNotPanic(t *testing.T) {
	cases := []string{
		"(",                    // unclosed
		")",                    // stray closer
		"(()))",                // imbalanced
		"'",                    // dangling sigil
		"&",                    // dangling sigil
		".",                    // bare dot
		"(. x)",                // unexpected dot
		"(args...)",            // multiple consecutive dots
		"`",                    // stray backtick
		"\"unterminated",       // unterminated string
		"() () ()",             // multiple empties
		"`",                    // single backtick
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on %q: %v", src, r)
				}
			}()
			_ = AnalyzeFile("test.pho", []byte(src))
			_ = AnalyzeFile("test.phl", []byte(src))
		})
	}
}
