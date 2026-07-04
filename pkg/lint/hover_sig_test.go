package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hovering a function that has a decl/impl split shows its SIGNATURE (the
// declared interface, with parameter and result types), not the `(= …)`
// implementation body — from both a call site and the implementation's name.
func TestHoverPrefersFunSignature(t *testing.T) {
	src := "(fun add (Number (optional Number else 0)) Number)\n" +
		"(let add (a b) = (+ a b))\n" +
		"(add 5)\n"

	for _, probe := range []struct {
		what      string
		line, col int
	}{
		{"call site", 3, 2},
		{"impl name", 2, 6},
	} {
		md, _, ok := HoverAt("t.pho", []byte(src), probe.line, probe.col)
		if !ok {
			t.Fatalf("%s: hover returned nothing", probe.what)
		}
		if !strings.Contains(md, "(fun add (Number (optional Number else 0)) Number)") {
			t.Errorf("%s: hover should show the signature (with result type), got:\n%s", probe.what, md)
		}
		if strings.Contains(md, "(let add (a") {
			t.Errorf("%s: hover must NOT show the implementation, got:\n%s", probe.what, md)
		}
	}
}

// With no signature present, hover falls back to the implementation — there is
// nothing else to show.
func TestHoverImplWhenNoSignature(t *testing.T) {
	src := "(let solo (a) = a)\n(solo 1)\n"
	md, _, ok := HoverAt("t.pho", []byte(src), 2, 2)
	if !ok || !strings.Contains(md, "(let solo") {
		t.Errorf("impl-only hover should show the impl, got ok=%v:\n%s", ok, md)
	}
}

// Hovering a type renders its method list from each method's SIGNATURE — the
// raw `(name type…) → ret` shape with parameter TYPES and result type — not the
// implementation's argument names. Static methods show all params (the receiver
// type is implicit); instance methods drop the receiver.
func TestHoverTypeMethodListShowsSignatures(t *testing.T) {
	src := "(struct File #id)\n" +
		"(static method File.open! (String String (optional Atom else :overwrite)) File)\n" +
		"(let File.open! (path perm mode) = (dep/OsOpen path perm mode))\n" +
		"(method File.close! (Self) None)\n" +
		"(let File.close! (self) = (dep/OsClose self.#id))\n" +
		"(let f = File)\n"

	md, _, ok := HoverAt("t.pho", []byte(src), 6, 10) // hover `File`
	if !ok {
		t.Fatal("type hover returned nothing")
	}
	for _, want := range []string{
		"(open! String String (optional Atom else :overwrite)) → File", // static: every arg type + result
		"(close!) → None", // instance: receiver dropped
	} {
		if !strings.Contains(md, want) {
			t.Errorf("method list missing %q, got:\n%s", want, md)
		}
	}
	// It must render TYPES, not the implementation's argument names.
	if strings.Contains(md, "perm") || strings.Contains(md, "(or mode") {
		t.Errorf("method list leaked impl argument names, got:\n%s", md)
	}
}

// A binding whose value navigates a package (`dep/OsOpen`, a PSlash node) must
// render the whole slash chain in hover — the slash cutover added PSlash and
// pnodeText has to know how to print it, or the head comes out empty.
func TestHoverRendersSlashChain(t *testing.T) {
	src := "(let id = (dep/OsOpen path perm mode))\n(let x = id)\n"
	md, _, ok := HoverAt("t.pho", []byte(src), 2, 10) // the `id` reference
	if !ok || !strings.Contains(md, "(dep/OsOpen path perm mode)") {
		t.Errorf("hover should render the full slash chain, got ok=%v:\n%s", ok, md)
	}
}

// Hovering a method through member access (`p.shift`) shows the method's
// SIGNATURE, not its `(= …)` implementation. hoverMember reads the declaring
// file from disk, so the source is materialized to a temp file.
func TestHoverMethodMemberPrefersSignature(t *testing.T) {
	src := "(struct P x)\n" +
		"(method P.shift (Self Number) Number)\n" +
		"(let P.shift (self n) = (+ self.x n))\n" +
		"(let p = P.{ x = 1 })\n" +
		"(p.shift 2)\n"

	p := filepath.Join(t.TempDir(), "m.pho")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	md, _, ok := HoverAt(p, []byte(src), 5, 4) // `shift` in `(p.shift 2)`
	if !ok {
		t.Fatal("method-member hover returned nothing")
	}
	if !strings.Contains(md, "(method P.shift (Self Number) Number)") {
		t.Errorf("method-member hover should show the full signature, got:\n%s", md)
	}
	if strings.Contains(md, "(= P.shift") {
		t.Errorf("method-member hover must NOT show the implementation, got:\n%s", md)
	}
}
