package builtins

import (
	"testing"

	"pho/pkg/core"
	"pho/pkg/diag"
	"pho/pkg/syntax"
)

// evalInPackage runs Pho source the way modload does — under a Context with a
// real Package, so package-scoped declarations (type extensions via
// method/property) have somewhere to live. Returns the value of the LAST
// top-level form. diagReport, when non-nil, receives every diagnostic code.
func evalInPackage(t *testing.T, src string, diagReport func(string)) core.Value {
	t.Helper()
	env := NewEnv()
	pkg := &core.Package{
		Path:    "test",
		Files:   map[string]*core.File{},
		Exports: map[string]core.Value{},
		Env:     &env,
	}
	file := &core.File{Mode: core.ModeProgram, Imports: map[string]core.Value{}, Pkg: pkg}
	ctx := core.Context{Env: &env, Package: pkg, File: file}
	if diagReport != nil {
		s := diag.NewSession()
		s.Report = func(e diag.RuntimeError) { diagReport(e.Code) }
		ctx.Diag = s
	}
	ctx.PushFrame()

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

func TestPrimitiveMethod(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want float64
	}{
		{
			"zero-arg method on a literal",
			"(method Number.double (self) do (* self 2))\n(5.double)",
			10,
		},
		{
			"method with an argument",
			"(method Number.plus (self n) do (+ self n))\n(10.plus 5)",
			15,
		},
		{
			"method on a bound variable",
			"(method Number.square (self) do (* self self))\n(let n = 7)\n(n.square)",
			49,
		},
		{
			// `x.M` yields a bound method reference; each (…) call applies it,
			// so a method's result can take another method call.
			"call on a method result",
			"(method Number.inc (self) do (+ self 1))\n(((3.inc).inc).inc)",
			6,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evalInPackage(t, c.src, nil)
			if got.Kind != core.KindNum {
				t.Fatalf("%s: got kind %q, want num", c.name, got.Kind)
			}
			if got.Val.(float64) != c.want {
				t.Errorf("%s: got %v, want %v", c.name, got.Val, c.want)
			}
		})
	}
}

func TestPrimitiveProperty(t *testing.T) {
	// A read-only computed property on Number, backed by an anonymous method.
	// A property is read WITHOUT a call — `n.Zero?` is the value directly
	// (unlike a method `(n.Method)`, which is a call on a method reference).
	const decl = "(property Number.zero? get (method Number (self) do (== self 0)))\n"

	if got := evalInPackage(t, decl+"0.zero?", nil); got.Kind != core.KindBool || got.Val.(bool) != true {
		t.Fatalf("0.Zero? = %v (%s), want True", got.Val, got.Kind)
	}
	if got := evalInPackage(t, decl+"5.zero?", nil); got.Kind != core.KindBool || got.Val.(bool) != false {
		t.Fatalf("5.Zero? = %v (%s), want False", got.Val, got.Kind)
	}
}

func TestPrimitiveMethodUnknownMember(t *testing.T) {
	// Accessing an undefined member is a clean error (Nil after the
	// diagnostic), not a panic.
	var codes []string
	got := evalInPackage(t, "(5.nope)", func(c string) { codes = append(codes, c) })
	if got.Kind != core.KindNil {
		t.Fatalf("(5.Nope) = %v (%s), want Nil after diagnostic", got.Val, got.Kind)
	}
	if len(codes) == 0 {
		t.Errorf("expected a diagnostic for an undefined member")
	}
}

func TestPrimitiveMethodDuplicate(t *testing.T) {
	// Declaring the same (type, member) twice in one package is rejected.
	var codes []string
	evalInPackage(t,
		"(method Number.dup (self) do self)\n(method Number.dup (self) do self)",
		func(c string) { codes = append(codes, c) })
	if !hasCode(codes, core.ErrRedeclare) {
		t.Fatalf("expected a %q diagnostic for a duplicate primitive method; got %v", core.ErrRedeclare, codes)
	}
}

// The built-in module (pkg/builtins/pho/*.phl) is always in scope: .Size /
// .Keys / .Empty? on collections (replacing len/keyof), loaded via the
// BuiltinExtensions hook on first member resolution.
func TestBuiltinModuleCollectionMethods(t *testing.T) {
	num := func(src string, want float64) {
		t.Helper()
		got := evalInPackage(t, src, nil)
		if got.Kind != core.KindNum || got.Val.(float64) != want {
			t.Errorf("%s = %v (%s), want %v", src, got.Val, got.Kind, want)
		}
	}
	// .Size / .Keys / .Empty? are PROPERTIES — read without a call.
	num("[1 2 3].size", 3)
	num("'café'.size", 4) // rune count, not bytes
	num("[ 'a' -> 1 'b' -> 2 ].size", 2)

	keys := evalInPackage(t, "[10 20 30].keys", nil)
	if keys.Kind != core.KindArray {
		t.Fatalf("([10 20 30].Keys) kind = %s, want array", keys.Kind)
	}
	ks := *keys.Val.(*[]core.Value)
	if len(ks) != 3 || ks[0].Val.(float64) != 0 || ks[1].Val.(float64) != 1 || ks[2].Val.(float64) != 2 {
		t.Errorf("([10 20 30].Keys) = %v, want [0 1 2]", ks)
	}

	if got := evalInPackage(t, "[].empty?", nil); got.Kind != core.KindBool || !got.Val.(bool) {
		t.Errorf("[].Empty? = %v, want True", got.Val)
	}
	if got := evalInPackage(t, "[1].empty?", nil); got.Kind != core.KindBool || got.Val.(bool) {
		t.Errorf("[1].Empty? = %v, want False", got.Val)
	}
}

// The universal methods Is? / In? on the top type apply to every value.
func TestBuiltinUniversalMethods(t *testing.T) {
	boolean := func(src string, want bool) {
		t.Helper()
		got := evalInPackage(t, src, nil)
		if got.Kind != core.KindBool || got.Val.(bool) != want {
			t.Errorf("%s = %v (%s), want %v", src, got.Val, got.Kind, want)
		}
	}
	boolean("(5.is? Number)", true)
	boolean("(5.is? String)", false)
	boolean("('hi'.is? String)", true)
	boolean("([1 2 3].is? List)", true)

	boolean("(2.in? [1 2 3])", true)
	boolean("(9.in? [1 2 3])", false)
}
