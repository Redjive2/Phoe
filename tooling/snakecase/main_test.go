package main

import "testing"

func TestTransform(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// Literal + receiver renames.
		{"nil", "Nil", "none"},
		{"bools", "(f True False)", "(f true false)"},
		{"self", "(method P.M (Self) Self.x)", "(method P.M (self) self.x)"},

		// const / var → let.
		{"const", "(const x 5)", "(let x = 5)"},
		{"var", "(var x 5)", "(let var x = 5)"},
		{"const multi", "(const a 1 b 2)", "(let a = 1 b = 2)"},
		{"const typed", "(const (Number x) 5)", "(let (Number x) = 5)"},
		{"const nil value", "(const x Nil)", "(let x = none)"},

		// maps {} → [] with arrows.
		{"map", "{:k :v}", "[:k -> :v]"},
		{"map multi", "{:a 1 :b 2}", "[:a -> 1 :b -> 2]"},
		{"empty map", "{}", "[]"},

		// Nesting: a map as a const value.
		{"map in const", "(const m {:k :v})", "(let m = [:k -> :v])"},

		// Untouched forms.
		{"fun", "(fun f (a) a)", "(fun f (a) a)"},
		{"list stays", "[1 2 3]", "[1 2 3]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := Transform(c.in)
			if err != nil {
				t.Fatalf("Transform(%q) error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Transform(%q)\n  got  %q\n  want %q", c.in, got, c.want)
			}
		})
	}
}
