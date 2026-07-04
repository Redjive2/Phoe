package main

// The `-split` pass is the mechanical half of the decl/impl split
// (Doc/PlanV1/DeclImplSplit.md): `fun`/`method` become type SIGNATURES, and
// IMPLEMENTATIONS move to the `=` form.
//
//	(fun add (a b) (+ a b))        →  (= add (a b) (+ a b))
//	(method Pair.sum (self) …)     →  (= Pair.sum (self) …)
//	(property P.x get (method P (self) B))  →  (property P.x (get (self) B))
//
// SIGNATURES are LEFT ALONE — `(fun add (Number Number) Number)` and
// `(method R.m (Self) Boolean)` already read as sigs (all-type params + a type
// return), detected exactly as the linter does (isFunSigForm). Anonymous forms
// (`(fun (a) b)`), `(static …)` forms, and trait *requirement* flags are also
// left untouched. Trait BODIES follow the split: their method sigs stay
// `method`, their named impls swap to `=`.
//
// Like Transform, this reuses the real lexer+parser and REFUSES (returns an
// error) on any lex/parse error rather than risk corrupting a file, and is
// format-preserving (span-anchored edits only).

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"

	"pho/pkg/ast"
	"pho/pkg/syntax"
)

// ---- detectors mirrored from pkg/lint (decls.go / scope.go) ----
//
// These are copies, not imports: pkg/lint keeps them unexported, and a codemod
// that misclassifies a sig as an impl would corrupt a file. They mirror the
// linter's tolerant-phase logic 1:1 so the codemod rewrites exactly the forms
// the linter already accepts in both shapes. Keep in sync with decls.go.

// asList returns br if n is a `(…)` list, else ok=false (mirrors lint.asList).
func asList(n ast.PNode) (*ast.PBranch, bool) {
	br, ok := n.(*ast.PBranch)
	if !ok || br.Open != "(" {
		return nil, false
	}
	return br, true
}

// headIdent returns the head identifier of a list, or "" (mirrors lint.headIdent).
func headIdent(br *ast.PBranch) string {
	if br == nil || len(br.Children) == 0 {
		return ""
	}
	leaf, ok := br.Children[0].(*ast.PLeaf)
	if !ok {
		return ""
	}
	return leaf.Value
}

var slIdentRe = regexp.MustCompile(`^#?[A-Za-z][A-Za-z0-9_]*\??!?$`)

func slLooksLikeIdentifier(v string) bool {
	if slIdentRe.MatchString(v) {
		return true
	}
	switch v {
	case "+", "-", "*", "/", "==", "~=", "<=", ">=", "<", ">", "=":
		return true
	}
	return false
}

var slTypeConnectives = map[string]bool{
	"Or": true, "And": true, "Not": true, "Diff": true,
	"List": true, "Map": true, "Fun": true, "Struct": true, "Trait": true,
}

func slLooksLikeTypePNode(n ast.PNode) bool {
	if leaf, ok := n.(*ast.PLeaf); ok {
		v := leaf.Value
		if v == "Nil" || v == "True" || v == "False" {
			return false
		}
		if len(v) > 0 && v[0] == '#' {
			v = v[1:]
		}
		return v != "" && v[0] >= 'A' && v[0] <= 'Z'
	}
	if br, ok := asList(n); ok && len(br.Children) >= 1 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			return slTypeConnectives[head.Value] || head.Value == "fun"
		}
	}
	return false
}

func slLooksLikeReturnTypePNode(n ast.PNode) bool {
	if leaf, ok := n.(*ast.PLeaf); ok {
		switch leaf.Value {
		case "Nil", "True", "False", "none", "true", "false":
			return true
		}
	}
	return slLooksLikeTypePNode(n)
}

func slLooksLikeSigParam(p ast.PNode) bool {
	if br, ok := asList(p); ok && len(br.Children) == 2 {
		if head, ok := br.Children[0].(*ast.PLeaf); ok {
			switch head.Value {
			case "var", "spread", "optional", "disc":
				return slLooksLikeTypePNode(br.Children[1])
			}
		}
	}
	return slLooksLikeTypePNode(p)
}

// slIsFunSigForm mirrors decls.go isFunSigForm: `(params) ret` is a SIGNATURE
// when every param slot is a type and the return slot is too.
func slIsFunSigForm(params, ret ast.PNode) bool {
	br, ok := asList(params)
	if !ok {
		return false
	}
	for _, p := range br.Children {
		if !slLooksLikeSigParam(p) {
			return false
		}
	}
	if len(br.Children) > 0 {
		return slLooksLikeReturnTypePNode(ret)
	}
	return slLooksLikeTypePNode(ret)
}

// ---- the transform ----

// SplitTransform rewrites one Pho source string, swapping fun/method IMPL heads
// to `=` and unwrapping old-form property delegates. It returns the new source
// and the edit count, and refuses on lex/parse errors.
func SplitTransform(src string) (string, int, error) {
	toks, lexErrs := syntax.LexPos(src)
	if len(lexErrs) > 0 {
		return src, 0, fmt.Errorf("refusing to transform: %d lex error(s)", len(lexErrs))
	}
	tree, parseErrs := syntax.ParsePos(toks)
	if len(parseErrs) > 0 {
		return src, 0, fmt.Errorf("refusing to transform: %d parse error(s)", len(parseErrs))
	}

	var edits []edit
	for _, form := range tree {
		collectSplitEdits(src, form, false, &edits)
	}
	if len(edits) == 0 {
		return src, 0, nil
	}
	return applyEdits(src, edits), len(edits), nil
}

// collectSplitEdits handles ONE form. It is deliberately shallow: fun/method
// impls live only at the top level or directly inside a `(trait …)` body (no
// nested impls exist in the corpus), so recursing into arbitrary bodies would
// only risk rewriting quoted data. `inTrait` marks the trait-body pass, where
// property REQUIREMENT flags must be left bare.
func collectSplitEdits(src string, form ast.PNode, inTrait bool, edits *[]edit) {
	br, ok := asList(form)
	if !ok {
		return
	}
	switch headIdent(br) {
	case "fun":
		*edits = append(*edits, funImplEdit(src, br)...)
	case "method":
		*edits = append(*edits, methodImplEdit(src, br, inTrait)...)
	case "property":
		if !inTrait { // a trait's `(property self.x get)` is a requirement flag — leave it
			*edits = append(*edits, propertyUnwrapEdits(src, br)...)
		}
	case "static":
		// static forms are not migrated by the codemod (the only one in the
		// corpus is a signature); leave untouched.
	case "trait":
		for _, c := range br.Children[1:] {
			collectSplitEdits(src, c, true, edits)
		}
	}
}

// funImplEdit swaps the head of a NAMED, non-signature `(fun name (params)
// body)` to `=`. Anonymous funs (3 children) and signatures are left alone.
func funImplEdit(src string, br *ast.PBranch) []edit {
	if len(br.Children) != 4 {
		return nil
	}
	name, ok := br.Children[1].(*ast.PLeaf)
	if !ok || !slLooksLikeIdentifier(name.Value) {
		return nil
	}
	if slIsFunSigForm(br.Children[2], br.Children[3]) {
		return nil
	}
	return []edit{headSwap(src, br.Children[0])}
}

// methodImplEdit swaps the head of a NAMED, non-signature `(method Owner.name
// (params) body)` to `=`. A bare-receiver anonymous method (`(method Owner
// (params) body)`, a property delegate) and signatures are left alone. Inside a
// trait body it also leaves signature-style REQUIREMENTS whose param list uses a
// lowercase `self` receiver (which `isFunSigForm` doesn't recognize as a sig).
func methodImplEdit(src string, br *ast.PBranch, inTrait bool) []edit {
	if len(br.Children) != 4 {
		return nil
	}
	dot, ok := br.Children[1].(*ast.PDot)
	if !ok {
		return nil
	}
	owner, ok := dot.LHS.(*ast.PLeaf)
	if !ok || !slLooksLikeIdentifier(owner.Value) {
		return nil
	}
	if nm, ok := dot.RHS.(*ast.PLeaf); !ok || !slLooksLikeIdentifier(nm.Value) {
		return nil
	}
	if slIsFunSigForm(br.Children[2], br.Children[3]) {
		return nil
	}
	// Inside a trait, a method whose RETURN slot is a type is a signature-style
	// REQUIREMENT (`(method self.area (self) Number)`), not a default
	// implementation — leave it. Mirrors the runtime's `!isTypeNode(br[3])` rule
	// in addTraitMember (a type 4th element = required return type, not a body).
	if inTrait && slLooksLikeTypePNode(br.Children[3]) {
		return nil
	}
	return []edit{headSwap(src, br.Children[0])}
}

// headSwap replaces the head-keyword leaf with `=`.
func headSwap(src string, head ast.PNode) edit {
	s := head.GetSpan()
	start := offsetOf(src, s.StartLine, s.StartCol)
	return edit{start, offsetOf(src, s.EndLine, s.EndCol), "="}
}

// propertyUnwrapEdits rewrites the old flat delegate forms of a property to the
// new parenthesized sub-forms:
//
//	get (method Owner (params) body)  →  (get (params) body)
//	set (fun (params) body)           →  (set (params) body)
//
// Each `get`/`set` keyword leaf is paired with the delegate branch that
// follows it. The keyword moves inside the delegate's parens, replacing the
// delegate's head (`method Owner` or `fun`).
func propertyUnwrapEdits(src string, br *ast.PBranch) []edit {
	var out []edit
	for i := 1; i+1 < len(br.Children); i++ {
		kw, ok := br.Children[i].(*ast.PLeaf)
		if !ok || (kw.Value != "get" && kw.Value != "set") {
			continue
		}
		deleg, ok := asList(br.Children[i+1])
		if !ok || len(deleg.Children) < 1 {
			continue
		}
		head := headIdent(deleg)
		var headEnd ast.PNode // last node of the head-to-strip ("fun" or "method Owner")
		switch head {
		case "method":
			// (method Owner (params) body): strip `method Owner`, keep params+body.
			if len(deleg.Children) < 3 {
				continue
			}
			headEnd = deleg.Children[1]
		case "fun":
			// (fun (params) body): strip just `fun`.
			if len(deleg.Children) < 2 {
				continue
			}
			headEnd = deleg.Children[0]
		default:
			continue
		}
		// 1. Remove the outer keyword leaf and the whitespace up to the delegate's `(`.
		kwStart := offsetOf(src, kw.Span.StartLine, kw.Span.StartCol)
		dOpen := offsetOf(src, deleg.Span.StartLine, deleg.Span.StartCol)
		out = append(out, edit{kwStart, dOpen, ""})
		// 2. Replace the delegate head (`method Owner` / `fun`) with the keyword.
		hStart := offsetOf(src, deleg.Children[0].GetSpan().StartLine, deleg.Children[0].GetSpan().StartCol)
		out = append(out, edit{hStart, endOf(src, headEnd), kw.Value})
		i++ // consume the delegate we just paired
	}
	return out
}

// ---- Go-embedded variant ----

// SplitGoFile applies SplitTransform to Pho embedded in Go string literals,
// mirroring MigrateGoFile's scan but running only the split pass.
func SplitGoFile(src string) (string, int, error) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), nil, 0)

	type lit struct {
		off  int
		text string
	}
	var lits []lit
	for {
		pos, tok, litText := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.STRING {
			lits = append(lits, lit{off: file.Offset(pos), text: litText})
		}
	}

	out := src
	changed := 0
	for i := len(lits) - 1; i >= 0; i-- {
		l := lits[i]
		newLit, ok := splitGoLiteral(l.text)
		if !ok || newLit == l.text {
			continue
		}
		out = out[:l.off] + newLit + out[l.off+len(l.text):]
		changed++
	}
	return out, changed, nil
}

func splitGoLiteral(goLit string) (result string, changed bool) {
	defer func() {
		if r := recover(); r != nil {
			prefix := goLit
			if len(prefix) > 50 {
				prefix = prefix[:50]
			}
			fmt.Fprintf(os.Stderr, "  skip (panic %v): %s…\n", r, prefix)
			result, changed = goLit, false
		}
	}()
	if len(goLit) < 2 {
		return goLit, false
	}
	val, err := strconv.Unquote(goLit)
	if err != nil || !looksLikePhoCode(val) {
		return goLit, false
	}
	migrated, n, err := SplitTransform(val)
	if err != nil || n == 0 {
		return goLit, false
	}
	if goLit[0] == '`' && !strings.Contains(migrated, "`") {
		return "`" + migrated + "`", true
	}
	return strconv.Quote(migrated), true
}
