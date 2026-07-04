package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const navSrc = `-- Adds one to a number.
(fun add-one (n) (+ n 1))
(struct Point x #y)
(let Point.shift (self d) = (+ self.x d))
(let var p = Point.{ x = 10 #y = 20 })
(let var total = (add-one p.x))
(= total 5)
`

// Cursor on the `AddOne` call site jumps to the fun declaration.
func TestDefinitionAtFunction(t *testing.T) {
	// Line 6 `(let var total = (add-one p.x))` — cursor inside add-one.
	site, ok := DefinitionAt("main.pho", []byte(navSrc), 6, 21)
	if !ok {
		t.Fatal("expected a definition for add-one call")
	}
	if site.Span.StartLine != 2 {
		t.Fatalf("expected add-one decl on line 2, got %d", site.Span.StartLine)
	}
}

// Cursor on `p.X` member access jumps to the field declaration in the
// struct.
func TestDefinitionAtStructField(t *testing.T) {
	col := strings.Index("(let var total = (add-one p.x))", "x)") + 1
	site, ok := DefinitionAt("main.pho", []byte(navSrc), 6, col)
	if !ok {
		t.Fatal("expected a definition for p.x member")
	}
	if site.Span.StartLine != 3 {
		t.Fatalf("expected field X decl on line 3 (struct decl), got %d", site.Span.StartLine)
	}
}

// Cursor on a method call via shape-known instance jumps to the method.
func TestDefinitionAtMethod(t *testing.T) {
	src := navSrc + "(let var shifted = (p.shift 1))\n"
	col := strings.Index("(let var shifted = (p.shift 1))", "p.shift") + 3
	site, ok := DefinitionAt("main.pho", []byte(src), 8, col)
	if !ok {
		t.Fatal("expected a definition for p.shift")
	}
	if site.Span.StartLine != 4 {
		t.Fatalf("expected shift decl on line 4, got %d", site.Span.StartLine)
	}
}

// Builtins have no jumpable definition.
func TestDefinitionAtBuiltinIsNone(t *testing.T) {
	// `fun` keyword on line 2, col 2.
	if _, ok := DefinitionAt("main.pho", []byte(navSrc), 2, 2); ok {
		t.Fatal("builtins must not resolve to a definition site")
	}
}

// Cross-file: pkg.Member jumps into the imported package's source.
func TestDefinitionAtImportMember(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "mylib")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(pkgDir, "lib.phl")
	if err := os.WriteFile(lib, []byte("(let visible () = 1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.pho")
	src := "(import '" + pkgDir + "')\n(let var x = (mylib.visible))\n"
	if err := os.WriteFile(main, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	col := strings.Index("(let var x = (mylib.visible))", "visible") + 2
	site, ok := DefinitionAt(main, []byte(src), 2, col)
	if !ok {
		t.Fatal("expected cross-file definition for mylib.Visible")
	}
	if site.File != lib {
		t.Fatalf("expected definition in %s, got %s", lib, site.File)
	}
	if site.Span.StartLine != 1 {
		t.Fatalf("expected decl on line 1 of lib, got %d", site.Span.StartLine)
	}
}

// Hover on a fun renders its signature and the doc comment above it.
func TestHoverAtFunction(t *testing.T) {
	md, _, ok := HoverAt("main.pho", []byte(navSrc), 6, 19)
	if !ok {
		t.Fatal("expected hover for add-one")
	}
	if !strings.Contains(md, "(fun add-one (n) ...)") {
		t.Fatalf("expected signature in hover, got %q", md)
	}
	if !strings.Contains(md, "Adds one to a number.") {
		t.Fatalf("expected doc comment in hover, got %q", md)
	}
}

// A struct's hover header is built from its declared fields, not a raw
// reconstruction of the branch. Mid-edit, an unclosed struct form makes
// the parser's recovery swallow following forms as extra children; the
// hover keeps only the bare field identifiers and must not dump that code.
func TestHoverStructExcludesSwallowedForms(t *testing.T) {
	// Struct form left unclosed; the two funs get swallowed as children.
	src := "(struct File\n    id\n    path\n    (fun Cwd () (dep.Cwd))\n    (fun Open (p) (dep.Open p))"
	md, _, ok := HoverAt("os.phl", []byte(src), 1, 10)
	if !ok {
		t.Fatal("expected hover on the struct name")
	}
	if !strings.Contains(md, "**struct**") || !strings.Contains(md, "`id`") || !strings.Contains(md, "`path`") {
		t.Fatalf("expected a struct hover listing fields id + path, got %q", md)
	}
	if strings.Contains(md, "fun") || strings.Contains(md, "dep.") {
		t.Fatalf("swallowed code leaked into struct hover: %q", md)
	}
}

// A valid multi-line struct still renders all its fields.
func TestHoverStructValidFields(t *testing.T) {
	src := "(struct Point \n    #x\n    #y\n    #z\n)\n"
	md, _, ok := HoverAt("p.phl", []byte(src), 1, 10)
	if !ok {
		t.Fatal("expected hover on the struct name")
	}
	for _, f := range []string{"#x", "#y", "#z"} {
		if !strings.Contains(md, "`"+f+"`") {
			t.Fatalf("expected field %s in hover, got %q", f, md)
		}
	}
}

// Hover on a type symbol renders a rich body: its kind, generic template
// parameters (with bounds), and members — struct fields+methods, trait
// methods+properties, or a type alias's target.
func TestTypeHoverRichBody(t *testing.T) {
	mustContain := func(t *testing.T, md string, subs ...string) {
		t.Helper()
		for _, s := range subs {
			if !strings.Contains(md, s) {
				t.Errorf("hover missing %q\n%s", s, md)
			}
		}
	}

	// Generic struct: kind, bound generic params, fields with types, a method
	// signature (result from the sig form).
	gs := "(template U (Some-Type B))\n(struct Container.{ U u B v })\n" +
		"(method Container.wrap (Self) B)\n(let Container.wrap (self) = self.v)\n"
	md, _, ok := HoverAt("t.pho", []byte(gs), 2, 10)
	if !ok {
		t.Fatal("expected hover on the struct name")
	}
	mustContain(t, md, "**struct**", "generic", "B <: Some-Type", "**fields**", "`u`: U", "**methods**", "(wrap) → B")

	// Trait: methods with result types, typed property with accessors.
	tr := "(trait Drawable (method self.area (self) Number) (property (String self.name) get))\n"
	md, _, ok = HoverAt("t.pho", []byte(tr), 1, 10)
	if !ok {
		t.Fatal("expected hover on the trait name")
	}
	mustContain(t, md, "**trait**", "**methods**", "area() → Number", "**properties**", "name: String (get)")

	// Type alias: what it equals.
	al := "(type Collection (Or String List Map))\n"
	md, _, ok = HoverAt("t.pho", []byte(al), 1, 8)
	if !ok {
		t.Fatal("expected hover on the alias name")
	}
	mustContain(t, md, "**type alias**", "(Or String List Map)")
}

// Hover on a shaped var names the struct-type it holds (the type label, not the
// coarse shape word).
func TestHoverAtShapedVar(t *testing.T) {
	col := strings.Index("(let var total = (add-one p.x))", "p.x") + 1
	md, _, ok := HoverAt("main.pho", []byte(navSrc), 6, col)
	if !ok {
		t.Fatal("expected hover for p")
	}
	if !strings.Contains(md, "— Point") {
		t.Fatalf("expected inferred type in hover, got %q", md)
	}
}

// Hover on a function parameter shows ONLY the parameter (not the whole
// enclosing function) plus its declared type from the signature. Covers a free
// function's param, a method's typed param, and the method receiver `self`.
func TestHoverAtParam(t *testing.T) {
	fn := "(fun scale (Number String) Boolean)\n(let scale (factor label) = (== factor 1))\n"
	cf := strings.Index("(let scale (factor label) = (== factor 1))", "factor") + 1
	md, _, ok := HoverAt("t.pho", []byte(fn), 2, cf)
	if !ok {
		t.Fatal("expected hover for param 'factor'")
	}
	if strings.Contains(md, "let scale") {
		t.Fatalf("param hover must show only the param, not the function: %q", md)
	}
	if !strings.Contains(md, "parameter — Number") {
		t.Fatalf("expected 'parameter — Number', got %q", md)
	}

	me := "(method Box.at (Self Number) String)\n(let Box.at (self i) = self.n)\n"
	ci := strings.Index("(let Box.at (self i) = self.n)", " i)") + 2
	md2, _, _ := HoverAt("t.pho", []byte(me), 2, ci)
	if !strings.Contains(md2, "parameter — Number") {
		t.Fatalf("method param 'i' expected 'parameter — Number', got %q", md2)
	}
	cs := strings.Index("(let Box.at (self i) = self.n)", "self") + 1
	md3, _, _ := HoverAt("t.pho", []byte(me), 2, cs)
	if !strings.Contains(md3, "parameter — Box") {
		t.Fatalf("receiver 'self' expected 'parameter — Box', got %q", md3)
	}
}

// References on a var finds the declaration, reads, and assignment.
func TestReferencesAtVar(t *testing.T) {
	// Cursor on `total` in its declaration (line 6).
	sites := ReferencesAt("", "main.pho", []byte(navSrc), 6, 10)
	if len(sites) != 2 {
		t.Fatalf("expected 2 reference sites for total (decl + assignment), got %#v", sites)
	}
	gotLines := []int{sites[0].Span.StartLine, sites[1].Span.StartLine}
	if gotLines[0] != 6 || gotLines[1] != 7 {
		t.Fatalf("expected references on lines 6 and 7, got %v", gotLines)
	}
}

// References on a struct member finds dot accesses plus the decl.
func TestReferencesAtMember(t *testing.T) {
	src := navSrc + "(let var more = p.x)\n"
	col := strings.Index("(let var total = (add-one p.x))", "x)") + 1
	sites := ReferencesAt("", "main.pho", []byte(src), 6, col)
	// self.X (line 4), p.X (line 6), p.X (line 8), decl X (line 3).
	if len(sites) != 4 {
		t.Fatalf("expected 4 reference sites for field X, got %#v", sites)
	}
	if sites[0].Span.StartLine != 3 {
		t.Fatalf("expected first reference to be the decl on line 3, got %d", sites[0].Span.StartLine)
	}
}

// Document symbols nest methods and fields under their struct.
func TestDocumentSymbols(t *testing.T) {
	syms := DocumentSymbols("main.pho", []byte(navSrc))
	var point *Symbol
	names := []string{}
	for i := range syms {
		names = append(names, syms[i].Name)
		if syms[i].Name == "Point" {
			point = &syms[i]
		}
	}
	for _, want := range []string{"add-one", "Point", "p", "total"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected symbol %q in outline, got %v", want, names)
		}
	}
	if point == nil {
		t.Fatal("expected Point struct symbol")
	}
	childNames := []string{}
	for _, c := range point.Children {
		childNames = append(childNames, c.Name)
	}
	wantChildren := map[string]bool{"x": false, "#y": false, "shift": false}
	for _, n := range childNames {
		if _, ok := wantChildren[n]; ok {
			wantChildren[n] = true
		}
	}
	for n, found := range wantChildren {
		if !found {
			t.Fatalf("expected %q nested under Point, got %v", n, childNames)
		}
	}
}
