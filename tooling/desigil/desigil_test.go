package main

import "testing"

// TestTransform_strips covers the structural slots where a sigil is pure
// boilerplate and must be removed.
func TestTransform_strips(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"fun named", "(fun 'add '(x y) '(+ x y))", "(fun add (x y) (+ x y))"},
		{"fun anon", "(var 'f (fun '(n) '(* n 2)))", "(var f (fun (n) (* n 2)))"},
		{"fun body leaf", "(fun 'id '(x) 'x)", "(fun id (x) x)"},
		{"method", "(method Point 'Shift '(self d) '(+ self.X d))", "(method Point Shift (self d) (+ self.X d))"},
		{"struct", "(struct 'Point '(X y))", "(struct Point (X y))"},
		{"const pairs", "(const 'PI 3 'E 2)", "(const PI 3 E 2)"},
		{"var single", "(var 'x 5)", "(var x 5)"},
		{"assign ident", "(= 'x 10)", "(= x 10)"},
		{"if then-else", "(if (< x 10) &(small) &(big))", "(if (< x 10) (small) (big))"},
		{"if then-only", "(if c &(go))", "(if c (go))"},
		{"for while-style", "(for &(< i 10) &(step))", "(for (< i 10) (step))"},
		{"for iterator", "(for 'e xs &(use e))", "(for e xs (use e))"},
		{"nested fun in body", "(fun 'o '() '(do (fun 'i '(x) '(+ x 1)) (i 1)))", "(fun o () (identity do (fun i (x) (+ x 1)) (i 1)))"},
		{"spread param", "(fun 'all '((spread args)) '(io.P (spread args)))", "(fun all ((spread args)) (io.P (spread args)))"},
		{
			"multiline",
			"(fun 'add\n     '(x y)\n     '(+ x y))",
			"(fun add\n     (x y)\n     (+ x y))",
		},
		{"trailing comment", "(fun 'f '() '(g)) -- note", "(fun f () (g)) -- note"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n, err := Transform(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("\n in:   %q\n got:  %q\n want: %q", c.in, got, c.want)
			}
			if n == 0 {
				t.Errorf("expected sigils removed, got 0")
			}
		})
	}
}

// TestTransform_wrapsDo covers the identity-wrap of standalone do-forms,
// which over-nest under do-notation once their wrapping sigil is gone.
func TestTransform_wrapsDo(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"standalone top-level", "(do (a) (b))", "(identity do (a) (b))"},
		{"fun body", "(fun 'f '() '(do (a) (b)))", "(fun f () (identity do (a) (b)))"},
		{"method body", "(method M 'm '(self) '(do (a) self))", "(method M m (self) (identity do (a) self))"},
		{"if arm", "(if c &(do (a) (b)) &(c))", "(if c (identity do (a) (b)) (c))"},
		{"for-while body", "(for &(< i 3) &(do (step) (= 'i (+ i 1))))", "(for (< i 3) (identity do (step) (= i (+ i 1))))"},
		{"nested do", "(do (a) (do (b) (c)))", "(identity do (a) (identity do (b) (c)))"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := Transform(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("\n in:   %q\n got:  %q\n want: %q", c.in, got, c.want)
			}
		})
	}
}

// TestTransform_doNoOps pins the do-forms that must NOT be wrapped: inline
// do-notation (do already has a head before it), an already-wrapped form,
// and a do inside data (a genuine quote).
func TestTransform_doNoOps(t *testing.T) {
	cases := []struct{ name, in string }{
		{"inline do-notation", "(foo a do b c)"},
		{"already wrapped", "(identity do (a) (b))"},
		{"do inside data quote", "(pause '(do a b))"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n, err := Transform(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.in || n != 0 {
				t.Errorf("expected no-op\n in:  %q\n got: %q (edits %d)", c.in, got, n)
			}
		})
	}
}

// TestTransform_preserves is the heart of the value-position rule: a quote
// that could just as well be a number, string, or expression stays. These
// must all be exact no-ops.
func TestTransform_preserves(t *testing.T) {
	cases := []struct{ name, in string }{
		{"map quoted keys", "(map 'a 1 'b 2)"},
		{"map number keys", "(map 1 \"one\" 2 \"two\")"},
		{"dict literal keys", "{'a 1 \"b\" 2}"},
		{"array quoted element", "[1 'two 3]"},
		{"import alias", "(goimport [\"stdDependencies\" 'dep])"},
		{"struct initializer dict", "(Point {'X 10 'y 20})"},
		{"quote inside body is data", "(pause '(a b))"},
		{"macro call args are data", "(myMacro! 'a (fun '(x) '(x)))"},
		{"hand-written block", "(block '(f))"},
		{"ampersand thunk value", "(runLater &(expensive))"},
		{"assign dot target", "(= p.X 100)"},
		{"already bare", "(fun add (x y) (+ x y))"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n, err := Transform(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.in || n != 0 {
				t.Errorf("expected no-op\n in:  %q\n got: %q (removed %d)", c.in, got, n)
			}
		})
	}
}

// TestTransform_valueQuoteSurvivesNameStrip pins the subtle case where one
// form has both a structural slot (stripped) and an adjacent value slot
// holding a quote (kept).
func TestTransform_valueQuoteSurvivesNameStrip(t *testing.T) {
	got, _, err := Transform("(var 'sym 'hello)")
	if err != nil {
		t.Fatal(err)
	}
	if want := "(var sym 'hello)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestTransform_idempotent verifies a second pass is a no-op.
func TestTransform_idempotent(t *testing.T) {
	once, _, err := Transform("(fun 'add '(x y) '(+ x y))")
	if err != nil {
		t.Fatal(err)
	}
	twice, n, err := Transform(once)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || twice != once {
		t.Errorf("not idempotent: %q -> %q (removed %d)", once, twice, n)
	}
}

// TestTransform_refusesMalformed ensures a parse error leaves the source
// untouched rather than risking a corrupt rewrite.
func TestTransform_refusesMalformed(t *testing.T) {
	src := "(fun 'add '(x y)" // unclosed
	got, n, err := Transform(src)
	if err == nil {
		t.Fatalf("expected an error for malformed input")
	}
	if got != src || n != 0 {
		t.Errorf("malformed input must be returned unchanged; got %q (removed %d)", got, n)
	}
}
