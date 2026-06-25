package syntax

import (
	"strings"
	"testing"
)

func TestClassifyIdent(t *testing.T) {
	cases := []struct {
		in   string
		want IdentKind
	}{
		// snake_case values
		{"my_var", IdentValue},
		{"foo", IdentValue},
		{"x", IdentValue},
		{"is_string?", IdentValue},
		{"parse_2", IdentValue},
		{"vec_3d", IdentValue},
		{"let", IdentValue},
		{"none", IdentValue},
		{"self", IdentValue},
		{"#secret", IdentValue},
		{"#is_empty?", IdentValue},

		// Title_Snake_Case types
		{"Type_Name", IdentType},
		{"Integer", IdentType},
		{"My_Struct", IdentType},
		{"Num", IdentType},
		{"#Secret_Type", IdentType},

		// invalid shapes
		{"myVar", IdentInvalid},     // camelCase
		{"TypeName", IdentInvalid},  // PascalCase (no underscore)
		{"HTTP", IdentInvalid},      // acronym
		{"My_var", IdentInvalid},    // type word lowercased
		{"my_Var", IdentInvalid},    // value word capitalized
		{"SCREAMING", IdentInvalid}, // all caps
		{"_foo", IdentInvalid},      // leading underscore
		{"foo_", IdentInvalid},      // trailing underscore
		{"foo__bar", IdentInvalid},  // doubled underscore
		{"Foo_", IdentInvalid},      // trailing underscore (type)
		{"9foo", IdentInvalid},      // leading digit
		{"", IdentInvalid},          // empty
		{"#", IdentInvalid},         // bare marker
	}
	for _, c := range cases {
		if got := classifyIdent(c.in); got != c.want {
			t.Errorf("classifyIdent(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLexPrivateIdentifier(t *testing.T) {
	if got := tokenValues(t, "#secret"); len(got) != 1 || got[0] != "#secret" {
		t.Fatalf("tokens = %v, want [#secret]", got)
	}
	// A '#' glued to a Title_Snake_Case type name lexes as one token too.
	if got := tokenValues(t, "#Secret_Type"); len(got) != 1 || got[0] != "#Secret_Type" {
		t.Fatalf("tokens = %v, want [#Secret_Type]", got)
	}
}

func TestLexStrayHash(t *testing.T) {
	// '#' not followed by an identifier start is a stray-marker error, but the
	// following name still lexes so the parser keeps making progress.
	toks, errs := LexPos("# foo")
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "stray '#'") {
		t.Fatalf("errs = %v, want one stray-'#' error", errs)
	}
	if len(toks) != 1 || toks[0].Value != "foo" {
		t.Fatalf("tokens = %v, want [foo]", toks)
	}
}

func TestStrictNamesOffByDefault(t *testing.T) {
	if StrictNames {
		t.Fatal("StrictNames must default to false (tolerant during migration)")
	}
	// camelCase lexes cleanly while the flag is off.
	if _, errs := LexPos("myVar"); len(errs) != 0 {
		t.Fatalf("camelCase rejected with StrictNames off: %v", errs)
	}
}

func TestStrictNamesRejectsNonConforming(t *testing.T) {
	StrictNames = true
	defer func() { StrictNames = false }()

	reject := []string{"myVar", "TypeName", "#badName"}
	for _, src := range reject {
		_, errs := LexPos(src)
		if len(errs) != 1 || !strings.Contains(errs[0].Message, "non-conforming name") {
			t.Errorf("LexPos(%q): errs=%v, want one non-conforming error", src, errs)
		}
	}

	accept := []string{"my_var", "Type_Name", "#secret", "#Secret_Type", "is_empty?"}
	for _, src := range accept {
		if _, errs := LexPos(src); len(errs) != 0 {
			t.Errorf("LexPos(%q): unexpected errs=%v", src, errs)
		}
	}
}
