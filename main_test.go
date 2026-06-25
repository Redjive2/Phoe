package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIDiagnostics is the end-to-end contract of the diagnostic
// system: exact stderr shape (plain style — NO_COLOR is forced) and
// exit codes. 0 = clean, 1 = runtime errors, 2 = parse errors.
func TestCLIDiagnostics(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pho-test-bin")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	runEnv := func(t *testing.T, fixture string, env ...string) (stdout, stderr string, code int) {
		t.Helper()
		cmd := exec.Command(bin, filepath.Join("testdata", fixture))
		cmd.Env = append(cmd.Environ(), "NO_COLOR=1")
		cmd.Env = append(cmd.Env, env...)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
		err := cmd.Run()
		if err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("running %s: %v", fixture, err)
			}
			code = exitErr.ExitCode()
		}
		return outBuf.String(), errBuf.String(), code
	}
	run := func(t *testing.T, fixture string) (stdout, stderr string, code int) {
		t.Helper()
		return runEnv(t, fixture)
	}

	t.Run("clean script exits 0 with silent stderr", func(t *testing.T) {
		_, stderr, code := run(t, "clean.pho")
		if code != 0 {
			t.Errorf("exit code = %d, want 0; stderr:\n%s", code, stderr)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("optional parameter may be omitted without error", func(t *testing.T) {
		// (optional name) makes a trailing arg omittable: the call with it
		// absent binds Nil and runs, so the script is clean end to end
		// (lint-on-run + runtime) — exit 0, silent stderr.
		_, stderr, code := run(t, "optional.pho")
		if code != 0 {
			t.Errorf("exit code = %d, want 0; stderr:\n%s", code, stderr)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("runtime error renders block and exits 1", func(t *testing.T) {
		stdout, stderr, code := run(t, "runtime_err.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		for _, want := range []string{
			"error[type-mismatch]: '-' expected a 'num' argument, got 'str'",
			"--> runtime_err.pho:4:1",
			"4 | (- 'a' 1)",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q; got:\n%s", want, stderr)
			}
		}
		if strings.Contains(stdout, "ERR") {
			t.Errorf("error text leaked to stdout: %q", stdout)
		}
	})

	t.Run("surplus builtin arguments are an arity error", func(t *testing.T) {
		// The linter intercepts the surplus before the program runs
		// (lint-on-run), so this surfaces as the static bad-form-arity
		// rather than the runtime arity check — but it's still flagged,
		// never silently ignored, which is what this pins.
		_, stderr, code := run(t, "surplus_arity.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr, "error[bad-form-arity]: '=' expects 2 argument(s); got 3") {
			t.Errorf("missing arity diagnostic; stderr:\n%s", stderr)
		}
	})

	t.Run("bad spread aborts the call", func(t *testing.T) {
		_, stderr, code := run(t, "bad_spread.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr, "error[bad-spread]: ") {
			t.Errorf("missing bad-spread diagnostic; stderr:\n%s", stderr)
		}
		// The body is a type error that would render if it ran; its
		// absence proves the bad spread aborted the call.
		if strings.Contains(stderr, "type-mismatch") {
			t.Errorf("callee body ran despite bad spread; stderr:\n%s", stderr)
		}
	})

	t.Run("broken import emits parse diagnostics exactly once", func(t *testing.T) {
		_, stderr, code := run(t, "import_twice.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if n := strings.Count(stderr, "error[parse-error]: "); n != 1 {
			t.Errorf("parse diagnostic rendered %d times, want exactly 1; stderr:\n%s", n, stderr)
		}
	})

	t.Run("error inside a fun body carets the body form", func(t *testing.T) {
		_, stderr, code := run(t, "body_span.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		for _, want := range []string{
			"--> body_span.pho:3:15",
			"3 | (fun half (n) (/ n 'x'))",
			"  |               ^^^^^^^^^",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q; got:\n%s", want, stderr)
			}
		}
	})

	t.Run("macro error shows the call site and the generated code", func(t *testing.T) {
		_, stderr, code := run(t, "macro_err.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		for _, want := range []string{
			"error[unresolved]: operation 'fakeFunctionName' is not defined",
			"4 | (~evil_macro)", // the call site, as normal
			"= expanded from macro 'evil_macro':",
			"1 | (fakeFunctionName fakeArgumentName)", // the generated code
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q; got:\n%s", want, stderr)
			}
		}
	})

	t.Run("macro error narrows the caret to the offending generated form", func(t *testing.T) {
		_, stderr, code := run(t, "macro_nested_err.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		// The generated code is (identity (undefinedThing)); the caret must
		// underline the inner (undefinedThing), not the whole generated form.
		for _, want := range []string{
			"= expanded from macro 'wrap':",
			"1 | (identity (undefinedThing))",
			"  |           ^^^^^^^^^^^^^^^^",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q; got:\n%s", want, stderr)
			}
		}
	})

	t.Run("nested call error carries a named stack trace", func(t *testing.T) {
		_, stderr, code := run(t, "trace.pho")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		for _, want := range []string{
			"trace (most recent call first):",
			"0: double       trace.pho:4:17",
			"1: tally        trace.pho:5:35",
			"2: <top level>  trace.pho:6:1",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q; got:\n%s", want, stderr)
			}
		}
	})

	t.Run("PHO_NO_SPANS does not change evaluation results", func(t *testing.T) {
		// The span wrappers must be semantically transparent: program
		// stdout and exit codes are identical with and without them.
		// (stderr legitimately differs — that's the point of the spans.)
		for _, fixture := range []string{
			"clean.pho", "runtime_err.pho", "bad_spread.pho",
			"surplus_arity.pho", "body_span.pho", "trace.pho",
		} {
			withSpans := exec.Command(bin, filepath.Join("testdata", fixture))
			withSpans.Env = append(withSpans.Environ(), "NO_COLOR=1")
			noSpans := exec.Command(bin, filepath.Join("testdata", fixture))
			noSpans.Env = append(noSpans.Environ(), "NO_COLOR=1", "PHO_NO_SPANS=1")

			var outA, outB bytes.Buffer
			withSpans.Stdout = &outA
			noSpans.Stdout = &outB
			errA := withSpans.Run()
			errB := noSpans.Run()

			if outA.String() != outB.String() {
				t.Errorf("%s: stdout differs with PHO_NO_SPANS=1:\n--- spans ---\n%s\n--- no spans ---\n%s",
					fixture, outA.String(), outB.String())
			}
			codeA, codeB := 0, 0
			if e, ok := errA.(*exec.ExitError); ok {
				codeA = e.ExitCode()
			}
			if e, ok := errB.(*exec.ExitError); ok {
				codeB = e.ExitCode()
			}
			if codeA != codeB {
				t.Errorf("%s: exit code differs with PHO_NO_SPANS=1: %d vs %d", fixture, codeA, codeB)
			}
		}
	})

	t.Run("infinite recursion becomes a recursion-limit diagnostic", func(t *testing.T) {
		// A small depth keeps the test fast; the guard must fire instead
		// of crashing with a fatal Go stack overflow.
		_, stderr, code := runEnv(t, "recursion.pho", "PHO_MAX_DEPTH=50")
		if code != 1 {
			t.Errorf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "error[recursion-limit]: recursion limit exceeded (50 calls)") {
			t.Errorf("missing recursion-limit diagnostic; stderr:\n%s", stderr)
		}
		// The deep identical trace must collapse, not dump 50 frames.
		if !strings.Contains(stderr, "repeated") {
			t.Errorf("recursion trace not collapsed; stderr:\n%s", stderr)
		}
	})

	t.Run("error cap renders compactly past the limit; summary counts all", func(t *testing.T) {
		_, stderr, code := runEnv(t, "multi_err.pho", "PHO_MAX_ERRORS=1")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr, "further errors shown compactly") {
			t.Errorf("missing cap transition line; stderr:\n%s", stderr)
		}
		// Compact form carries the location inline.
		if !strings.Contains(stderr, "--> multi_err.pho:4:1") {
			t.Errorf("missing compact-form location; stderr:\n%s", stderr)
		}
		if !strings.Contains(stderr, "error: 3 errors") {
			t.Errorf("summary should count all 3 errors; stderr:\n%s", stderr)
		}
	})

	t.Run("PHO_STRICT stops after the first erroring form", func(t *testing.T) {
		_, lenient, _ := run(t, "multi_err.pho")
		if n := strings.Count(lenient, "type-mismatch"); n != 3 {
			t.Errorf("lenient run = %d errors, want 3", n)
		}
		_, strict, code := runEnv(t, "multi_err.pho", "PHO_STRICT=1")
		if code != 1 {
			t.Errorf("strict exit code = %d, want 1", code)
		}
		if n := strings.Count(strict, "type-mismatch"); n != 1 {
			t.Errorf("strict run = %d errors, want 1 (stop after first)", n)
		}
	})

	t.Run("parse error renders excerpt and exits 2", func(t *testing.T) {
		_, stderr, code := run(t, "parse_err.pho")
		if code != 2 {
			t.Errorf("exit code = %d, want 2; stderr:\n%s", code, stderr)
		}
		for _, want := range []string{
			"error[parse-error]: ",
			"--> parse_err.pho:2:15",
			"2 | (const best 3))",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q; got:\n%s", want, stderr)
			}
		}
	})
}
