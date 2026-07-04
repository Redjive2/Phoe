package lint

import (
	"testing"

	"pho/pkg/annot"
)

// A call to a method on an IMPORTED struct instance type-checks its arguments
// against the method's inline signature, harvested from the imported package by
// PackageStructs and read at the call site via methodSigForShape. Previously
// imported method sigs were unchecked (methodSigFor only saw local structs).
func TestImportedMethodSig(t *testing.T) {
	if err := annot.InitDefault("../../script/std/annot"); err != nil {
		t.Skipf("annotation macros not loadable: %v", err)
	}
	defer annot.SetDefault(annot.New(nil))

	geo := "(struct Point.{ Number x Number y })\n" +
		"(method Point.scale (Self Number) Point)\n" + // inline signature
		"(let Point.scale (self k) = self)\n" // impl

	// A String argument where the imported method expects Number must fire.
	path, src := writeApp(t, geo, "(let p = geo.Point.{ x = 1 y = 2 })\n(p.scale 'nope')")
	if !hasDiag(AnalyzeFile(path, src), "type-mismatch") {
		t.Errorf("a String arg to an imported method expecting Number should fire")
	}

	// A Number argument satisfies the signature — no diagnostic.
	path, src = writeApp(t, geo, "(let p = geo.Point.{ x = 1 y = 2 })\n(p.scale 3)")
	if hasDiag(AnalyzeFile(path, src), "type-mismatch") {
		t.Errorf("a Number arg to an imported method expecting Number should be clean")
	}

	// An imported struct WITHOUT an inline method sig stays gradual: no false
	// positive even on an obviously-wrong argument.
	geoUntyped := "(struct Box.{ Number n })\n(let Box.bump (self k) = self)\n"
	path, src = writeApp(t, geoUntyped, "(let b = geo.Box.{ n = 1 })\n(b.bump 'anything')")
	if hasDiag(AnalyzeFile(path, src), "type-mismatch") {
		t.Errorf("an un-annotated imported method must stay gradual (no false positive)")
	}
}
