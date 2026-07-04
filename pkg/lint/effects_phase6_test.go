package lint

import (
	"strings"
	"testing"
)

// Hover on a fun/method surfaces its inferred effect set. This is informational
// and does not depend on the EffectCheck gate.
func TestHoverShowsEffects(t *testing.T) {
	src := "(let tug! () = (dep.OsChdir 'x'))\n(let calc (a b) = (+ a b))\n"

	io, _, ok := HoverAt("t.pho", []byte(src), 1, 7) // on tug!
	if !ok {
		t.Fatal("expected hover for tug!")
	}
	if !strings.Contains(io, "**effects**") || !strings.Contains(io, "io") {
		t.Fatalf("tug! hover should report io effect, got:\n%s", io)
	}

	pure, _, ok := HoverAt("t.pho", []byte(src), 2, 7) // on calc
	if !ok {
		t.Fatal("expected hover for calc")
	}
	if !strings.Contains(pure, "**effects**: pure") {
		t.Fatalf("calc hover should report pure, got:\n%s", pure)
	}
}

// A mutating method hover names the mutates-self effect.
func TestHoverShowsMutatesSelf(t *testing.T) {
	src := "(struct Counter n)\n(let Counter.bump! ((var self) by) = (= self.n (+ self.n by)))\n"
	md, _, ok := HoverAt("t.pho", []byte(src), 2, 14) // on bump! (in `(= Counter.bump!`)
	if !ok {
		t.Fatal("expected hover for bump!")
	}
	if !strings.Contains(md, "mutates-self") {
		t.Fatalf("bump! hover should report mutates-self, got:\n%s", md)
	}
}
