package main

import "testing"

func TestRecase(t *testing.T) {
	renames := map[string]string{
		"PctlSpawn":     "pctl_spawn",
		"privateHelper": "#private_helper",
		"camelVar":      "#camel_var",
	}
	types := map[string]bool{"Point": true, "Box": true, "Number": true}
	goimports := map[string]bool{"dep": true}

	cases := []struct{ name, in, want string }{
		// Go-module members keep their fixed Go names (unchanged).
		{"goimport member", "(dep.PctlSpawn x)", "(dep.PctlSpawn x)"},
		// Construction field keys: public → snake, private → `#` (bare in source).
		{"construction keys", "Box.{ Width 1 height 2 }", "Box.{ width 1 #height 2 }"},
		// Top-level names + references via the package map.
		{"public ref", "(PctlSpawn 1)", "(pctl_spawn 1)"},
		{"private ref", "privateHelper", "#private_helper"},
		{"private var ref", "camelVar", "#camel_var"},

		// Params/locals: snake_case, NO `#`.
		{"param", "(fun f (myArg) myArg)", "(fun f (my_arg) my_arg)"},

		// Member access: visibility by capitalization.
		{"public member", "obj.Size", "obj.size"},
		{"private member", "self.secret", "self.#secret"},
		{"predicate member", "p.Is?", "p.is?"},
		{"type member stays", "x.Point", "x.Point"},

		// Struct fields: public → snake, private → `#`.
		{"struct fields", "(struct Box Width height)", "(struct Box width #height)"},

		// A bare type reference is Title_Snake (no-op for single word).
		{"type ref", "(p.Is? Point)", "(p.is? Point)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := Recase(c.in, renames, types, goimports)
			if err != nil {
				t.Fatalf("Recase(%q) error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Recase(%q)\n  got  %q\n  want %q", c.in, got, c.want)
			}
		})
	}
}
