package goop

import (
	"sort"
	"testing"

	"pho/pkg/core"
)

func TestPrimSize(t *testing.T) {
	s := &stdDependencies{}

	list := core.TvSlice([]core.Value{core.TvNum(1), core.TvNum(2), core.TvNum(3)})
	if got := s.Size(list); got != 3 {
		t.Errorf("Size(list of 3) = %v, want 3", got)
	}

	// Strings measure by rune, not byte: "café" is 4 runes / 5 bytes.
	if got := s.Size(core.TvStr("café")); got != 4 {
		t.Errorf("Size(\"café\") = %v, want 4 (runes)", got)
	}

	dict := core.TvDict(map[core.Value]core.Value{
		core.TvStr("a"): core.TvNum(1),
		core.TvStr("b"): core.TvNum(2),
	})
	if got := s.Size(dict); got != 2 {
		t.Errorf("Size(dict of 2) = %v, want 2", got)
	}

	if got := s.Size(core.TvSlice(nil)); got != 0 {
		t.Errorf("Size(empty list) = %v, want 0", got)
	}
	if got := s.Size(core.TvStr("")); got != 0 {
		t.Errorf("Size(empty string) = %v, want 0", got)
	}
}

func TestPrimKeysList(t *testing.T) {
	s := &stdDependencies{}
	list := core.TvSlice([]core.Value{core.TvStr("x"), core.TvStr("y"), core.TvStr("z")})

	keys := s.Keys(list)
	if len(keys) != 3 {
		t.Fatalf("Keys(list of 3) returned %d keys, want 3", len(keys))
	}
	// A list's keys are its indices 0..Size-1, in order.
	for i, k := range keys {
		if k.Kind != core.KindNum {
			t.Errorf("Keys[%d].Kind = %q, want %q", i, k.Kind, core.KindNum)
			continue
		}
		if got := k.Val.(float64); got != float64(i) {
			t.Errorf("Keys[%d] = %v, want %d", i, got, i)
		}
	}
}

func TestPrimKeysDict(t *testing.T) {
	s := &stdDependencies{}
	dict := core.TvDict(map[core.Value]core.Value{
		core.TvStr("a"): core.TvNum(1),
		core.TvStr("b"): core.TvNum(2),
	})

	keys := s.Keys(dict)
	got := make([]string, 0, len(keys))
	for _, k := range keys {
		if k.Kind != core.KindStr {
			t.Errorf("dict key Kind = %q, want %q", k.Kind, core.KindStr)
			continue
		}
		got = append(got, k.Val.(string))
	}
	// Dict iteration order is unspecified; compare as a set.
	sort.Strings(got)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Keys(dict) = %v, want [a b]", got)
	}
}
