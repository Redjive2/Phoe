package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pho/pkg/syntax"
)

// Object-model dot completion: a collection receiver offers the built-in
// members (Size/Keys/Empty?) and the universal Is?/In?; a non-collection
// offers only the universal members.
func TestDotCompletionOnPrimitives(t *testing.T) {
	// `xs.` — list receiver. Dot at col 10, cursor just past it at col 11.
	src := "(var xs [1 2 3])\n(var q xs.)\n"
	defs := CompletionsAt("main.pho", []byte(src), 2, 11)
	for _, want := range []string{"size", "keys", "empty?", "is?", "in?"} {
		if !containsName(defs, want) {
			t.Fatalf("list completion missing %q, got %v", want, defNames(defs))
		}
	}

	// `n.` — a number is not a collection: universal members yes, Size no.
	nsrc := "(var n 5)\n(var q n.)\n"
	ndefs := CompletionsAt("main.pho", []byte(nsrc), 2, 10)
	if !containsName(ndefs, "is?") || !containsName(ndefs, "in?") {
		t.Fatalf("number completion missing universal members, got %v", defNames(ndefs))
	}
	if containsName(ndefs, "size") {
		t.Fatalf("a number must not offer the collection member size, got %v", defNames(ndefs))
	}
}

// A user-declared method on a primitive type surfaces in completion: the
// walker collects it under the type name like a struct method.
func TestDotCompletionUserPrimitiveExtension(t *testing.T) {
	src := "(let Number.Double (self) = (* self 2))\n(var n 5)\n(var q n.)\n"
	defs := CompletionsAt("main.pho", []byte(src), 3, 10)
	if !containsName(defs, "Double") {
		t.Fatalf("user primitive extension Double missing from completion, got %v", defNames(defs))
	}
	if !containsName(defs, "is?") {
		t.Fatalf("universal members must still appear alongside user extensions, got %v", defNames(defs))
	}
}

// Hover and go-to-definition resolve a user-declared method on a primitive
// type, through the same machinery as struct methods.
func TestNavOnUserPrimitiveExtension(t *testing.T) {
	src := "(let Number.double (self) = (* self 2))\n(let var n = 5)\n(let var q = (n.double))\n"

	site, ok := DefinitionAt("main.pho", []byte(src), 3, 17)
	if !ok {
		t.Fatalf("expected go-to-definition to resolve n.Double")
	}
	if site.Span.StartLine != 1 {
		t.Errorf("expected the (method Number.Double …) decl on line 1, got line %d", site.Span.StartLine)
	}

	md, _, hok := HoverAt("main.pho", []byte(src), 3, 17)
	if !hok || !strings.Contains(md, "double") {
		t.Errorf("expected hover mentioning double, got ok=%v md=%q", hok, md)
	}
}

// Hover on a built-in object-model member shows a synthetic description
// (built-in members have no workspace definition site).
func TestHoverOnBuiltinMember(t *testing.T) {
	src := "(let var xs = [1 2 3])\n(let var n = xs.size)\n"
	md, _, ok := HoverAt("main.pho", []byte(src), 2, 17)
	if !ok || !strings.Contains(md, "size") || !strings.Contains(md, "built-in") {
		t.Fatalf("expected a built-in hover for xs.size, got ok=%v md=%q", ok, md)
	}
}

// TestBuiltinMemberSurfaceInSync guards against drift between the built-in
// module sources (pkg/builtins/pho/*.phl) and the member surface the linter
// mirrors in typemembers.go. It is name-level (not per-type), so it is stable
// across the module's per-type ↔ union restructurings while still catching a
// newly-added built-in member the tooling doesn't yet know about.
func TestBuiltinMemberSurfaceInSync(t *testing.T) {
	known := map[string]bool{}
	for _, members := range builtinTypeMembers {
		for _, m := range members {
			known[m.Name] = true
		}
	}
	for _, m := range universalMembers {
		known[m.Name] = true
	}

	files, err := filepath.Glob(filepath.Join("..", "builtins", "pho", "*.phl"))
	if err != nil || len(files) == 0 {
		t.Skipf("built-in module sources not found (%v); skipping drift check", err)
	}

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		tokens, _ := syntax.LexPos(string(src))
		tree, _ := syntax.ParsePos(tokens)
		tree = syntax.NormalizeDo(tree)
		for _, form := range tree {
			d, ok := declOf(form)
			if !ok || (d.Head != "method" && d.Head != "property") || d.Name == "" {
				continue
			}
			if !known[d.Name] {
				t.Errorf("built-in member %q (declared in %s as %s %s.%s) is not in the linter's member surface (typemembers.go) — add it so completion/tooling know it",
					d.Name, filepath.Base(f), d.Head, d.Owner, d.Name)
			}
		}
	}
}
