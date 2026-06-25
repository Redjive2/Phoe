package main

import (
	"fmt"
	"regexp"

	"pho/pkg/ast"
	"pho/pkg/syntax"
)

// The occurrence rewriter: it applies the casing/visibility scheme to every
// identifier in a source, given the package-wide rename map (top-level names)
// and type set built from all files. General identifiers use the leaf rule;
// member accesses and struct-field declarations use the member rule, where a
// name carries its own visibility (Doc/PlanV1/Syntax.md).

var recaseIdentRe = regexp.MustCompile(`^#?[A-Za-z][A-Za-z0-9_]*\??$`)

func isIdentLeaf(s string) bool { return recaseIdentRe.MatchString(s) }

// literalNames are handled by the mechanical Transform (Nil→none, etc.); Recase
// leaves them alone so the two passes never fight over the same token.
var literalNames = map[string]bool{"Nil": true, "True": true, "False": true, "Self": true}

// recaseLeaf decides the new spelling of a general identifier: a top-level name,
// a reference, a param/local, or a bare type reference. The package-wide map
// carries privacy (`#`) for top-level names; an unmapped name is a value
// (param/local) → snake_case, or a known type → Title_Snake.
func recaseLeaf(name string, renames map[string]string, types map[string]bool) string {
	if literalNames[name] {
		return name
	}
	if nn, ok := renames[name]; ok {
		return nn
	}
	if types[name] {
		return toTitleSnake(name)
	}
	return toSnakeCase(name)
}

// recaseMember decides the new spelling of a struct/module member (a field,
// method, or `pkg.X` export). A member encodes its own visibility under the old
// rule — Capitalized = public → snake_case; lowercase = private → `#` +
// snake_case — except a member naming an exported TYPE stays Title_Snake.
func recaseMember(name string, types map[string]bool) string {
	if literalNames[name] {
		return name
	}
	if types[name] {
		return toTitleSnake(name)
	}
	if isCapitalized(name) {
		return toSnakeCase(name)
	}
	return "#" + toSnakeCase(name)
}

// recaseCtx bundles the package-wide maps a recase pass consults.
type recaseCtx struct {
	renames   map[string]string
	types     map[string]bool
	goimports map[string]bool // Go-module aliases whose members must NOT be recased
	bound     map[string]bool // names bound LOCALLY in the current scope (params,
	// body lets) — these are values (snake_case, never `#`) even if they collide
	// with a global private name. Replaced per fun/method scope.
}

// Recase rewrites identifier spellings in src per the new scheme. It refuses on
// lex/parse errors so a malformed file is never corrupted.
func Recase(src string, renames map[string]string, types, goimports map[string]bool) (string, int, error) {
	toks, lexErrs := syntax.LexPos(src)
	if len(lexErrs) > 0 {
		return src, 0, fmt.Errorf("refusing to recase: %d lex error(s)", len(lexErrs))
	}
	tree, parseErrs := syntax.ParsePos(toks)
	if len(parseErrs) > 0 {
		return src, 0, fmt.Errorf("refusing to recase: %d parse error(s)", len(parseErrs))
	}
	ctx := &recaseCtx{renames, types, goimports, map[string]bool{}}
	var edits []edit
	for _, form := range tree {
		recaseWalk(src, form, ctx, &edits)
	}
	if len(edits) == 0 {
		return src, 0, nil
	}
	return applyEdits(src, edits), len(edits), nil
}

func leafEdit(src string, lf *ast.PLeaf, newVal string, edits *[]edit) {
	if newVal == lf.Value {
		return
	}
	start := offsetOf(src, lf.Span.StartLine, lf.Span.StartCol)
	*edits = append(*edits, edit{start, start + len(lf.Value), newVal})
}

func isStrLit(n ast.PNode) (*ast.PLeaf, bool) {
	if lf, ok := n.(*ast.PLeaf); ok && len(lf.Value) >= 2 && lf.Value[0] == '\'' {
		return lf, true
	}
	return nil, false
}

func recaseWalk(src string, n ast.PNode, ctx *recaseCtx, edits *[]edit) {
	switch node := n.(type) {
	case *ast.PLeaf:
		if isIdentLeaf(node.Value) {
			leafEdit(src, node, recaseName(node.Value, ctx), edits)
		}
	case *ast.PBranch:
		if node.Open == "(" && len(node.Children) >= 1 {
			if head, ok := node.Children[0].(*ast.PLeaf); ok {
				if head.Value == "struct" {
					recaseStruct(src, node, ctx, edits)
					return
				}
				// fun/method open a new scope: their params and body-local
				// bindings are values, even if they shadow a global private name.
				if head.Value == "fun" || head.Value == "method" {
					recaseFunLike(src, node, ctx, edits)
					return
				}
				// A construction `(Type 'field' val …)` (the `.{}` sugar quotes
				// the keys). A USER struct type with a string-literal first arg
				// marks it; built-in connectives (`(Or 'GET' …)`) are excluded.
				if len(node.Children) >= 2 && ctx.types[head.Value] && !builtinTypes[head.Value] {
					if _, ok := isStrLit(node.Children[1]); ok {
						recaseConstruction(src, node, ctx, edits)
						return
					}
				}
			}
		}
		for _, c := range node.Children {
			recaseWalk(src, c, ctx, edits)
		}
	case *ast.PDot:
		recaseWalk(src, node.LHS, ctx, edits)
		// A member of a Go-module alias (`dep.PctlSpawn`) names a fixed Go
		// export — leave it untouched.
		if lhs, ok := node.LHS.(*ast.PLeaf); ok && ctx.goimports[lhs.Value] {
			return
		}
		if lf, ok := node.RHS.(*ast.PLeaf); ok && isIdentLeaf(lf.Value) {
			leafEdit(src, lf, recaseMember(lf.Value, ctx.types), edits)
		} else {
			recaseWalk(src, node.RHS, ctx, edits)
		}
	case *ast.PMacroCall:
		recaseWalk(src, node.Head, ctx, edits)
		for _, a := range node.Args {
			recaseWalk(src, a, ctx, edits)
		}
	case *ast.PSigil:
		recaseWalk(src, node.Inner, ctx, edits)
	}
}

// recaseName chooses the spelling of a general identifier, respecting scope: a
// name bound locally (param/body let) is a value (snake_case, never `#`); any
// other name goes through the package-wide map / type rule.
func recaseName(name string, ctx *recaseCtx) string {
	if ctx.bound[name] {
		if literalNames[name] {
			return name
		}
		return toSnakeCase(name)
	}
	return recaseLeaf(name, ctx.renames, ctx.types)
}

func cloneSet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s)+4)
	for k := range s {
		out[k] = true
	}
	return out
}

// recaseFunLike walks a `(fun …)` / `(method …)` form in a fresh scope: its
// parameter names and body-local bindings are added to `bound` so references to
// them recase as locals, not via the global private map.
func recaseFunLike(src string, br *ast.PBranch, ctx *recaseCtx, edits *[]edit) {
	child := &recaseCtx{ctx.renames, ctx.types, ctx.goimports, cloneSet(ctx.bound)}
	collectFunLocals(br, ctx.types, child.bound)
	for i := 1; i < len(br.Children); i++ {
		recaseWalk(src, br.Children[i], child, edits)
	}
}

// collectFunLocals adds a fun/method's parameter names and any body-local
// let/const/var/foreach binding names (anywhere in the subtree, closures
// included) to set.
func collectFunLocals(br *ast.PBranch, types, set map[string]bool) {
	for i := 1; i < len(br.Children); i++ {
		if pl, ok := br.Children[i].(*ast.PBranch); ok && pl.Open == "(" {
			for _, p := range pl.Children {
				// Skip type names: a SIGNATURE `(fun f (Number Number) R)` has a
				// type-list here, not value params — those stay types, not locals.
				if name := paramName(p); name != "" && !types[name] {
					set[name] = true
				}
			}
			break // the first (…) after the head is the parameter list
		}
	}
	collectBindings(br, set)
}

// paramName reads a parameter name: a bare leaf, or the `(optional x)` /
// `(spread x)` wrapper whose second child is the name.
func paramName(n ast.PNode) string {
	if lf, ok := n.(*ast.PLeaf); ok {
		return lf.Value
	}
	if br, ok := n.(*ast.PBranch); ok && br.Open == "(" && len(br.Children) == 2 {
		if lf, ok := br.Children[1].(*ast.PLeaf); ok {
			return lf.Value
		}
	}
	return ""
}

// collectBindings adds every let/const/var/foreach binding name found in the
// subtree to set. `let` is `(let [var] name = value …)`; const/var are pairs
// (defensive — Recase normally runs after the mechanical const→let pass).
func collectBindings(n ast.PNode, set map[string]bool) {
	br, ok := n.(*ast.PBranch)
	if !ok {
		return
	}
	if br.Open == "(" && len(br.Children) >= 1 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			switch head.Value {
			case "let":
				i := 1
				if i < len(br.Children) {
					if m, ok := br.Children[i].(*ast.PLeaf); ok && m.Value == "var" {
						i++
					}
				}
				for ; i+2 < len(br.Children); i += 3 {
					if name := bindLeafName(br.Children[i]); name != "" {
						set[name] = true
					}
				}
			case "const", "var":
				for i := 1; i+1 < len(br.Children); i += 2 {
					if name := bindLeafName(br.Children[i]); name != "" {
						set[name] = true
					}
				}
			case "foreach":
				if len(br.Children) >= 2 {
					if lf, ok := br.Children[1].(*ast.PLeaf); ok {
						set[lf.Value] = true
					}
				}
			}
		}
	}
	for _, c := range br.Children {
		collectBindings(c, set)
	}
}

// recaseConstruction recases a `(Type 'field' val …)` form: the type head as a
// leaf, the quoted field keys via the member rule (re-quoting), and the values
// recursively. This closes the struct-init/typed-decl gap where field keys are
// string literals after the `.{}` sugar.
func recaseConstruction(src string, br *ast.PBranch, ctx *recaseCtx, edits *[]edit) {
	if head, ok := br.Children[0].(*ast.PLeaf); ok {
		leafEdit(src, head, recaseLeaf(head.Value, ctx.renames, ctx.types), edits)
	}
	for i := 1; i < len(br.Children); i++ {
		if i%2 == 1 {
			if lf, ok := isStrLit(br.Children[i]); ok {
				recaseConstructionKey(src, lf, ctx, edits)
			}
		} else {
			recaseWalk(src, br.Children[i], ctx, edits)
		}
	}
}

// recaseConstructionKey recases one field key of a construction. The `.{}` sugar
// quotes a BARE source word into a synthetic `'X'` leaf whose SPAN still covers
// the bare token, while an explicitly-quoted key's span includes the quotes — so
// the edit is span-based and re-quotes only when the source already did.
func recaseConstructionKey(src string, lf *ast.PLeaf, ctx *recaseCtx, edits *[]edit) {
	inner := lf.Value[1 : len(lf.Value)-1]
	if !isIdentLeaf(inner) {
		return
	}
	newInner := recaseMember(inner, ctx.types)
	if newInner == inner {
		return
	}
	start := offsetOf(src, lf.Span.StartLine, lf.Span.StartCol)
	end := offsetOf(src, lf.Span.EndLine, lf.Span.EndCol)
	repl := newInner
	if start < len(src) && src[start] == '\'' {
		repl = "'" + newInner + "'" // source key was explicitly quoted
	}
	*edits = append(*edits, edit{start, end, repl})
}

// recaseStruct handles `(struct Name f0 f1 …)`: the Name is a type-ref leaf and
// the bare field names use the member rule (private lowercase fields gain `#`).
// The typed form `(struct (Name 'F' T …))` arrives as a construction branch at
// child[1], handled by recaseConstruction via the normal walk.
func recaseStruct(src string, br *ast.PBranch, ctx *recaseCtx, edits *[]edit) {
	if len(br.Children) >= 2 {
		if name, ok := br.Children[1].(*ast.PLeaf); ok && isIdentLeaf(name.Value) {
			leafEdit(src, name, recaseLeaf(name.Value, ctx.renames, ctx.types), edits)
		} else {
			recaseWalk(src, br.Children[1], ctx, edits)
		}
	}
	for _, c := range br.Children[2:] {
		if lf, ok := c.(*ast.PLeaf); ok && isIdentLeaf(lf.Value) {
			leafEdit(src, lf, recaseMember(lf.Value, ctx.types), edits)
		} else {
			recaseWalk(src, c, ctx, edits)
		}
	}
}

// fileTree pairs a parsed file with whether it's a library (.phl).
type fileTree struct {
	tree      []ast.PNode
	isLibrary bool
}

// buildGlobalMaps unions the per-file rename maps and type sets across every
// parsed file, so a name is renamed consistently package-wide (a private
// binding `#foo` matches its references in sibling files), respecting each
// file's library/program privacy rule.
func buildGlobalMaps(files []fileTree) (map[string]string, map[string]bool) {
	renames := map[string]string{}
	types := map[string]bool{}
	for k := range builtinTypes {
		types[k] = true
	}
	for _, f := range files {
		for k := range collectTypeNames(f.tree) {
			types[k] = true
		}
	}
	for _, f := range files {
		for k, v := range buildRenameMap(f.tree, f.isLibrary) {
			renames[k] = v
		}
	}
	return renames, types
}
