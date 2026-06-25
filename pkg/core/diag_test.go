package core

import (
	"testing"

	"pho/pkg/diag"
)

// TestSetTriState pins the SetResult contract '=' relies on: OK for a
// mutable binding, Const for constants, Missing for undeclared names.
func TestSetTriState(t *testing.T) {
	globals := map[string]tStackEntry{}
	env := tenv{Globals: &globals}
	ctx := Context{Env: &env}
	ctx.PushFrame()

	if !ctx.Declare("x", TvNum(1), false) {
		t.Fatal("Declare x failed")
	}
	if !ctx.Declare("k", TvNum(2), true) {
		t.Fatal("Declare k failed")
	}

	if got := ctx.Set("x", TvNum(10)); got != SetOK {
		t.Errorf("Set x = %v, want SetOK", got)
	}
	if v, _ := ctx.Resolve("x"); v.Val.(float64) != 10 {
		t.Errorf("x = %v after Set, want 10", v.Val)
	}
	if got := ctx.Set("k", TvNum(10)); got != SetConst {
		t.Errorf("Set k = %v, want SetConst", got)
	}
	if v, _ := ctx.Resolve("k"); v.Val.(float64) != 2 {
		t.Errorf("k = %v after rejected Set, want 2", v.Val)
	}
	if got := ctx.Set("nope", TvNum(10)); got != SetMissing {
		t.Errorf("Set nope = %v, want SetMissing", got)
	}
}

// TestErrorfNilSessionSafe: a bare Context (no session, no file) must
// not panic on Errorf/Warnf — it falls back to a plain stderr line —
// and the value contract (TvNil) holds.
func TestErrorfNilSessionSafe(t *testing.T) {
	ctx := Context{}
	if v := ctx.Errorf(ErrUnresolved, "x"); v != TvNil {
		t.Errorf("Errorf = %v, want TvNil", v)
	}
	if v := ctx.Warnf(ErrUnresolved, "x"); v != TvNil {
		t.Errorf("Warnf = %v, want TvNil", v)
	}
}

// TestSessionCounts: errors and warnings count separately; the hook
// receives each emitted diagnostic.
func TestSessionCounts(t *testing.T) {
	s := diag.NewSession()
	var seen []diag.RuntimeError
	s.Report = func(e diag.RuntimeError) { seen = append(seen, e) }

	ctx := Context{Diag: s}
	ctx.Errorf(ErrUnresolved, "a")
	ctx.Errorf(ErrArity, "b")
	ctx.Warnf(ErrUnresolved, "c")

	if s.ErrorCount() != 2 {
		t.Errorf("ErrorCount = %d, want 2", s.ErrorCount())
	}
	if s.WarningCount() != 1 {
		t.Errorf("WarningCount = %d, want 1", s.WarningCount())
	}
	if len(seen) != 3 {
		t.Fatalf("Report hook saw %d diagnostics, want 3", len(seen))
	}
	if seen[1].Code != ErrArity || seen[1].Message != "b" {
		t.Errorf("second diagnostic = %+v", seen[1])
	}
	if seen[2].Severity != diag.SeverityWarning {
		t.Errorf("third diagnostic severity = %v, want warning", seen[2].Severity)
	}
}

// TestEmitExpansion: an error raised under an expansion context attaches
// the generated code as a secondary excerpt, while the primary span stays
// the call site (ctx.At).
func TestEmitExpansion(t *testing.T) {
	s := diag.NewSession()
	var seen []diag.RuntimeError
	s.Report = func(e diag.RuntimeError) { seen = append(seen, e) }

	callSite := Span{StartLine: 9, StartCol: 1, EndLine: 9, EndCol: 13}
	ctx := Context{Diag: s, At: &callSite}
	ectx := ctx.WithExpansion("evilMacro", "(fake_fn arg)")
	ectx.Errorf(ErrUnresolved, "operation 'fakeFn' is not defined")

	if len(seen) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(seen))
	}
	if seen[0].Span != callSite {
		t.Errorf("primary span = %+v, want call site %+v", seen[0].Span, callSite)
	}
	exp := seen[0].Expansion
	if exp == nil {
		t.Fatal("Expansion not attached")
	}
	if exp.Macro != "evilMacro" || exp.Source != "(fake_fn arg)" {
		t.Errorf("expansion = %+v", exp)
	}
	// Phase A carets the whole generated form (one line, full width).
	wantSpan := Span{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: len("(fake_fn arg)") + 1}
	if exp.Span != wantSpan {
		t.Errorf("expansion span = %+v, want %+v", exp.Span, wantSpan)
	}

	// An error outside any expansion carries none.
	ctx.Errorf(ErrUnresolved, "x")
	if seen[1].Expansion != nil {
		t.Errorf("non-expansion error should have no Expansion, got %+v", seen[1].Expansion)
	}
}

// TestErrorfAt: ErrorfAt carets the given node's span when it carries
// one, and falls back to the current ctx.At otherwise.
func TestErrorfAt(t *testing.T) {
	s := diag.NewSession()
	var seen []diag.RuntimeError
	s.Report = func(e diag.RuntimeError) { seen = append(seen, e) }

	argSpan := Span{StartLine: 2, StartCol: 6, EndLine: 2, EndCol: 13}
	enclosing := Span{StartLine: 2, StartCol: 1, EndLine: 2, EndCol: 20}

	// A span-wrapped node: ErrorfAt should use its span, not ctx.At.
	wrapped := WithSpan(Branch{Leaf("+")}, argSpan)
	ctx := Context{Diag: s, At: &enclosing}
	ctx.ErrorfAt(wrapped, ErrType, "bad arg")
	if seen[0].Span != argSpan {
		t.Errorf("ErrorfAt(wrapped).Span = %+v, want arg span %+v", seen[0].Span, argSpan)
	}

	// A bare leaf has no span → fall back to ctx.At (the enclosing form).
	ctx.ErrorfAt(Leaf("x"), ErrType, "bad arg")
	if seen[1].Span != enclosing {
		t.Errorf("ErrorfAt(leaf).Span = %+v, want enclosing %+v", seen[1].Span, enclosing)
	}
}
