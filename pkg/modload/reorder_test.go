package modload

import (
	"strings"
	"testing"

	"pho/pkg/core"
	"pho/pkg/syntax"
)

// parseForms lowers source into the top-level orderedForms the loader builds.
func parseForms(t *testing.T, src string) []orderedForm {
	t.Helper()
	toks, _ := syntax.LexPos(src)
	tree, _ := syntax.ParsePos(toks)
	lowered, ok := syntax.Lower(tree).(core.Branch)
	if !ok {
		t.Fatalf("Lower did not return a Branch")
	}
	var forms []orderedForm
	for _, f := range lowered {
		forms = append(forms, orderedForm{form: f})
	}
	return forms
}

func heads(forms []orderedForm) []string {
	out := make([]string, len(forms))
	for i, f := range forms {
		br, _ := core.AsBranch(f.form)
		if len(br) > 0 {
			h, _ := core.AsLeaf(br[0])
			out[i] = string(h)
		}
	}
	return out
}

// liftDefinitions moves every non-var/const declaration ahead of the var/const
// ones, preserving source order within each group.
func TestLiftDefinitionsPartition(t *testing.T) {
	src := `(let current_temp = Temperature.{ 'Degrees' 15 })
(method Temperature.show (self) self.degrees)
(let var counter = 0)
(struct Temperature degrees unit)
(let other = 5)
(fun helper () 1)`
	got := heads(liftDefinitions(parseForms(t, src)))

	// All definitions come first (in source order), then all let bindings
	// (module state, in source order).
	want := []string{"method", "struct", "fun", "let", "let", "let"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("partition order:\n got  %v\n want %v", got, want)
	}
}

// var/const are NOT sorted among themselves — their source order is preserved.
func TestLiftDefinitionsDoesNotSortInits(t *testing.T) {
	src := `(let a = (+ b 1))
(let b = 10)`
	got := parseForms(t, src)
	out := liftDefinitions(got)
	// Both are const; order unchanged: a still before b.
	a0, _ := core.AsBranch(out[0].form)
	n0, _ := core.AsLeaf(a0[1])
	if string(n0) != "a" {
		t.Errorf("const order should be unchanged (a then b); got first = %s", n0)
	}
}

// A correctly-ordered library is left as-is.
func TestLiftDefinitionsStable(t *testing.T) {
	src := `(struct Point x y)
(= Point.sum (self) (+ self.x self.y))
(let origin = Point.{ 'X' 0 'Y' 0 })`
	got := heads(liftDefinitions(parseForms(t, src)))
	// The method impl is now the decl/impl-split `=` form; it still lifts as a
	// definition (ahead of the `let`), which is what this test guards.
	want := []string{"struct", "=", "let"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("stable order expected %v; got %v", want, got)
	}
}
