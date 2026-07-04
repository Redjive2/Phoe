package lint

import "testing"

// Features.md §10: a type's declared name must be Title-Kebab-Case. PascalCase
// (MyType) is rejected; the hyphenated form (My-Type) is clean. Enforced for
// every type-introducing head: struct, type, trait, template.
func TestBadTypeNameStruct(t *testing.T) {
	bad := analyze(t, "(struct MyType #v)\n")
	if !hasDiagWithName(bad, "bad-type-name", "MyType") {
		t.Fatalf("PascalCase struct name MyType must be flagged, got %#v", bad)
	}
	good := analyze(t, "(struct My-Type #v)\n")
	if hasDiag(good, "bad-type-name") {
		t.Fatalf("Title-Kebab struct name My-Type must be clean, got %#v", good)
	}
}

func TestBadTypeNameTypeAlias(t *testing.T) {
	bad := analyze(t, "(type MyAlias Number)\n")
	if !hasDiagWithName(bad, "bad-type-name", "MyAlias") {
		t.Fatalf("PascalCase type alias MyAlias must be flagged, got %#v", bad)
	}
	good := analyze(t, "(type My-Alias Number)\n")
	if hasDiag(good, "bad-type-name") {
		t.Fatalf("Title-Kebab alias My-Alias must be clean, got %#v", good)
	}
}

func TestBadTypeNameTrait(t *testing.T) {
	bad := analyze(t, "(trait MyTrait (method Self.conv (Self) Self))\n")
	if !hasDiagWithName(bad, "bad-type-name", "MyTrait") {
		t.Fatalf("PascalCase trait name MyTrait must be flagged, got %#v", bad)
	}
	good := analyze(t, "(trait My-Trait (method Self.conv (Self) Self))\n")
	if hasDiag(good, "bad-type-name") {
		t.Fatalf("Title-Kebab trait name My-Trait must be clean, got %#v", good)
	}
}

func TestBadTypeNameTemplate(t *testing.T) {
	bad := analyze(t, "(template MyParam)\n(struct Box (MyParam))\n")
	if !hasDiagWithName(bad, "bad-type-name", "MyParam") {
		t.Fatalf("PascalCase template param MyParam must be flagged, got %#v", bad)
	}
	// Single-capital-letter params (the idiomatic A/T/I) classify as valid
	// Title-Kebab type names and must stay clean.
	good := analyze(t, "(template T)\n(struct Box (T))\n")
	if hasDiag(good, "bad-type-name") {
		t.Fatalf("single-letter template param T must be clean, got %#v", good)
	}
}

// §10 also governs type REFERENCES, not just declarations. A malformed type
// name used in a signature slot or result type is flagged even though the
// signature body is erased and never walked as code.
func TestBadTypeNameSigSlot(t *testing.T) {
	src := "(struct MyType #v)\n"
	bad := analyze(t, src+"(fun take (MyType) None)\n")
	// The reference in the sig slot is flagged (in addition to the decl).
	n := 0
	for _, d := range bad {
		if d.Code == "bad-type-name" {
			n++
		}
	}
	if n < 2 {
		t.Fatalf("expected the MyType decl AND its sig-slot reference flagged, got %d bad-type-name in %#v", n, bad)
	}
	good := analyze(t, "(struct My-Type #v)\n(fun take (My-Type) None)\n")
	if hasDiag(good, "bad-type-name") {
		t.Fatalf("Title-Kebab type in a sig slot must be clean, got %#v", good)
	}
}

// A malformed result type is flagged too.
func TestBadTypeNameSigResult(t *testing.T) {
	bad := analyze(t, "(struct My-Type #v)\n(type Bad-Alias My-Type)\n(fun mk () BadResult)\n")
	if !hasDiagWithName(bad, "bad-type-name", "BadResult") {
		t.Fatalf("PascalCase result type BadResult must be flagged, got %#v", bad)
	}
}

// A type reference in value position (`.is?`) resolves to the type and is held
// to the same rule.
func TestBadTypeNameValueRef(t *testing.T) {
	bad := analyze(t, "(struct MyType #v)\n(fun check (x) do (x.is? MyType))\n")
	if !hasDiagWithName(bad, "bad-type-name", "MyType") {
		t.Fatalf("a `.is? MyType` reference must be flagged, got %#v", bad)
	}
}

// A lowercase name in type position is not a value escape hatch — types are
// Title-Kebab-Case, so a lowercase struct name is rejected too.
func TestBadTypeNameLowercase(t *testing.T) {
	bad := analyze(t, "(struct thing #v)\n")
	if !hasDiagWithName(bad, "bad-type-name", "thing") {
		t.Fatalf("lowercase struct name thing must be flagged, got %#v", bad)
	}
}
