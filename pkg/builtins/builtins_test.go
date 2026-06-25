package builtins

import (
	"testing"

	"pho/pkg/core"
	"pho/pkg/diag"
	"pho/pkg/syntax"
)

// evalProgram runs Pho source through the real pipeline — lex, parse,
// lower (which desugars `"%x"` interpolation into Strinterp/Strcoerce),
// and evaluate against a full builtin env — returning the value of the
// LAST top-level form. This is the end-to-end proof that interpolation
// produces a concatenated string at runtime, not merely that it lowers
// to the right tree shape (which pkg/syntax already covers).
func evalProgram(t *testing.T, src string) core.Value {
	t.Helper()
	env := NewEnv()
	// A program-mode File so top-level `var` is permitted (the var
	// builtin allows it only inside a function or a .pho program), and
	// Resolve has a non-nil Imports map to consult — mirroring how
	// modload runs a .pho script.
	file := &core.File{Mode: core.ModeProgram, Imports: map[string]core.Value{}}
	ctx := core.Context{Env: &env, File: file}
	ctx.PushFrame() // top-level frame for const/var, mirroring modload

	tokens, _ := syntax.LexPos(src)
	tree, _ := syntax.ParsePos(tokens)
	lowered, ok := syntax.Lower(tree).(core.Branch)
	if !ok {
		t.Fatalf("Lower did not return a Branch for %q", src)
	}

	var last core.Value
	for _, form := range lowered {
		last = form.Evaluate(ctx)
	}
	return last
}

// TestInterpolationEndToEnd proves the `"%name"` / `"%a.b.c"` /
// `"%(expr)"` surface actually interpolates at runtime, across the
// three forms plus non-string coercion and `\%` escaping.
func TestInterpolationEndToEnd(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"bare name",
			"(let who = 'World')\n'hi %who'",
			"hi World",
		},
		{
			"number coercion",
			"(let n = 42)\n'n=%n'",
			"n=42",
		},
		{
			"paren expression",
			"(let xs = [1 2 3])\n'len=%(+ 0 xs.Size)'",
			"len=3",
		},
		{
			"two interpolations and a literal tail",
			"(let a = 1 b = 2)\n'%a+%b='",
			"1+2=",
		},
		{
			"escaped percent stays literal",
			"'100\\% sure'",
			"100% sure",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalProgram(t, tc.src)
			if v.Kind != core.KindStr {
				t.Fatalf("eval(%q): expected str, got kind %q (%v)", tc.src, v.Kind, v.Val)
			}
			if got := v.Val.(string); got != tc.want {
				t.Errorf("eval(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestBindMethodResetsPrivilegedOnReturn pins down the defer ordering
// inside BindMethod: the Privileged-reset defer must run BEFORE the
// outer recover that catches ReturnSignal, so a method that uses
// (return) leaves its receiver in a clean state. If the recover were
// registered after the Privileged-reset defer (or if recover() and
// re-panic semantics were misunderstood), this test would catch it.
func TestBindMethodResetsPrivilegedOnReturn(t *testing.T) {
	env := NewEnv()
	ctx := core.Context{Env: &env}

	// Minimal struct value — no fields or methods, just enough to
	// hang a Privileged flag off.
	structPtr := &core.Struct{
		Fields:  []string{},
		Methods: map[string]core.Fun{},
		Origin:  &env,
	}
	instance := core.TvInstance(structPtr, map[string]core.Value{}, false)

	// BindMethod reads InstStack[0] as the receiver; populate it.
	env.InstStack = append([]core.Value{instance}, env.InstStack...)

	// Body: (return 42) — the `return` builtin in NewEnv panics
	// ReturnSignal{Value: TvNum(42)}, BindMethod catches it, returns 42.
	body := core.Branch{core.Leaf("return"), core.Leaf("42")}

	fn := core.BindMethod("T.m", body, []string{"self"}, ctx)
	result := fn(ctx, nil)

	if result.Kind != core.KindNum {
		t.Fatalf("expected numeric result kind, got %q", result.Kind)
	}
	if got := result.Val.(float64); got != 42 {
		t.Errorf("expected 42 from (return 42), got %v", got)
	}

	instData := instance.Val.(*core.Instance)
	if instData.Privileged {
		t.Errorf("Privileged must be false after method exit (even via panic), still true")
	}
}

// TestBindFunRecoversReturn covers the BindFun side of the same
// contract — (return v) inside a regular function body surfaces as
// the function's return value.
func TestBindFunRecoversReturn(t *testing.T) {
	env := NewEnv()
	ctx := core.Context{Env: &env}

	// (return "hi") with one arg.
	body := core.Branch{core.Leaf("return"), core.Leaf(`'hi'`)}

	fn := core.BindFun("f", body, []string{}, ctx)
	result := fn(ctx, nil)

	if result.Kind != core.KindStr {
		t.Fatalf("expected str result kind, got %q", result.Kind)
	}
	if got := result.Val.(string); got != "hi" {
		t.Errorf("expected \"hi\" from (return \"hi\"), got %q", got)
	}
}

// TestStringifyValue pins down strcoerce's per-kind formatting. The
// rules are arbitrary in places (e.g. arrays as `[a b c]`, methods
// as `<method>`) but stable: callers building `"%v"`-style output
// should get predictable strings.
func TestStringifyValue(t *testing.T) {
	cases := []struct {
		name string
		in   core.Value
		want string
	}{
		{"str passthrough", core.TvStr("hi"), "hi"},
		{"int", core.TvNum(42), "42"},
		{"float", core.TvNum(3.14), "3.14"},
		{"true", core.TvBool(true), "True"},
		{"false", core.TvBool(false), "False"},
		{"nil", core.TvNil, "Nil"},
		{"chr", core.TvChr('z'), "z"},
		{"array", core.TvSlice([]core.Value{core.TvNum(1), core.TvNum(2), core.TvStr("x")}), "[1 2 x]"},
		{"nested array", core.TvSlice([]core.Value{core.TvSlice([]core.Value{core.TvNum(1)})}), "[[1]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := core.Stringify(tc.in)
			if got != tc.want {
				t.Errorf("Stringify(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBindFunBareReturn — (return) with no args yields Nil.
func TestBindFunBareReturn(t *testing.T) {
	env := NewEnv()
	ctx := core.Context{Env: &env}

	body := core.Branch{core.Leaf("return")}

	fn := core.BindFun("f", body, []string{}, ctx)
	result := fn(ctx, nil)

	if result.Kind != core.KindNil {
		t.Errorf("expected nil kind from bare (return), got %q", result.Kind)
	}
}

// TestInterpolationInFunBody is the regression for the reported bug:
// fun/method bodies are QUOTED, so a `"%x"` literal inside one used to
// round-trip through the quote system as raw text and never interpolate
// (top-level interpolation worked, but the in-body case — i.e. nearly
// all real interpolation — silently rendered the literal `%x`). These
// call functions/methods whose bodies interpolate and check the result.
func TestInterpolationInFunBody(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"quoted-string fun body",
			"(fun greet (who) 'hi %who')\n(greet 'World')",
			"hi World",
		},
		{
			"interp inside (do ...) body",
			"(fun tag (n) (identity do (let var s = 'n=%n') s))\n(tag 7)",
			"n=7",
		},
		{
			"paren expr in fun body",
			"(fun count (xs) 'len=%(+ 0 xs.Size)')\n(count [1 2 3 4])",
			"len=4",
		},
		// NOTE: method-body interpolation uses the exact same quoted-body
		// path as fun bodies (listifyP), so the cases above cover the
		// desugaring. Method *dispatch* needs the full modload wiring
		// that this minimal env can't replicate, so the method case is
		// verified against the real interpreter instead (see the demo run
		// in the change description), not here.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalProgram(t, tc.src)
			if v.Kind != core.KindStr {
				t.Fatalf("eval(%q): expected str, got kind %q (%v)", tc.src, v.Kind, v.Val)
			}
			if got := v.Val.(string); got != tc.want {
				t.Errorf("eval(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// evalProgramDiag runs src like evalProgram but attaches a diagnostic
// session so a test can assert which runtime errors fired. Returns the
// value of the last form plus every diagnostic code emitted.
func evalProgramDiag(t *testing.T, src string) (core.Value, []string) {
	t.Helper()
	env := NewEnv()
	s := diag.NewSession()
	var codes []string
	s.Report = func(e diag.RuntimeError) { codes = append(codes, e.Code) }
	file := &core.File{Mode: core.ModeProgram, Imports: map[string]core.Value{}}
	ctx := core.Context{Env: &env, File: file, Diag: s}
	ctx.PushFrame()

	tokens, _ := syntax.LexPos(src)
	tree, _ := syntax.ParsePos(tokens)
	lowered, _ := syntax.Lower(tree).(core.Branch)
	var last core.Value
	for _, form := range lowered {
		last = form.Evaluate(ctx)
	}
	return last, codes
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// An omitted (optional name) parameter binds to Nil; a supplied one
// binds the argument. The body `'b` is the identity of the optional
// parameter, so the returned value is exactly what got bound.
func TestOptionalParamBinding(t *testing.T) {
	if v := evalProgram(t, "(fun f (a (optional b)) b)\n(f 1)"); v.Kind != core.KindNil {
		t.Errorf("omitted optional: got kind %q (%v), want Nil", v.Kind, v.Val)
	}
	if v := evalProgram(t, "(fun f (a (optional b)) b)\n(f 1 2)"); v.Kind != core.KindNum || v.Val.(float64) != 2 {
		t.Errorf("supplied optional: got %v, want 2", v.Val)
	}
}

// Optional and spread compose: the rest-arg collects only what's left
// after every named parameter (required + optional) is filled.
func TestOptionalWithSpread(t *testing.T) {
	// Body returns the rest array.
	restLen := func(src string) int {
		t.Helper()
		v := evalProgram(t, src)
		if v.Kind != core.KindArray {
			t.Fatalf("expected array rest, got kind %q (%v)", v.Kind, v.Val)
		}
		return len(*v.Val.(*[]core.Value))
	}
	prog := func(call string) string {
		return "(fun g (a (optional b) (spread r)) r)\n" + call
	}
	if n := restLen(prog("(g 1)")); n != 0 {
		t.Errorf("(g 1): rest len = %d, want 0", n)
	}
	if n := restLen(prog("(g 1 2)")); n != 0 {
		t.Errorf("(g 1 2): rest len = %d, want 0 (b consumes the 2nd arg)", n)
	}
	if n := restLen(prog("(g 1 2 3 4)")); n != 2 {
		t.Errorf("(g 1 2 3 4): rest len = %d, want 2", n)
	}
}

// A required parameter omitted still errors, even when later params are
// optional — the minimum-arity check counts only required params.
func TestOptionalRequiredStillEnforced(t *testing.T) {
	_, codes := evalProgramDiag(t, "(fun need (a (optional b)) a)\n(need)")
	if !hasCode(codes, core.ErrArity) {
		t.Errorf("omitting a required arg must raise ErrArity, got %v", codes)
	}
}

// A required parameter after an optional one is a malformed declaration,
// rejected by parseArgList when the `fun` form is evaluated.
func TestOptionalOrderingRejected(t *testing.T) {
	_, codes := evalProgramDiag(t, "(fun bad ((optional a) b) a)")
	if !hasCode(codes, core.ErrBadForm) {
		t.Errorf("required-after-optional must raise ErrBadForm, got %v", codes)
	}
}
