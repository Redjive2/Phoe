package lint

import "testing"

// Recursive and nested data types navigate through struct-typed fields: a
// field declared as a struct (directly self-referential `Next Node`, nested
// `Inner B`, or a nullable `(Or Node Nil)`) gives member access an instance
// shape, so `node.Next.Next.Value` resolves and typos fire at any depth.
func TestRecursiveStructNavigation(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantFire bool
	}{
		{"self-ref resolves", "(struct Node.{ Value Number Next Node })\n(const n Node.{ Value 1 Next Nil })\n(const a n.Next.Value)\n", false},
		{"self-ref typo fires", "(struct Node.{ Value Number Next Node })\n(const n Node.{ Value 1 Next Nil })\n(const a n.Next.Bogus)\n", true},
		{"deep chain typo fires", "(struct Node.{ Value Number Next Node })\n(const n Node.{ Value 1 Next Nil })\n(const a n.Next.Next.Nope)\n", true},
		{"nested resolves", "(struct B.{ X Number })\n(struct A.{ Inner B })\n(const x A.{ Inner B.{ X 1 } })\n(const y x.Inner.X)\n", false},
		{"nested typo fires", "(struct B.{ X Number })\n(struct A.{ Inner B })\n(const x A.{ Inner B.{ X 1 } })\n(const y x.Inner.Zap)\n", true},
		{"nullable link resolves", "(struct Node.{ Value Number Next (Or Node Nil) })\n(const n Node.{ Value 1 Next Nil })\n(const a n.Next.Value)\n", false},
		{"nullable link typo fires", "(struct Node.{ Value Number Next (Or Node Nil) })\n(const n Node.{ Value 1 Next Nil })\n(const a n.Next.Glorp)\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "unknown-member"); got != c.wantFire {
				t.Errorf("unknown-member fired=%v, want %v\n  src: %q", got, c.wantFire, c.src)
			}
		})
	}
}
