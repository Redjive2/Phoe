package lint

import "testing"

// Generic INSTANTIATION: a construction's type arguments are inferred from the
// field values and threaded through, so a generic field access `c.field`
// resolves to the concrete type argument downstream — checked precisely, not
// left as the bare (gradual) variable.
func TestGenericInstantiation(t *testing.T) {
	box := "(template (Number B))\n(struct Box { B v })\n(const c Box.{ v = 5 })\n"
	pair := "(template U B)\n(struct Pair { U a B b })\n(const p Pair.{ a = 5 b = 'x' })\n"
	gStr := "(fun g (String) None)\n(let g (s) = none)\n"
	gNum := "(fun g (Number) None)\n(let g (n) = none)\n"
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// c.v was constructed from 5, so it instantiates to Number.
		{"instantiated field clashes downstream", box + gStr + "(g c.v)", true},
		{"instantiated field matches downstream", box + gNum + "(g c.v)", false},
		// Two-parameter generic: a -> Number, b -> String, independently.
		{"pair first arg (Number) vs String", pair + gStr + "(g p.a)", true},
		{"pair second arg (String) vs String", pair + gStr + "(g p.b)", false},
		{"pair second arg (String) vs Number", pair + gNum + "(g p.b)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "type-mismatch"); got != c.wantErr {
				t.Errorf("type-mismatch=%v, want %v\n  src: %q", got, c.wantErr, c.src)
			}
		})
	}
}
