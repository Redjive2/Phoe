package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// analyze lints `src` as a standalone .pho program in a temp dir, so
// no sibling/package machinery interferes.
func analyze(t *testing.T, src string) []Diagnostic {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.pho")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return AnalyzeFile(path, []byte(src))
}

const pointPrelude = `(struct Point X y)
(method Point.Shift (self d) (+ self.X d))
(method Point.tweak (self d) (+ self.y d))
(var p Point.{ X 10 y 20 })
`

// ----------------------------------------------------------------------
// Instance member checks
// ----------------------------------------------------------------------

func TestUnknownMemberOnInstance(t *testing.T) {
	diags := analyze(t, pointPrelude+`(var q p.Nope)
`)
	if !hasDiagWithName(diags, "unknown-member", "Nope") {
		t.Fatalf("expected unknown-member for p.Nope, got %#v", diags)
	}
}

func TestKnownMembersAreSilent(t *testing.T) {
	diags := analyze(t, pointPrelude+`(var a p.X)
(var b (p.Shift 1))
`)
	for _, code := range []string{"unknown-member", "private-member-access", "invalid-member-access"} {
		if hasDiag(diags, code) {
			t.Fatalf("expected no %s on valid members, got %#v", code, diags)
		}
	}
}

func TestPrivateMemberOutsideMethod(t *testing.T) {
	diags := analyze(t, pointPrelude+`(var a p.y)
(var b (p.tweak 1))
`)
	if !hasDiagWithName(diags, "private-member-access", "'y'") {
		t.Fatalf("expected private-member-access for p.y, got %#v", diags)
	}
	if !hasDiagWithName(diags, "private-member-access", "'tweak'") {
		t.Fatalf("expected private-member-access for p.tweak, got %#v", diags)
	}
}

func TestSelfAccessIsPrivileged(t *testing.T) {
	diags := analyze(t, `(struct Point X y)
(method Point.Sum (self) (+ self.X self.y))
(method Point.Bad (self) self.zzz)
`)
	if hasDiag(diags, "private-member-access") {
		t.Fatalf("self access must not fire privacy, got %#v", diags)
	}
	if !hasDiagWithName(diags, "unknown-member", "zzz") {
		t.Fatalf("expected unknown-member for self.zzz, got %#v", diags)
	}
}

func TestSelfAliasKeepsPrivilege(t *testing.T) {
	diags := analyze(t, `(struct Point X y)
(method Point.Roundabout (self) (identity do
  (var me self)
  me.y))
`)
	if hasDiag(diags, "private-member-access") {
		t.Fatalf("aliased self must keep privilege, got %#v", diags)
	}
}

func TestUnknownMemberViaSiblingFile(t *testing.T) {
	dir := t.TempDir()
	sibling := filepath.Join(dir, "types.phl")
	if err := os.WriteFile(sibling, []byte(`(struct Box Content)
(method Box.Open (self) self.Content)
`), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "use.phl")
	src := []byte(`(fun Use () (identity do
  (var b Box.{ Content 1 })
  (var x b.Content)
  (var y (b.Open))
  (var z b.Missing)))
`)
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}
	diags := AnalyzeFile(target, src)
	if !hasDiagWithName(diags, "unknown-member", "Missing") {
		t.Fatalf("expected unknown-member for b.Missing via sibling struct, got %#v", diags)
	}
	if hasDiagWithName(diags, "unknown-member", "Content") || hasDiagWithName(diags, "unknown-member", "Open") {
		t.Fatalf("expected no unknown-member on real members, got %#v", diags)
	}
}

// ----------------------------------------------------------------------
// Shape tracking through assignment
// ----------------------------------------------------------------------

func TestReassignmentRetargetsShape(t *testing.T) {
	diags := analyze(t, `(struct Point X)
(struct Line Length)
(var v Point.{ X 1 })
(= v Line.{ Length 2 })
(var a v.Length)
(var b v.X)
`)
	if !hasDiagWithName(diags, "unknown-member", "'X'") {
		t.Fatalf("expected unknown-member for v.X after retarget to Line, got %#v", diags)
	}
	if hasDiagWithName(diags, "unknown-member", "Length") {
		t.Fatalf("v.Length must be valid after retarget, got %#v", diags)
	}
}

func TestBranchReassignmentInvalidatesShape(t *testing.T) {
	diags := analyze(t, `(struct Point X)
(var v Point.{ X 1 })
(if (== 1 1) then (= v 5))
(var a v.Whatever)
`)
	if hasDiag(diags, "unknown-member") || hasDiag(diags, "invalid-member-access") {
		t.Fatalf("branch reassignment must invalidate the shape, got %#v", diags)
	}
}

func TestFunctionBodyAssignInvalidatesTopLevelShape(t *testing.T) {
	diags := analyze(t, `(struct Point X)
(var v Point.{ X 1 })
(fun clobber () (= v 5))
(var a v.Anything)
`)
	if hasDiag(diags, "unknown-member") {
		t.Fatalf("cross-frame assignment must invalidate the shape, got %#v", diags)
	}
}

// ----------------------------------------------------------------------
// Dict / array / scalar access
// ----------------------------------------------------------------------

func TestDictBareAccessNeedsBrackets(t *testing.T) {
	diags := analyze(t, `(var d {'a' 1 'b' 2})
(var x d.zzz)
`)
	// Bare dot on a dict is a member lookup; `zzz` isn't a member of Map, so
	// it's flagged (dict KEY lookup must use brackets, d.[zzz]).
	if !hasDiagWithName(diags, "unknown-member", "zzz") {
		t.Fatalf("expected unknown-member for d.zzz, got %#v", diags)
	}
}

func TestDictBracketUnboundKeyIsUnresolved(t *testing.T) {
	diags := analyze(t, `(var d {'a' 1})
(var x d.[zzz])
`)
	// The key expression inside the bracket is an ordinary expression, so
	// an unbound name is the standard unresolved-identifier, not a
	// member-access error.
	if !hasDiagWithName(diags, "unresolved-identifier", "zzz") {
		t.Fatalf("expected unresolved-identifier for the unbound key expression, got %#v", diags)
	}
}

func TestDictBracketBoundKeyIsSilent(t *testing.T) {
	diags := analyze(t, `(var d {'a' 1})
(var k 'a')
(var x d.[k])
`)
	if hasDiag(diags, "unknown-key") || hasDiag(diags, "unresolved-identifier") {
		t.Fatalf("a bound computed key must be silent, got %#v", diags)
	}
}

func TestDictUnknownStaticKeyWarns(t *testing.T) {
	diags := analyze(t, `(var d {'a' 1 'b' 2})
(var x d.['missing'])
`)
	found := false
	for _, d := range diags {
		if d.Code == "unknown-key" && d.Severity == SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unknown-key warning for d.[\"missing\"], got %#v", diags)
	}
}

func TestDictKnownStaticKeySilent(t *testing.T) {
	diags := analyze(t, `(var d {'a' 1 'b' 2})
(var x d.['a'])
(var y d.['b'])
`)
	if hasDiag(diags, "unknown-key") {
		t.Fatalf("known static keys must be silent, got %#v", diags)
	}
}

func TestDictWriteAddsKey(t *testing.T) {
	diags := analyze(t, `(var d {'a' 1})
(= d.['fresh'] 2)
(var x d.['fresh'])
`)
	if hasDiag(diags, "unknown-key") {
		t.Fatalf("a written key must be readable without warning, got %#v", diags)
	}
}

func TestComputedDictKeysDisableKeyChecks(t *testing.T) {
	diags := analyze(t, `(var k 'dyn')
(var d {k 1})
(var x d.['anything'])
`)
	if hasDiag(diags, "unknown-key") {
		t.Fatalf("computed keys must disable key tracking, got %#v", diags)
	}
}

func TestArrayBareAccessNeedsBrackets(t *testing.T) {
	diags := analyze(t, `(var arr [1 2 3])
(var x arr.qqq)
(var z arr.0)
`)
	// An identifier after a bare dot is a member lookup that misses on a List;
	// a numeric literal is steered to bracket indexing.
	if !hasDiagWithName(diags, "unknown-member", "qqq") {
		t.Fatalf("expected unknown-member for arr.qqq, got %#v", diags)
	}
	if !hasDiagWithName(diags, "invalid-member-access", "0") {
		t.Fatalf("expected invalid-member-access steering arr.0 to arr.[0], got %#v", diags)
	}
}

func TestArrayBracketAccessIsChecked(t *testing.T) {
	diags := analyze(t, `(var arr [1 2 3])
(var i 0)
(var y arr.[i])
(var z arr.[0])
(var w arr.[1 : 2])
`)
	if hasDiag(diags, "invalid-member-access") {
		t.Fatalf("bracket indexing/slicing must be silent, got %#v", diags)
	}
	// An unbound index expression inside the bracket is still caught.
	unbound := analyze(t, `(var arr [1 2 3])
(var x arr.[qqq])
`)
	if !hasDiagWithName(unbound, "unresolved-identifier", "qqq") {
		t.Fatalf("an unbound index expression must be flagged, got %#v", unbound)
	}
}

// (TestKeyofResultIsArray was removed: the `keyof` builtin is gone — the keys
// of a map are reached via the `.Keys` member now — and `.Keys` does not yet
// shape-infer as a List, so there's no equivalent member-access check.)

// TestIncompleteIfDoesNotPanicTheWalker is a regression for a crash that
// broke hover/completion/definition for a whole file: an incomplete
// `(if ...)` (fewer than 3 children — routine mid-edit) made the walker's
// unguarded Children[2:] panic with "slice bounds out of range [2:1]".
// Because the walker runs over the entire buffer, that single panic
// aborted every navigation request for the file. These calls must all
// return normally (a panic fails the test).
func TestIncompleteIfDoesNotPanicTheWalker(t *testing.T) {
	for _, src := range []string{
		"(if)",
		"(if x)",
		"(fun f () (if))",
		"(fun g (n) (identity do (if (< n 1)) (+ n 1)))",
	} {
		_ = analyze(t, src)
		_ = CompletionsAt("main.pho", []byte(src), 1, 2)
		_, _, _ = HoverAt("main.pho", []byte(src), 1, 2)
		_, _ = DefinitionAt("main.pho", []byte(src), 1, 2)
	}
}

func TestScalarAccess(t *testing.T) {
	diags := analyze(t, `(var n 5)
(var x n.foo)
(var f 1.5)
(var b True)
(var y b.thing)
`)
	if !hasDiagWithName(diags, "unknown-member", "foo") {
		t.Fatalf("expected unknown-member for n.foo, got %#v", diags)
	}
	if !hasDiagWithName(diags, "unknown-member", "thing") {
		t.Fatalf("expected unknown-member for b.thing, got %#v", diags)
	}
	// 1.5 — the decimal hack — must stay silent.
	for _, d := range diags {
		if d.Code == "invalid-member-access" && d.Span.StartLine == 3 {
			t.Fatalf("the decimal hack must not be flagged, got %#v", d)
		}
	}
}

// ----------------------------------------------------------------------
// Instance writes
// ----------------------------------------------------------------------

func TestWriteUnknownField(t *testing.T) {
	diags := analyze(t, pointPrelude+`(= p.Nope 1)
`)
	if !hasDiagWithName(diags, "unknown-member", "Nope") {
		t.Fatalf("expected unknown-member for write to p.Nope, got %#v", diags)
	}
}

func TestWriteToMethod(t *testing.T) {
	diags := analyze(t, pointPrelude+`(= p.Shift 1)
`)
	if !hasDiagWithName(diags, "unknown-member", "Shift") {
		t.Fatalf("expected unknown-member for write to method p.Shift, got %#v", diags)
	}
}

func TestWritePrivateFieldOutside(t *testing.T) {
	diags := analyze(t, pointPrelude+`(= p.y 1)
`)
	if !hasDiagWithName(diags, "private-member-access", "'y'") {
		t.Fatalf("expected private-member-access for write to p.y, got %#v", diags)
	}
}

func TestWriteOwnFieldInMethod(t *testing.T) {
	diags := analyze(t, `(struct Point X y)
(method Point.Reset (self) (= self.y 0))
`)
	if hasDiag(diags, "private-member-access") || hasDiag(diags, "unknown-member") {
		t.Fatalf("self field writes must be fine, got %#v", diags)
	}
}

// ----------------------------------------------------------------------
// Unknown shapes stay silent (no false positives)
// ----------------------------------------------------------------------

func TestUnknownShapesStaySilent(t *testing.T) {
	diags := analyze(t, `(struct Point X)
(fun mk () Point.{ X 1 })
(fun use (p) (identity do
  (var a p.Whatever)
  (var b (mk))
  (var c b.AlsoWhatever)
  (var d b.AlsoWhatever.Chained)))
`)
	for _, code := range []string{"unknown-member", "invalid-member-access", "unknown-key", "private-member-access"} {
		if hasDiag(diags, code) {
			t.Fatalf("params and call results are Unknown — expected no %s, got %#v", code, diags)
		}
	}
}
