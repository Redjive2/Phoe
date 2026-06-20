package builtins

import (
	"testing"

	"pho/pkg/core"
)

// keyof over an array yields its integer indices, in order.
func TestKeyofArray(t *testing.T) {
	got := evalProgram(t, `(keyof [10 20 30])`)
	arr, ok := got.Val.(*[]core.Value)
	if !ok || len(*arr) != 3 {
		t.Fatalf("expected a 3-element array, got %#v", got.Val)
	}
	for i, v := range *arr {
		if v.Kind != core.KindNum || v.Val != float64(i) {
			t.Fatalf("index %d = %#v, want num %d", i, v, i)
		}
	}
}

// keyof over a dict yields its keys without panicking — the dict case
// asserted the value as a non-pointer map when it is stored as *map.
func TestKeyofDict(t *testing.T) {
	got := evalProgram(t, `(keyof { 'a 1 'b 2 })`)
	arr, ok := got.Val.(*[]core.Value)
	if !ok || len(*arr) != 2 {
		t.Fatalf("expected 2 keys, got %#v", got.Val)
	}
	seen := map[string]bool{}
	for _, v := range *arr {
		s, _ := v.Val.(string)
		seen[s] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("expected keys a and b, got %#v", *arr)
	}
}
