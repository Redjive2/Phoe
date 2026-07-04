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
		{"self-ref resolves", "(struct Node.{ Number value Node next })\n(let n = Node.{ value = 1 next = none })\n(let a = n.next.value)\n", false},
		{"self-ref typo fires", "(struct Node.{ Number value Node next })\n(let n = Node.{ value = 1 next = none })\n(let a = n.next.bogus)\n", true},
		{"deep chain typo fires", "(struct Node.{ Number value Node next })\n(let n = Node.{ value = 1 next = none })\n(let a = n.next.next.nope)\n", true},
		{"nested resolves", "(struct B.{ Number x })\n(struct A.{ B inner })\n(let x = A.{ inner = B.{ x = 1 } })\n(let y = x.inner.x)\n", false},
		{"nested typo fires", "(struct B.{ Number x })\n(struct A.{ B inner })\n(let x = A.{ inner = B.{ x = 1 } })\n(let y = x.inner.zap)\n", true},
		{"nullable link resolves", "(struct Node.{ Number value (Or Node None) next })\n(let n = Node.{ value = 1 next = none })\n(let a = n.next.value)\n", false},
		{"nullable link typo fires", "(struct Node.{ Number value (Or Node None) next })\n(let n = Node.{ value = 1 next = none })\n(let a = n.next.glorp)\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDiag(AnalyzeFile("t.pho", []byte(c.src)), "unknown-member"); got != c.wantFire {
				t.Errorf("unknown-member fired=%v, want %v\n  src: %q", got, c.wantFire, c.src)
			}
		})
	}
}
