package builtins

import (
	"testing"

	"pho/pkg/core"
)

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
	instance := core.TvInstance(structPtr, map[string]core.Value{}, false, false)

	// BindMethod reads InstStack[0] as the receiver; populate it.
	env.InstStack = append([]core.Value{instance}, env.InstStack...)

	// Body: (return 42) — the `return` builtin in NewEnv panics
	// ReturnSignal{Value: TvNum(42)}, BindMethod catches it, returns 42.
	body := core.Branch{core.Leaf("return"), core.Leaf("42")}

	fn := core.BindMethod(body, []string{"self"}, ctx)
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
	body := core.Branch{core.Leaf("return"), core.Leaf(`"hi"`)}

	fn := core.BindFun(body, []string{}, ctx)
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
			got := stringifyValue(tc.in)
			if got != tc.want {
				t.Errorf("stringifyValue(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBindFunBareReturn — (return) with no args yields Nil.
func TestBindFunBareReturn(t *testing.T) {
	env := NewEnv()
	ctx := core.Context{Env: &env}

	body := core.Branch{core.Leaf("return")}

	fn := core.BindFun(body, []string{}, ctx)
	result := fn(ctx, nil)

	if result.Kind != core.KindNil {
		t.Errorf("expected nil kind from bare (return), got %q", result.Kind)
	}
}
