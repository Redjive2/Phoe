package builtins

import "testing"

// Pattern matching in `let` value bindings (assignment destructuring): a `let`
// target may be a name, a typed `(Type name)`, or a destructuring pattern
// (`[a b …]`, nested). `(var …)` marks a binder reassignable; a plain binder is
// const. The pattern helpers (var / (Type name) / (name) capture) are the same
// ones the clause and select matchers use.

func TestLetDestructureConsts(t *testing.T) {
	// Three binders from a list, all const by default.
	evalNum(t, "(let [a b c] = [1 2 3])\n(+ a (+ b c))", 6)
}

func TestLetDestructureVarElement(t *testing.T) {
	// A per-element (var x) is reassignable; a plain sibling stays bound.
	evalNum(t, "(let [(var x) y] = [10 20])\n(= x 99)\n(+ x y)", 119)
}

func TestLetDestructureTopLevelVar(t *testing.T) {
	// A leading `var` makes every binder in the pattern reassignable.
	evalNum(t, "(let var [p q] = [3 4])\n(= p 5)\n(+ p q)", 9)
}

func TestLetTypedBindingGrouped(t *testing.T) {
	// The grouped `(Type name)` typed binding — the type is erased, the name
	// binds. (The ungrouped `Type name` form is retired; see below.)
	evalNum(t, "(let (Number n) = 42)\nn", 42)
	// var + type in one binder.
	evalNum(t, "(let [(var Number m)] = [7])\n(= m 8)\nm", 8)
}

func TestLetDestructureNested(t *testing.T) {
	evalNum(t, "(let [a [b c]] = [1 [2 3]])\n(+ a (+ b c))", 6)
}

// The bare-name, multi-binding, and `let var` value forms keep working — the
// destructuring rework must not regress the scalar cases.
func TestLetScalarFormsUnaffected(t *testing.T) {
	evalNum(t, "(let x = 5)\nx", 5)
	evalNum(t, "(let a = 1 b = 2)\n(+ a b)", 3)
	evalNum(t, "(let var y = 1)\n(= y 7)\ny", 7)
}

// The `()` capture operator on a struct-field key binds the whole field value
// AND destructures it via the field's pattern. `(var field)` captures mutably.
func TestLetStructFieldCapture(t *testing.T) {
	bag := "(struct Bag items)\n"
	// (items) captures the list AND [a b c] destructures it: a,b,c bound, and
	// `items` bound to the whole [10 20 30].
	evalNum(t, bag+"(let Bag.{ (items) = [a b c] } = Bag.{ items = [10 20 30] })\n(+ a (+ b c))", 60)
	evalNum(t, bag+"(let Bag.{ (items) = [a b c] } = Bag.{ items = [10 20 30] })\nitems.[1]", 20)
	// (var items) captures reassignably.
	evalNum(t, bag+"(let Bag.{ (var items) = [a] } = Bag.{ items = [5] })\n(= items 99)\nitems", 99)
	// Plain (non-capture) struct destructure still works — no `items` binding.
	evalNum(t, bag+"(let Bag.{ items = [a b] } = Bag.{ items = [7 8] })\n(+ a b)", 15)
}

func TestLetDestructureErrors(t *testing.T) {
	// A plain destructured binder is const: reassigning it is an error.
	if _, codes := evalProgramDiag(t, "(let [a b] = [1 2])\n(= a 9)"); !hasCode(codes, "const-assign") {
		t.Errorf("reassigning a const destructured binder: want const-assign, got %v", codes)
	}
	// Length mismatch: the value does not match the pattern.
	if _, codes := evalProgramDiag(t, "(let [a b] = [1 2 3])"); !hasCode(codes, "type-mismatch") {
		t.Errorf("length mismatch: want type-mismatch, got %v", codes)
	}
	// A non-list value against a list pattern fails to match.
	if _, codes := evalProgramDiag(t, "(let [a b] = 5)"); !hasCode(codes, "type-mismatch") {
		t.Errorf("non-list vs list pattern: want type-mismatch, got %v", codes)
	}
	// The retired ungrouped `Type name = value` form is a hard error.
	if _, codes := evalProgramDiag(t, "(let Number x = 5)"); len(codes) == 0 {
		t.Errorf("ungrouped 'Type name = value' should be rejected, got no diagnostics")
	}
}

// The capture operator (and var/type helpers) work in select patterns too, not
// just `let` — they share the pattern engine. (Uses select, not a method clause,
// so the test doesn't depend on the fun/method signature syntax, which a sibling
// is mid-migrating.)
func TestCaptureEverywhere(t *testing.T) {
	// A struct-field capture inside a select case: `items` bound to the whole
	// list AND [a b] destructures it.
	src := "(struct Bag items)\n" +
		"(let bg = Bag.{ items = [3 4] })\n" +
		"(select bg case Bag.{ (items) = [a b] } -> (+ (+ a b) items.size))"
	evalNum(t, src, 9) // 3+4 + size 2

	// A nested (var x) binder in a list case is reassignable in the arm.
	sel := "(let pair = [1 2])\n" +
		"(select pair case [(var x) y] -> (do (= x 10) (+ x y)))"
	evalNum(t, sel, 12)
}
