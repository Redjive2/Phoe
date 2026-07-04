package main

import (
	"reflect"
	"testing"

	"pho/pkg/syntax"
)

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"myVar", []string{"my", "Var"}},
		{"PctlSpawn", []string{"Pctl", "Spawn"}},
		{"HTTPServer", []string{"HTTP", "Server"}},
		{"userID", []string{"user", "ID"}},
		{"my_var", []string{"my", "var"}},
		{"My_Struct", []string{"My", "Struct"}},
		{"foo", []string{"foo"}},
		{"vec3D", []string{"vec3", "D"}},
		{"ID", []string{"ID"}},
	}
	for _, c := range cases {
		if got := splitWords(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitWords(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToSnakeCase(t *testing.T) {
	cases := map[string]string{
		"myVar":       "my_var",
		"PctlSpawn":   "pctl_spawn",
		"Config":      "config",
		"Is":          "is",
		"Is?":         "is?",
		"IsEmpty?":    "is_empty?",
		"HTTPServer":  "http_server",
		"stripQuotes": "strip_quotes",
		"argOrField":  "arg_or_field",
		"my_var":      "my_var",
		"foo":         "foo",
		"parse2":      "parse2",
		"#myHelper":   "#my_helper",
		"#secret":     "#secret",
		"userID":      "user_id",
	}
	for in, want := range cases {
		if got := toSnakeCase(in); got != want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToTitleSnake(t *testing.T) {
	cases := map[string]string{
		"MyStruct":   "My_Struct",
		"Point":      "Point",
		"Number":     "Number",
		"HTTPServer": "Http_Server",
		"My_Struct":  "My_Struct",
		"myStruct":   "My_Struct",
		"#Secret":    "#Secret",
		"#myType":    "#My_Type",
	}
	for in, want := range cases {
		if got := toTitleSnake(in); got != want {
			t.Errorf("toTitleSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollectTypeNames(t *testing.T) {
	src := `(struct Point X y)
(struct Writer.{ Number id })
(type Pair (Or Number String))
(trait Drawable (method Self.Draw (self)))
(const config 5)
(fun helper (a) a)
`
	toks, _ := syntax.LexPos(src)
	tree, errs := syntax.ParsePos(toks)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	types := collectTypeNames(tree)

	for _, name := range []string{"Point", "Writer", "Pair", "Drawable", "Number", "Or", "String"} {
		if !types[name] {
			t.Errorf("expected %q classified as a type", name)
		}
	}
	// Values must NOT be in the type set.
	for _, name := range []string{"config", "helper", "X", "y"} {
		if types[name] {
			t.Errorf("%q is a value, must not be a type", name)
		}
	}
}

func TestBuildRenameMap(t *testing.T) {
	src := `(struct MyBox X y)
(type Pair (Or Number String))
(const PublicConst 1)
(const privateHelper 2)
(fun PublicFn (a) a)
(fun internalFn (a) a)
(var camelState 0)
`
	toks, _ := syntax.LexPos(src)
	tree, errs := syntax.ParsePos(toks)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	got := buildRenameMap(tree, true)
	want := map[string]string{
		"MyBox":         "My_Box",       // user type → Title_Snake
		"Pair":          "Pair",         // single-word type → unchanged... dropped (no-op)
		"PublicConst":   "public_const", // public value → snake, no #
		"privateHelper": "#private_helper",
		"PublicFn":      "public_fn",
		"internalFn":    "#internal_fn",
		"camelState":    "#camel_state", // private (lowercase) camelCase var
	}
	// "Pair" is a no-op rename (single Title word), so it's dropped from the map.
	delete(want, "Pair")
	for k, v := range want {
		if got[k] != v {
			t.Errorf("rename[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["Pair"]; ok {
		t.Errorf("Pair is a no-op rename and should be dropped, got %q", got["Pair"])
	}
	if _, ok := got["Number"]; ok {
		t.Errorf("builtin type Number must not be renamed")
	}
}
