package lint

import (
	"testing"

	"pho/pkg/annot"
	"pho/pkg/core"
	"pho/pkg/syntax"
)

// A method's `--@ (~sig Recv (P…) R…)` is harvested onto its owner's
// member surface, with the receiver dropped from the call-argument signature.
// This is the OM half of the Sig surface (Coordination §3) that the gradual
// checker reads to type a method call.
func TestMethodSigHarvest(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	src := "(struct Reader #buffer)\n" +
		"(method Reader.seek (Reader Number) Boolean)\n" +
		"(let Reader.seek (self n) = true)\n"

	tokens, _ := syntax.LexPos(src)
	tree, _ := syntax.ParsePos(tokens)
	tree = syntax.NormalizeDo(tree)

	w := newWalker("t.pho")
	scope := newScope(PackageScope("t.pho"))
	w.fileScope = scope
	w.collect(scope, tree)
	w.harvestMethodSigs(scope, tree)

	sig := w.methodSigFor(scope, "Reader", "seek")
	if sig == nil {
		t.Fatal("expected a harvested signature for Reader.seek")
	}
	// The receiver (Reader) is not an argument; params are just [Number].
	if len(sig.Params) != 1 || sig.Params[0] != core.TypeNumber {
		t.Fatalf("params = %v, want [Number]", sig.Params)
	}
	if sig.Result != core.TypeBoolean {
		t.Fatalf("result = %v, want Boolean", sig.Result)
	}

	// An un-annotated method has no harvested signature.
	if got := w.methodSigFor(scope, "Reader", "Nope"); got != nil {
		t.Fatalf("expected no signature for an undeclared method, got %v", got)
	}
}
