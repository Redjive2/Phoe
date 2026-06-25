package syntax

import "testing"

// The `&do` block helper turns the rest of the enclosing form into a single
// do-block as the block's body: `(f &do a b)` lowers to `(f &(do a b))`, which
// the `&` sigil then makes a one-argument `it` function. Plain `&expr` and
// `&literal` are left as-is for the sigil lowering to wrap.
func TestNormalizeAmpDo(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"&do captures the rest of the form",
			`(list.Map &do (let p = (+ it 1)) (* p 2))`,
			`(list.Map &(do (let p = (+ it 1)) (* p 2)))`,
		},
		{
			"&do with a single statement",
			`(f &do (+ it 1))`,
			`(f &(do (+ it 1)))`,
		},
		{
			"&do stops at an elif/else boundary",
			`(if c then &do a b else &do x)`,
			`(if c then &(do a b) else &(do x))`,
		},
		{
			"plain &expr is untouched here (sigil lowering wraps it)",
			`(f &(+ it 1))`,
			`(f &(+ it 1))`,
		},
		{
			"&literal is untouched",
			`(f &none)`,
			`(f &none)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeToString(t, tc.src); got != tc.want {
				t.Errorf("NormalizeDo(%s)\n  got  %s\n  want %s", tc.src, got, tc.want)
			}
		})
	}
}
