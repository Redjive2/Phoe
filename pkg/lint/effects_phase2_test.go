package lint

import (
	"testing"

	"pho/pkg/ast"
	"pho/pkg/syntax"
)

func TestEffectSetModel(t *testing.T) {
	s := effectSet{}
	if !s.pure() || s.String() != "pure" {
		t.Fatalf("empty set should be pure, got %q", s.String())
	}
	// A self-mutation drives '=', not '!'.
	s.add(fxMutatesSelf)
	if s.pure() || !s.has(fxMutatesSelf) {
		t.Fatalf("expected mutates-self present and not pure")
	}
	if !s.needsEquals() || s.needsBang() {
		t.Fatalf("mutates-self needs '=' only, got needsEquals=%v needsBang=%v", s.needsEquals(), s.needsBang())
	}
	if s.String() != "mutates-self" {
		t.Fatalf("String() = %q, want mutates-self", s.String())
	}
	// A named environmental effect (a called `!`-function) drives '!'. String()
	// renders the union sorted.
	s.add("print-line!")
	if !s.needsBang() {
		t.Fatalf("a `!`-call should need '!'")
	}
	if s.String() != "mutates-self, print-line!" {
		t.Fatalf("String() = %q, want 'mutates-self, print-line!'", s.String())
	}
	if got := s.union(effectSet{}); !got.has(fxMutatesSelf) || !got.has("print-line!") {
		t.Fatalf("union with the empty set dropped effects: %v", got)
	}
}

func TestDeclaredEffectful(t *testing.T) {
	cases := map[string]bool{
		"Append!": true, "grow!": true, "#grow!": true,
		"append": false, "atom?": false, "self": false, "!": false,
	}
	for name, want := range cases {
		if got := declaredEffectful(name); got != want {
			t.Errorf("declaredEffectful(%q) = %v, want %v", name, got, want)
		}
	}
}

// paramListOf parses a `(method …)` form and returns its parameter-list node
// (children: method, Recv.Name, params, body → index 2).
func paramListOf(t *testing.T, methodSrc string) ast.PNode {
	t.Helper()
	toks, _ := syntax.LexPos(methodSrc)
	tree, _ := syntax.ParsePos(toks)
	if len(tree) == 0 {
		t.Fatalf("no top-level form parsed from %q", methodSrc)
	}
	br, ok := tree[0].(*ast.PBranch)
	if !ok || len(br.Children) < 3 {
		t.Fatalf("unexpected method shape for %q", methodSrc)
	}
	return br.Children[2]
}

func TestParamMutabilityExtraction(t *testing.T) {
	mut := paramMutability(paramListOf(t, "(let T.Bump! ((var self) by) = self)"))
	if len(mut) != 2 || !mut[0] || mut[1] {
		t.Fatalf("(var self) by → %v, want [true false]", mut)
	}
	if !receiverMutable(paramListOf(t, "(let T.Bump! ((var self) by) = self)")) {
		t.Fatalf("(var self) receiver should be mutable")
	}
	if receiverMutable(paramListOf(t, "(let T.Total (self) = self)")) {
		t.Fatalf("plain (self) receiver must NOT be mutable")
	}
	// (spread …) / (optional …) are not (var …) slots.
	mut = paramMutability(paramListOf(t, "(let T.M ((var self) (optional x) (spread r)) = self)"))
	if len(mut) != 3 || !mut[0] || mut[1] || mut[2] {
		t.Fatalf("mixed params → %v, want [true false false]", mut)
	}
}
