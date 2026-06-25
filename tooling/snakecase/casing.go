package main

import (
	"strings"

	"pho/pkg/ast"
)

// Casing transforms for the syntax migration's hard half: rewriting identifier
// SPELLING to the new scheme (Doc/PlanV1/Syntax.md). Values become snake_case,
// types become Title_Snake_Case. These are the pure, name-only transforms; the
// SEMANTIC decision of which names are types vs values (and which are private)
// lives in the classifier below and is still in progress.

func isUpper(r byte) bool { return r >= 'A' && r <= 'Z' }
func isLower(r byte) bool { return r >= 'a' && r <= 'z' }
func isDigit(r byte) bool { return r >= '0' && r <= '9' }

// splitWords breaks an identifier into its component words, recognizing both
// snake_case (`_` separators) and camelCase/PascalCase boundaries. An acronym
// run stays one word until a Capitalized tail begins: `HTTPServer` → [HTTP,
// Server]. A leading `#` and a trailing `?` are NOT part of any word — callers
// strip them first.
func splitWords(s string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			flush()
			continue
		}
		if isUpper(c) && i > 0 {
			prev := s[i-1]
			var next byte
			if i+1 < len(s) {
				next = s[i+1]
			}
			// New word before an uppercase that follows a lowercase/digit
			// (`aB`, `2B`), or that starts a Capitalized tail after an acronym
			// run (`HTTPServer`: the `S` is preceded by upper, followed by lower).
			if isLower(prev) || isDigit(prev) || (isUpper(prev) && isLower(next)) {
				flush()
			}
		}
		cur.WriteByte(c)
	}
	flush()
	return words
}

// splitAffixes peels a leading `#` (private marker) and trailing `?` (predicate
// convention) off a name, returning them so the caller can reattach after a
// casing transform of the bare middle.
func splitAffixes(s string) (hash, bare, question string) {
	if strings.HasPrefix(s, "#") {
		hash, s = "#", s[1:]
	}
	if strings.HasSuffix(s, "?") {
		question, s = "?", s[:len(s)-1]
	}
	return hash, s, question
}

// toSnakeCase rewrites a value identifier to snake_case, preserving a leading
// `#` and trailing `?`: `myVar`→`my_var`, `PctlSpawn`→`pctl_spawn`, `Is?`→`is?`,
// `#argOrField`→`#arg_or_field`. Already-snake names are returned unchanged.
func toSnakeCase(s string) string {
	hash, bare, q := splitAffixes(s)
	words := splitWords(bare)
	for i, w := range words {
		words[i] = strings.ToLower(w)
	}
	return hash + strings.Join(words, "_") + q
}

// toTitleSnake rewrites a type identifier to Title_Snake_Case, preserving a
// leading `#` and trailing `?`: `MyStruct`→`My_Struct`, `Point`→`Point`,
// `HTTPServer`→`Http_Server`, `Number`→`Number`.
func toTitleSnake(s string) string {
	hash, bare, q := splitAffixes(s)
	words := splitWords(bare)
	for i, w := range words {
		words[i] = titleWord(w)
	}
	return hash + strings.Join(words, "_") + q
}

func titleWord(w string) string {
	if w == "" {
		return ""
	}
	b := []byte(strings.ToLower(w))
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

// ---- type-name classifier (in progress) ----

// builtinTypes are the predeclared type names. They are already single Title
// words, so toTitleSnake is a no-op for them — listed so the classifier treats
// references to them as types (not values to snake_case).
var builtinTypes = map[string]bool{
	"Number": true, "String": true, "List": true, "Map": true, "Boolean": true,
	"Char": true, "Atom": true, "Function": true, "NilT": true, "Type": true,
	"Unknown": true, "None": true, "Collection": true, "Dynamic": true,
	"Or": true, "And": true, "Not": true, "Diff": true, "Fun": true,
	"Struct": true, "Trait": true,
}

// collectTypeNames scans top-level forms for user-declared type names — the
// names bound by `(struct …)`, `(type …)`, `(trait …)` — and returns the set
// seeded with the builtin types. This is the first half of the value-vs-type
// classification: a Capitalized name in this set rewrites with toTitleSnake; a
// Capitalized name NOT in it is a value (a public const/method) → toSnakeCase.
func collectTypeNames(tree []ast.PNode) map[string]bool {
	types := map[string]bool{}
	for k := range builtinTypes {
		types[k] = true
	}
	for _, form := range tree {
		br, ok := form.(*ast.PBranch)
		if !ok || br.Open != "(" || len(br.Children) < 2 {
			continue
		}
		head, ok := br.Children[0].(*ast.PLeaf)
		if !ok {
			continue
		}
		switch head.Value {
		case "struct", "type", "trait":
			if name := typeDeclName(br.Children[1]); name != "" {
				types[name] = true
			}
		}
	}
	return types
}

// typeDeclName reads the name a struct/type/trait declaration binds. The bare
// form `(struct Name …)` has a leaf name; the typed struct form `(struct
// Name.{ … })` was rewritten at parse time to `(struct (Name "F" T …))`, so the
// name is the head leaf of the inner call.
func typeDeclName(n ast.PNode) string {
	switch node := n.(type) {
	case *ast.PLeaf:
		return node.Value
	case *ast.PBranch:
		if node.Open == "(" && len(node.Children) >= 1 {
			if head, ok := node.Children[0].(*ast.PLeaf); ok {
				return head.Value
			}
		}
	}
	return ""
}

func isCapitalized(n string) bool { return n != "" && n[0] >= 'A' && n[0] <= 'Z' }

// bindLeafName reads a const/var binding name: a bare leaf `x` or the typed form
// `(Type x)` whose second child is the name.
func bindLeafName(n ast.PNode) string {
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

// collectTopLevelValues returns each top-level value binding (const/var/fun)
// mapped to whether it was PUBLIC under the old capitalization rule. Used to
// decide visibility: a public binding becomes plain snake_case; a private one
// gains a `#` prefix.
func collectTopLevelValues(tree []ast.PNode) map[string]bool {
	out := map[string]bool{}
	for _, form := range tree {
		br, ok := form.(*ast.PBranch)
		if !ok || br.Open != "(" || len(br.Children) < 2 {
			continue
		}
		head, ok := br.Children[0].(*ast.PLeaf)
		if !ok {
			continue
		}
		switch head.Value {
		case "const", "var":
			for i := 1; i+1 < len(br.Children); i += 2 {
				if n := bindLeafName(br.Children[i]); n != "" {
					out[n] = isCapitalized(n)
				}
			}
		case "fun":
			// Named form `(fun name (args) body)` — child[1] is the name leaf;
			// the anonymous form has an arg-list branch there instead.
			if lf, ok := br.Children[1].(*ast.PLeaf); ok {
				out[lf.Value] = isCapitalized(lf.Value)
			}
		}
	}
	return out
}

// buildRenameMap produces the declaration-level rename map for one file: each
// declared name → its new spelling. Type names (struct/type/trait, plus
// builtins) rewrite with toTitleSnake; value names with toSnakeCase. In a
// LIBRARY (.phl) a private (lowercase) value gains a `#` prefix — module
// visibility moves from capitalization to `#`. In a PROGRAM (.pho) there are no
// module members, so no value gets `#`. No-op renames are dropped.
func buildRenameMap(tree []ast.PNode, isLibrary bool) map[string]string {
	types := collectTypeNames(tree)
	values := collectTopLevelValues(tree)
	renames := map[string]string{}

	for name := range types {
		if builtinTypes[name] {
			continue // builtins keep their spelling
		}
		renames[name] = toTitleSnake(name)
	}
	for name, public := range values {
		if types[name] {
			continue // a name can't be both; the type classification wins
		}
		if isLibrary && !public {
			renames[name] = "#" + toSnakeCase(name)
		} else {
			renames[name] = toSnakeCase(name)
		}
	}
	for k, v := range renames {
		if k == v {
			delete(renames, k)
		}
	}
	return renames
}

// collectGoimports returns the set of `goimport` aliases — Go-module handles
// (`(goimport ('stdDependencies' dep))` → `dep`). Members accessed through them
// (`dep.PctlSpawn`) name FIXED Go exports and must never be recased.
func collectGoimports(tree []ast.PNode) map[string]bool {
	out := map[string]bool{}
	for _, form := range tree {
		br, ok := form.(*ast.PBranch)
		if !ok || br.Open != "(" || len(br.Children) < 2 {
			continue
		}
		head, ok := br.Children[0].(*ast.PLeaf)
		if !ok || head.Value != "goimport" {
			continue
		}
		for _, arg := range br.Children[1:] {
			if pair, ok := arg.(*ast.PBranch); ok && pair.Open == "(" && len(pair.Children) == 2 {
				if alias, ok := pair.Children[1].(*ast.PLeaf); ok {
					out[alias.Value] = true
				}
			}
		}
	}
	return out
}
