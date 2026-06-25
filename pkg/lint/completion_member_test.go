package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func defNames(defs []Definition) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names
}

func containsName(defs []Definition, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// `p.` on a struct instance completes public fields and methods only.
func TestDotCompletionOnInstance(t *testing.T) {
	src := `(struct Point X y)
(method Point.Shift (self d) (+ self.X d))
(method Point.tweak (self d) (+ self.y d))
(var p Point.{ X 10 y 20 })
(var q p.)
`
	// Cursor right after "p." on line 5 (col 10 = just past the dot).
	defs := CompletionsAt("main.pho", []byte(src), 5, 10)
	if !containsName(defs, "X") || !containsName(defs, "Shift") {
		t.Fatalf("expected X and Shift in completions, got %v", defNames(defs))
	}
	if containsName(defs, "y") || containsName(defs, "tweak") {
		t.Fatalf("private members must be filtered outside methods, got %v", defNames(defs))
	}
}

// `self.` inside a method completes private members too.
func TestDotCompletionOnSelf(t *testing.T) {
	src := `(struct Point X y)
(method Point.M (self) (identity do
  (var a self.)))
`
	defs := CompletionsAt("main.pho", []byte(src), 3, 15)
	if !containsName(defs, "y") {
		t.Fatalf("self completion must include private fields, got %v", defNames(defs))
	}
}

// Partial member already typed: `p.Sh` still completes members.
func TestDotCompletionPartialMember(t *testing.T) {
	src := `(struct Point X y)
(method Point.Shift (self d) (+ self.X d))
(var p Point.{ X 10 y 20 })
(var q p.Sh)
`
	defs := CompletionsAt("main.pho", []byte(src), 4, 12)
	if !containsName(defs, "Shift") {
		t.Fatalf("expected Shift for partial member, got %v", defNames(defs))
	}
}

// Dict receivers complete their known keys as bracket-index forms.
func TestDotCompletionOnDict(t *testing.T) {
	src := `(var d {'alpha' 1 'beta' 2})
(var x d.)
`
	defs := CompletionsAt("main.pho", []byte(src), 2, 10)
	if !containsName(defs, `['alpha']`) || !containsName(defs, `['beta']`) {
		t.Fatalf("expected bracket-index key suggestions, got %v", defNames(defs))
	}
}

// Import receivers complete package exports.
func TestDotCompletionOnImport(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "mylib")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(pkgDir, "lib.phl")
	if err := os.WriteFile(lib, []byte(`(fun Visible () 1)
(fun hidden () 2)
(struct Thing Part)
`), 0o644); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "main.pho")
	src := "(import '" + pkgDir + "')\n(var x mylib.)\n"
	if err := os.WriteFile(main, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	col := len("(var x mylib.") + 1
	defs := CompletionsAt(main, []byte(src), 2, col)
	if !containsName(defs, "Visible") || !containsName(defs, "Thing") {
		t.Fatalf("expected exports in completions, got %v", defNames(defs))
	}
	if containsName(defs, "hidden") {
		t.Fatalf("lowercase decls aren't exported, got %v", defNames(defs))
	}
}

// Without a dot context, completion falls back to scope names.
func TestPlainCompletionStillWorks(t *testing.T) {
	src := `(var value 1)
(var x )
`
	defs := CompletionsAt("main.pho", []byte(src), 2, 9)
	if !containsName(defs, "value") {
		t.Fatalf("expected scope completion fallback, got %v", strings.Join(defNames(defs), ", "))
	}
}
