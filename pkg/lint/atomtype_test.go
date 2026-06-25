package lint

import (
	"testing"

	"pho/pkg/ast"
	"pho/pkg/core"
)

// The checker's type resolvers understand LITERAL singletons — atoms (`:ok`),
// numbers (`5`), strings (`"GET"`), and bools (`True`) — in a written type
// position, in a harvested annotation value, and as an inferred literal type.
// Each resolves to the precise singleton (not the flat primitive), which is
// what makes an annotated enum like `--@ (~type (Or :ok :error))` or
// `(Or "GET" "POST")` check exactly. The harvest delivers every scalar as its
// source TEXT (an atom as ":ok", a string as the quoted `"GET"`), so the tests
// feed that representation. Tested directly on the resolvers — no dependency on
// the annotation/macro pipeline.
func TestLiteralSingletonResolution(t *testing.T) {
	okT := core.AtomSingleton("ok")
	enum := okT.Or(core.AtomSingleton("error"))

	// resolveTypeNode (AST leaves): each literal form is its singleton.
	if got := resolveTypeNode(&ast.PLeaf{Value: ":ok"}, nil); got != okT {
		t.Errorf("resolveTypeNode(:ok) = %s, want :ok", got.Name())
	}
	if got := resolveTypeNode(&ast.PLeaf{Value: "5"}, nil); got != core.NumSingleton(5) {
		t.Errorf("resolveTypeNode(5) = %s, want 5", got.Name())
	}
	if got := resolveTypeNode(&ast.PLeaf{Value: `'GET'`}, nil); got != core.StrSingleton("GET") {
		t.Errorf(`resolveTypeNode("GET") = %s, want "GET"`, got.Name())
	}
	if got := resolveTypeNode(&ast.PLeaf{Value: "True"}, nil); got != core.BoolSingleton(true) {
		t.Errorf("resolveTypeNode(True) = %s, want True", got.Name())
	}
	// A bare name is still a type name, not a literal.
	if got := resolveTypeNode(&ast.PLeaf{Value: "Number"}, nil); got != core.TypeNumber {
		t.Errorf("resolveTypeNode(Number) = %s, want Number", got.Name())
	}

	// (Or :ok :error) written as code resolves to the tagged union.
	orNode := &ast.PBranch{Open: "(", Close: ")", Children: []ast.PNode{
		&ast.PLeaf{Value: "Or"}, &ast.PLeaf{Value: ":ok"}, &ast.PLeaf{Value: ":error"},
	}}
	if got := resolveTypeNode(orNode, nil); got != enum {
		t.Errorf("resolveTypeNode((Or :ok :error)) = %s, want %s", got.Name(), enum.Name())
	}

	// resolveAnnotType: scalars arrive as source-text strings.
	if got := resolveAnnotType(core.TvStr(":ok"), nil); got != okT {
		t.Errorf("resolveAnnotType(:ok) = %s, want :ok", got.Name())
	}
	if got := resolveAnnotType(core.TvStr("5"), nil); got != core.NumSingleton(5) {
		t.Errorf("resolveAnnotType(5) = %s, want 5", got.Name())
	}
	if got := resolveAnnotType(core.TvStr(`'GET'`), nil); got != core.StrSingleton("GET") {
		t.Errorf(`resolveAnnotType("GET") = %s, want "GET"`, got.Name())
	}
	if got := resolveAnnotType(core.TvStr("Number"), nil); got != core.TypeNumber {
		t.Errorf("resolveAnnotType(Number) = %s, want Number", got.Name())
	}
	// (Or "GET" "POST") harvested as a list of source-text strings.
	methods := core.StrSingleton("GET").Or(core.StrSingleton("POST"))
	orList := core.TvSlice([]core.Tval{core.TvStr("Or"), core.TvStr(`'GET'`), core.TvStr(`'POST'`)})
	if got := resolveAnnotType(orList, nil); got != methods {
		t.Errorf(`resolveAnnotType([Or "GET" "POST"]) = %s, want %s`, got.Name(), methods.Name())
	}

	// inferType: a literal infers its precise singleton, not the flat primitive.
	if got := inferType(&ast.PLeaf{Value: ":ok"}, nil, flowEnv{}); got != okT {
		t.Errorf("inferType(:ok) = %s, want :ok", got.Name())
	}
	if got := inferType(&ast.PLeaf{Value: "200"}, nil, flowEnv{}); got != core.NumSingleton(200) {
		t.Errorf("inferType(200) = %s, want 200", got.Name())
	}
	if got := inferType(&ast.PLeaf{Value: `'GET'`}, nil, flowEnv{}); got != core.StrSingleton("GET") {
		t.Errorf(`inferType("GET") = %s, want "GET"`, got.Name())
	}
	if got := inferType(&ast.PLeaf{Value: ":ok"}, nil, flowEnv{}); got == core.TypeAtom {
		t.Errorf("inferType(:ok) must be the singleton, not bare Atom")
	}
}
