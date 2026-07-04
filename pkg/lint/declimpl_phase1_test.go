package lint

import (
	"strings"
	"testing"
)

// Decl/impl split (Doc/PlanV1/DeclImplSplit.md), Phase 1 — TOLERANT lint + editor.
// `(let name (params) = body)` is a function implementation; `(let Owner.name (params) = body)`
// a method implementation; `(= target value)` (2 args) stays reassignment. The old
// `(fun name (a b) body)` impl form keeps working during the tolerant window.

func TestDeclImplFunImpl(t *testing.T) {
	// A (= …) function impl binds the name; its body is reference-checked.
	d := AnalyzeFile("t.pho", []byte("(let add (a b) = (+ a b))\n(let x = (add 1 2))"))
	if hasDiag(d, "unresolved-identifier") || hasDiag(d, "parse-error") || hasDiag(d, "bad-form-arity") {
		t.Fatalf("clean (= add …) impl produced unexpected diags: %v", d)
	}
	// The body IS walked (dispatch-ordering guard): an undefined ref is flagged.
	d = AnalyzeFile("t.pho", []byte("(let add (a b) = (+ a nope))"))
	if !hasDiag(d, "unresolved-identifier") {
		t.Fatalf("expected unresolved-identifier for 'nope' in the impl body; got %v", d)
	}
}

func TestDeclImplMethodImpl(t *testing.T) {
	d := AnalyzeFile("t.pho", []byte("(struct P a)\n(let P.f (self) = self.a)"))
	if hasDiag(d, "unresolved-identifier") || hasDiag(d, "parse-error") {
		t.Fatalf("clean (= P.f …) method impl produced unexpected diags: %v", d)
	}
	d = AnalyzeFile("t.pho", []byte("(struct P a)\n(let P.f (self) = (+ self.a bogus))"))
	if !hasDiag(d, "unresolved-identifier") {
		t.Fatalf("expected unresolved-identifier for 'bogus' in the method impl body; got %v", d)
	}
}

func TestDeclImplReassignStillWorks(t *testing.T) {
	// The 2-arg (= x v) form is reassignment (NOT a define) — set-on-constant fires.
	d := AnalyzeFile("t.pho", []byte("(let c = 1)\n(= c 2)"))
	if !hasDiag(d, "set-on-constant") {
		t.Fatalf("2-arg (= c 2) should still flag set-on-constant; got %v", d)
	}
}

func TestDeclImplOldFunFormTolerated(t *testing.T) {
	// During the tolerant window the old (fun name (a b) body) impl still binds.
	d := AnalyzeFile("t.pho", []byte("(let greet (name) = name)\n(let x = (greet 'hi'))"))
	if hasDiag(d, "unresolved-identifier") {
		t.Fatalf("old (fun greet …) impl should still resolve; got %v", d)
	}
}

func TestDeclImplEditorSurfaces(t *testing.T) {
	src := []byte("(let add (a b) = (+ a b))\n(struct P x)\n(let P.f (self) = self.x)")
	// Outline: the fun + method impls appear (the method nests under its struct).
	var haveAdd, haveF bool
	for _, s := range DocumentSymbols("t.pho", src) {
		if s.Name == "add" && s.Kind == DefFun {
			haveAdd = true
		}
		for _, c := range s.Children {
			if c.Name == "f" && c.Kind == DefMethod {
				haveF = true
			}
		}
	}
	if !haveAdd || !haveF {
		t.Fatalf("outline missing = impls: add=%v P.f=%v", haveAdd, haveF)
	}
	// Params complete inside the impl body.
	if defs := CompletionsAt("t.pho", src, 1, 16); !containsName(defs, "a") || !containsName(defs, "b") {
		t.Fatalf("params a/b should complete inside the (= add …) body; got %v", defNames(defs))
	}
	// Hover on the impl name renders its header.
	if md, _, ok := HoverAt("t.pho", src, 1, 6); !ok || !strings.Contains(md, "(let add") {
		t.Fatalf("hover on the (let add …) impl name failed: ok=%v md=%q", ok, md)
	}
}

// The NEW property delegate form lints clean and its accessor bodies are
// reference-checked; the old flat form still works (tolerant).
func TestDeclImplPropertyLint(t *testing.T) {
	d := AnalyzeFile("t.pho", []byte("(struct Box x)\n(property Box.dbl (get (self) (* self.x 2)) (set (self v) (= self.x v)))"))
	for _, code := range []string{"unresolved-identifier", "bad-form-shape", "bad-form-arity", "invalid-self-usage"} {
		if hasDiag(d, code) {
			t.Fatalf("clean new-form property produced %s: %v", code, d)
		}
	}
	d = AnalyzeFile("t.pho", []byte("(struct Box x)\n(property Box.dbl (get (self) (+ self.x ghost)))"))
	if !hasDiag(d, "unresolved-identifier") {
		t.Fatalf("expected unresolved-identifier for 'ghost' in the getter body; got %v", d)
	}
}
