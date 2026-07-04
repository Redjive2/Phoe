package builtins

import (
	"testing"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// patCtx builds an eval context like evalProgram's, optionally running setup
// forms (struct declarations, lets) in it first.
func patCtx(t *testing.T, setup string) core.Context {
	t.Helper()
	env := NewEnv()
	file := &core.File{Mode: core.ModeProgram, Imports: map[string]core.Value{}}
	ctx := core.Context{Env: &env, File: file}
	ctx.PushFrame()
	if setup != "" {
		for _, form := range lowerForms(t, setup) {
			form.Evaluate(ctx)
		}
	}
	return ctx
}

func lowerForms(t *testing.T, src string) core.Branch {
	t.Helper()
	tokens, _ := syntax.LexPos(src)
	tree, _ := syntax.ParsePos(tokens)
	lowered, ok := syntax.Lower(tree).(core.Branch)
	if !ok {
		t.Fatalf("Lower did not return a Branch for %q", src)
	}
	return lowered
}

// mustPattern parses a single pattern expression in ctx.
func mustPattern(t *testing.T, ctx core.Context, src string) *pattern {
	t.Helper()
	forms := lowerForms(t, src)
	p, ok := parsePattern(ctx, forms[0], newBinderSet())
	if !ok {
		t.Fatalf("parsePattern(%q) failed", src)
	}
	return p
}

// evalIn evaluates forms in ctx, returning the last value.
func evalIn(t *testing.T, ctx core.Context, src string) core.Value {
	t.Helper()
	var last core.Value
	for _, form := range lowerForms(t, src) {
		last = form.Evaluate(ctx)
	}
	return last
}

func TestPatternMatching(t *testing.T) {
	cases := []struct {
		name    string
		setup   string // forms evaluated before parsing the pattern
		pattern string
		value   string // expression evaluated in the same ctx
		match   bool
		binds   map[string]string // binder -> expression producing expected value
	}{
		{"bind anything", "", "x", "5", true, map[string]string{"x": "5"}},
		{"literal number match", "", "0", "0", true, nil},
		{"literal number reject", "", "0", "1", false, nil},
		{"literal string", "", "'hi'", "'hi'", true, nil},
		{"literal string reject", "", "'hi'", "'ho'", false, nil},
		{"literal atom", "", ":fast", ":fast", true, nil},
		{"literal atom reject", "", ":fast", ":slow", false, nil},
		{"literal true", "", "true", "true", true, nil},
		{"literal none", "", "none", "none", true, nil},
		{"literal none rejects 0", "", "none", "0", false, nil},
		{"type value disc-style", "", "Number", "Number", true, nil},
		{"type value rejects instance", "", "Number", "5", false, nil},
		{"type test binds", "", "(Number n)", "42", true, map[string]string{"n": "42"}},
		{"type test rejects", "", "(Number n)", "'str'", false, nil},
		{"list exact", "", "[a 0]", "[7 0]", true, map[string]string{"a": "7"}},
		{"list literal mismatch", "", "[a 0]", "[7 1]", false, nil},
		{"list length mismatch", "", "[a 0]", "[7 0 9]", false, nil},
		{"nested list", "", "[[a b] c]", "[[1 2] 3]", true, map[string]string{"a": "1", "b": "2", "c": "3"}},
		{
			"struct destructure",
			"(struct Point x y)",
			"Point.{ x = 0 y = b }",
			"Point.{ x = 0 y = 2 }",
			true, map[string]string{"b": "2"},
		},
		{
			"struct field literal reject",
			"(struct Point x y)",
			"Point.{ x = 0 y = b }",
			"Point.{ x = 1 y = 2 }",
			false, nil,
		},
		{
			"struct wrong type rejects",
			"(struct Point x y)\n(struct Spot x y)",
			"Point.{ x = a y = b }",
			"Spot.{ x = 1 y = 2 }",
			false, nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := patCtx(t, tc.setup)
			pat := mustPattern(t, ctx, tc.pattern)
			val := evalIn(t, ctx, tc.value)

			out := map[string]core.Value{}
			got := matchPattern(ctx, pat, val, out, false)
			if got != tc.match {
				t.Fatalf("match(%s, %s) = %v, want %v", tc.pattern, tc.value, got, tc.match)
			}
			for name, expr := range tc.binds {
				want := evalIn(t, ctx, expr)
				bound, ok := out[name]
				if !ok {
					t.Fatalf("binder %q not bound; got %v", name, out)
				}
				if !tvalEqual(bound, want) {
					t.Errorf("binder %q = %v, want %v", name, bound.Val, want.Val)
				}
			}
		})
	}
}

// A pattern binding the same name twice is a definition-time error.
func TestPatternDuplicateBinder(t *testing.T) {
	ctx := patCtx(t, "")
	forms := lowerForms(t, "[a a]")
	if _, ok := parsePattern(ctx, forms[0], newBinderSet()); ok {
		t.Fatal("duplicate binder should fail to parse")
	}
}

// An unknown Capitalized name in a pattern fails at definition time.
func TestPatternUnknownTypeName(t *testing.T) {
	ctx := patCtx(t, "")
	forms := lowerForms(t, "Nope-Type")
	if _, ok := parsePattern(ctx, forms[0], newBinderSet()); ok {
		t.Fatal("unknown Capitalized name should fail to parse")
	}
}

// Private fields destructure only under privilege.
func TestPatternPrivateFieldPrivilege(t *testing.T) {
	ctx := patCtx(t, "(struct Sec #k)\n(struct Sec2 #k)")
	pat := mustPattern(t, ctx, "Sec.{ #k = v }")
	val := evalIn(t, ctx, "Sec.{ #k = 9 }")

	out := map[string]core.Value{}
	if matchPattern(ctx, pat, val, out, false) {
		t.Error("private-field pattern without privilege must not match")
	}
	out = map[string]core.Value{}
	if !matchPattern(ctx, pat, val, out, true) {
		t.Error("private-field pattern with privilege should match")
	}
	if v, ok := out["v"]; !ok || v.Val.(float64) != 9 {
		t.Errorf("v = %v, want 9", out["v"])
	}
}
