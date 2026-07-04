package lint

import "testing"

// The only valid property syntax is the parenthesized accessor form
// `(property Target (get (params) body) [(set (params) body)])`. The old flat
// `get getter [set setter]` keyword form draws a `bad-form-shape` diagnostic —
// for instance, free-standing, and static properties alike.
func TestOldFlatPropertyRejected(t *testing.T) {
	cases := []string{
		"(struct P #x)\n(property P.doubled get (method P (self) (* self.#x 2)))\n",
		"(let n = 5)\n(property twice get (fun () (* n 2)))\n",
		"(struct Counter n)\n(static property Counter.zero get (method Counter (self) self.{ n = 0 }))\n",
	}
	for _, src := range cases {
		if diags := analyze(t, src); !hasDiag(diags, "bad-form-shape") {
			t.Fatalf("old flat property form must be rejected, got %#v\nsrc: %s", diags, src)
		}
	}
}

// The parenthesized accessor form lints clean — including a `?`-suffixed name,
// a setter, and the static variant.
func TestNewPropertyFormClean(t *testing.T) {
	cases := []string{
		"(let temp = 50)\n(property is-chilly? (get () (< temp 60)))\n",
		"(struct Animal is-fish?)\n" +
			"(property Animal.has-legs?\n" +
			"    (get (self) (not self.is-fish?))\n" +
			"    (set (self is-not-fish?) (= self.is-fish? (not is-not-fish?))))\n",
		"(struct Counter n)\n(static property Counter.zero (get (self) self.{ n = 0 }))\n",
	}
	for _, src := range cases {
		if diags := analyze(t, src); hasDiag(diags, "bad-form-shape") {
			t.Fatalf("new accessor property form must be clean, got %#v\nsrc: %s", diags, src)
		}
	}
}
