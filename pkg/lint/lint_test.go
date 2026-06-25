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
	if err := os.WriteFile(sibling, []byte(`(import 'std/io')
(let helper = 42)
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Target file: refers to `helper` (visible) and to `io` (must NOT
	// be visible — sibling's import is file-scoped).
	target := filepath.Join(dir, "main.phl")
	src := []byte(`(let doubled = (+ helper helper))
(let mystery = io)
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
	src := []byte(`(import 'std/io')
(io.print_line 'hi')
`)
	used := AnalyzeFile("test.pho", src)
	if hasDiag(used, "unused-import") {
		t.Errorf("did not expect unused-import on used import, got %#v", used)
	}

	src = []byte(`(import 'std/io')
(fun main () (identity do))
`)
	unused := AnalyzeFile("test.pho", src)
	if !hasDiag(unused, "unused-import") {
		t.Errorf("expected unused-import on unreferenced alias, got %#v", unused)
	}
}

func TestInvalidSelfUsage(t *testing.T) {
	// `self` outside any method body is flagged.
	src := []byte(`(io.print_line self)
`)
	diags := AnalyzeFile("test.pho", src)
	if !hasDiag(diags, "invalid-self-usage") {
		t.Errorf("expected invalid-self-usage at top level, got %#v", diags)
	}

	// `self` inside a method body is not flagged.
	src = []byte(`(struct T #x)
(method T.#foo (self) (identity do (io.print_line self.#x)))
`)
	diags = AnalyzeFile("test.pho", src)
	if hasDiag(diags, "invalid-self-usage") {
		t.Errorf("did not expect invalid-self-usage inside method body, got %#v", diags)
	}

	// `self` inside a fun nested in a method is allowed (closure
	// captures the receiver).
	src = []byte(`(struct T #x)
(method T.#foo (self) (identity do
  (fun inner () (io.print_line self.#x))
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
		{"fun-too-few", `(fun foo)`},
		{"fun-too-many", `(fun foo (x) (body) extra)`},
		{"struct-too-few", `(struct)`},
		{"= without value", `(= x)`},
		{"do-empty", `(do)`},
		{"var-odd-args", `(let var a = 1 b)`},
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
	// Post-cutover the declaration/control forms are BARE (no '/& sigils);
	// `if` uses the then/elif/else keyword markers, with bare arms.
	d := AnalyzeFile("test.pho", []byte("(let cond = true)\n(let foo = 1)\n(let bar = 2)\n(if cond then foo else bar)"))
	if hasDiag(d, "bad-form-shape") {
		t.Errorf("keyword-form if should be valid, got %#v", d)
	}

	// Post-cutover the declaration forms take BARE names and arg-lists. A
	// leftover `'` quote is no longer recognized at all — it's a parse-level
	// "unrecognized character" (covered by the lexer tests), not a lint
	// diagnostic — so here the linter only needs to confirm the bare forms
	// stay clean.
	d = AnalyzeFile("test.pho", []byte(`(let var x = 5)`))
	if hasDiag(d, "bad-form-shape") {
		t.Errorf("bare var name should be clean, got %#v", d)
	}

	// Top-level `var` is allowed in both .pho scripts and .phl libraries
	// (a library var is read-only module state). Neither flags it — the
	// dedicated ban was removed, and var is a libraryForms declaration so
	// it isn't a side effect either.
	d = AnalyzeFile("script.pho", []byte(`(let var x = 5)`))
	if hasDiag(d, "no-top-level-var") || hasDiag(d, "phl-side-effect") {
		t.Errorf("did not expect a top-level-var diagnostic on .pho, got %#v", d)
	}
	d = AnalyzeFile("library.phl", []byte(`(let var x = 5)`))
	if hasDiag(d, "no-top-level-var") || hasDiag(d, "phl-side-effect") {
		t.Errorf("top-level var is now allowed in .phl libraries, got %#v", d)
	}

	// A top-level macro declaration is a declaration, not a side effect —
	// it belongs in a .phl library just like fun/struct (libraryForms).
	d = AnalyzeFile("library.phl", []byte(`(macro twice! (e) (+ e e))`))
	if hasDiag(d, "phl-side-effect") {
		t.Errorf("top-level macro should be allowed in .phl libraries, got %#v", d)
	}

	// A fun with a bare param-list and a bare body — the identity function
	// `(fun (value) value)` — is well-formed and the LSP must not flag it.
	d = AnalyzeFile("test.pho", []byte(`(fun (value) value)`))
	if hasDiag(d, "bad-form-shape") {
		t.Errorf("did not expect bad-form-shape on bare-leaf fun body, got %#v", d)
	}

	// `=` accepts a bare ident or a dot target — the user's mixed
	// style across the cards/ scripts must keep linting clean.
	d = AnalyzeFile("test.pho", []byte(`(fun main () (identity do
  (let var x = 0)
  (= x 5)
  (= x 10)
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
	d = AnalyzeFile("test.pho", []byte(`(fun f () (break))`))
	if !hasDiag(d, "break-outside-loop") {
		t.Errorf("expected break-outside-loop on (break) inside fun outside for, got %#v", d)
	}

	// (return) inside a fun / method body — fine.
	d = AnalyzeFile("test.pho", []byte(`(fun f (x) (identity do (return x)))`))
	if hasDiag(d, "return-outside-function") {
		t.Errorf("did not expect return-outside-function inside fun body, got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(struct P #x)
(method P.m (self) (return self.#x))`))
	if hasDiag(d, "return-outside-function") {
		t.Errorf("did not expect return-outside-function inside method body, got %#v", d)
	}

	// (break) / (continue) inside a for body — fine, both shapes.
	d = AnalyzeFile("test.pho", []byte(`(fun f () (foreach i in [1 2 3] (break)))`))
	if hasDiag(d, "break-outside-loop") {
		t.Errorf("did not expect break-outside-loop inside for body, got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(fun f () (foreach true in (continue)))`))
	if hasDiag(d, "continue-outside-loop") {
		t.Errorf("did not expect continue-outside-loop inside for body, got %#v", d)
	}

	// A fun nested inside a for breaks the lexical loop chain —
	// (break) inside the inner fun is still invalid.
	d = AnalyzeFile("test.pho", []byte(`(fun outer () (foreach i in [1 2 3]
    (identity do
        (let var inner = (fun () (break)))
        (inner)
    )))`))
	if !hasDiag(d, "break-outside-loop") {
		t.Errorf("expected break-outside-loop on (break) inside fun nested in for, got %#v", d)
	}

	// Arity violations.
	d = AnalyzeFile("test.pho", []byte(`(fun f () (return 1 2))`))
	if !hasDiag(d, "bad-form-arity") {
		t.Errorf("expected bad-form-arity on (return 1 2), got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(fun f () (foreach true in (break x)))`))
	if !hasDiag(d, "bad-form-arity") {
		t.Errorf("expected bad-form-arity on (break x), got %#v", d)
	}
}

// String-interpolation chunks get walked by the lint just like
// normal code: an unknown identifier inside `%name`, `%a.b.c`, or
// `%(call args)` fires unresolved-identifier. Resolved names stay
// clean.
func TestInterpolationReferenceChecks(t *testing.T) {
	d := AnalyzeFile("test.pho", []byte(`(let name = 'ok')
(io.print_line 'hi %name')`))
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
	d = AnalyzeFile("test.pho", []byte(`(io.print_line 'hi %who')`))
	if !hasDiagWithName(d, "unresolved-identifier", "who") {
		t.Errorf("expected unresolved-identifier 'who' inside %%who, got %#v", d)
	}

	// Unresolved name inside %(call ...).
	d = AnalyzeFile("test.pho", []byte(`(io.print_line 'got %(len missing)')`))
	if !hasDiagWithName(d, "unresolved-identifier", "missing") {
		t.Errorf("expected unresolved-identifier 'missing' inside %%(len missing), got %#v", d)
	}

	// Bad interpolation shape — trailing %, empty %(), %X for X not a
	// valid start — surfaces as bad-interpolation.
	d = AnalyzeFile("test.pho", []byte(`(io.print_line 'trailing %')`))
	if !hasDiag(d, "bad-interpolation") {
		t.Errorf("expected bad-interpolation on trailing %%, got %#v", d)
	}
	d = AnalyzeFile("test.pho", []byte(`(io.print_line 'empty %()')`))
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

	// An imported package with one public fun and one '#'-private helper.
	pkgDir := filepath.Join(dir, "mathx")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "lib.phl"), []byte(`(fun square (x) (* x x))
(fun #cube (x) (* x (* x x)))
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Importer references two names:
	//   mathx.square     — public export, no diag
	//   mathx.#cube      — '#'-private helper, not exported
	target := filepath.Join(dir, "main.pho")
	src := []byte(`(import '` + pkgDir + `')
(mathx.square 3)
(mathx.#cube 3)
`)
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}

	diags := AnalyzeFile(target, src)

	if hasDiagWithName(diags, "unknown-export", "square") {
		t.Errorf("did not expect unknown-export on public export square, got %#v", diags)
	}
	if !hasDiagWithName(diags, "unknown-export", "#cube") {
		t.Errorf("expected unknown-export on '#'-private '#cube' (not exported), got %#v", diags)
	}
}

// When the import path can't be resolved (LSP running from a
// different cwd, missing package, etc.) the check stays silent —
// flagging every dot access as "package not found" would drown out
// the real signal.
func TestUnknownExportSilentOnUnresolvableImport(t *testing.T) {
	src := []byte(`(import 'definitely/does/not/exist')
(exist.foo)
`)
	diags := AnalyzeFile("test.pho", src)
	if hasDiag(diags, "unknown-export") {
		t.Errorf("expected no unknown-export for unresolvable import, got %#v", diags)
	}
}

// goimport aliases have no Pho-side package to read; the check
// silently skips them.
func TestUnknownExportSkipsGoImport(t *testing.T) {
	src := []byte(`(goimport ('stdDependencies' dep))
(dep.AnythingAtAll)
`)
	diags := AnalyzeFile("test.pho", src)
	if hasDiag(diags, "unknown-export") {
		t.Errorf("expected no unknown-export on goimport member, got %#v", diags)
	}
}

func TestSetOnImportAlias(t *testing.T) {
	d := AnalyzeFile("test.pho", []byte(`(import 'std/io')
(fun main () (= io 5))
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
	if err := os.WriteFile(sibling, []byte(`(let shared = 1)
`), 0o644); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "main.phl")
	src := []byte(`(let shared = 2)
`)
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}

	diags := AnalyzeFile(target, src)
	if hasDiag(diags, "redeclaration") {
		t.Errorf("did not expect cross-file redeclaration diag, got %#v", diags)
	}
}

// A method shares no namespace with top-level bindings: the runtime
// stores it on the owner struct (builtins.method) and never Declares
// it. So (method Process 'Stdout ...) must not be reported as shadowing
// (fun 'Stdout ...) or a 'Stdout method on a different receiver. Only a
// second 'Stdout on Process itself is a genuine redeclaration.
func TestMethodDoesNotShadowFunOrOtherReceiver(t *testing.T) {
	// method vs fun of the same name — no redeclaration.
	d := AnalyzeFile("test.pho", []byte(`(struct Process #pid)
(fun stdout () (none))
(method Process.stdout (self) (self.#pid))
`))
	if hasDiag(d, "redeclaration") {
		t.Errorf("method must not shadow a fun of the same name, got %#v", d)
	}

	// two methods named Stdout on DIFFERENT receivers — no redeclaration.
	d = AnalyzeFile("test.pho", []byte(`(struct Process #pid)
(struct File #fd)
(method Process.stdout (self) (self.#pid))
(method File.stdout (self) (self.#fd))
`))
	if hasDiag(d, "redeclaration") {
		t.Errorf("methods named the same on different receivers must not collide, got %#v", d)
	}

	// the SAME method on the SAME receiver twice — still a redeclaration.
	d = AnalyzeFile("test.pho", []byte(`(struct Process #pid)
(method Process.stdout (self) (self.#pid))
(method Process.stdout (self) (self.#pid))
`))
	if !hasDiag(d, "redeclaration") {
		t.Errorf("a method redefined on the same receiver should be flagged, got %#v", d)
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
	src := []byte("(import 'std/io')\n()\n")
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
		"(",              // unclosed
		")",              // stray closer
		"(()))",          // imbalanced
		"'",              // dangling sigil
		"&",              // dangling sigil
		".",              // bare dot
		"(. x)",          // unexpected dot
		"(args...)",      // multiple consecutive dots
		"`",              // stray backtick
		"\"unterminated", // unterminated string
		"() () ()",       // multiple empties
		"`",              // single backtick
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
