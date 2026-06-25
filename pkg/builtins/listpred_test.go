package builtins

import (
	"testing"

	"pho/pkg/core"
)

// (list? x) is True only for arrays. It backs core.Flatten's decision to
// splice a nested list versus keep a scalar element. Driven through the
// full pipeline (evalProgram), but uses no `fun`/std so it's independent of
// any in-flight declaration-syntax changes.
func TestListPredicate(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"(list? [1 2 3])", true},
		{"(list? [])", true},
		{"(list? 5)", false},
		{"(list? 'ab')", false},
		{"(list? true)", false},
		{"(list? none)", false},
		{"(list? ['a' -> 1])", false},
	}
	for _, c := range cases {
		v := evalProgram(t, c.src)
		if v.Kind != core.KindBool {
			t.Fatalf("%s: kind = %s, want bool", c.src, v.Kind)
		}
		if v.Val.(bool) != c.want {
			t.Errorf("%s = %v, want %v", c.src, v.Val, c.want)
		}
	}
}

func TestListPredicateArity(t *testing.T) {
	for _, src := range []string{"(list?)", "(list? 1 2)"} {
		if _, codes := evalProgramDiag(t, src); !hasCode(codes, core.ErrArity) {
			t.Fatalf("%s: expected arity error, got %v", src, codes)
		}
	}
}
