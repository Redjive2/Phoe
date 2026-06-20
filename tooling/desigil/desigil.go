// Command desigil rewrites Pho source to the post-cutover syntax. It does
// two surgical transforms:
//
//  1. Removes the ' and & sigils from the *structural* argument slots of the
//     declaration and control builtins — fun, method, struct, var, const, =,
//     if, for — where the sigil was pure boilerplate.
//
//  2. Wraps any standalone `(do …)` form as `(identity do …)`. With `do`
//     promoted to parse-level do-notation (a bare `do` captures its trailing
//     siblings), a `do` in head position over-nests into `((core.Do …))` and
//     fails as "not callable"; `identity` is the head that absorbs it. This
//     keeps multi-statement bodies and if/for arms working after their sigils
//     are removed.
//
// Both transforms are conservative about *value* positions. A sigil is only
// removed when it sits in a slot that is, by the form's grammar, always a
// static name / parameter list / field list / deferred body — never a runtime
// value. Quotes used as data — map and dict keys, array elements, import
// aliases, struct-initializer dict keys, and any other quote-as-data — are
// left exactly as written. Likewise a `(do …)` reached as data (inside a
// genuine quote) is never touched, only one evaluated as code.
//
// The rewrite parses with the real positioned parser (pkg/syntax) and edits
// the source by source position, leaving every other byte — whitespace,
// comments, layout — untouched.
package main

import (
	"fmt"
	"sort"

	"pho/pkg/ast"
	"pho/pkg/syntax"
)

// Transform rewrites src to the post-cutover syntax and returns the result.
//
// If src has any lex or parse error, Transform refuses to touch it and
// returns (src, 0, error): a migration tool must never silently corrupt a
// file it couldn't fully understand. On success the second return is the
// number of edits made (0 means src was already in the new syntax, so
// Transform is idempotent — a second run is a no-op).
func Transform(src string) (string, int, error) {
	toks, lexErrs := syntax.LexPos(src)
	tree, parseErrs := syntax.ParsePos(toks)
	if n := len(lexErrs) + len(parseErrs); n > 0 {
		return src, 0, fmt.Errorf("refusing to transform: %d lex/parse error(s)", n)
	}

	var d desigiler
	for _, n := range tree {
		// Every top-level form is evaluated as code.
		d.walk(n)
	}
	return d.apply(src)
}

// editKind distinguishes a sigil deletion from an identity-wrap insertion.
type editKind int

const (
	delSigil    editKind = iota // delete the single ' or & byte at (line,col)
	insIdentity                 // insert "identity " before the byte at (line,col)
)

type edit struct {
	line, col int
	kind      editKind
}

type desigiler struct {
	edits []edit
}

// cut records the leading sigil of a PSigil for deletion. The sigil byte
// sits at the node's span start (see parseSigil in pkg/syntax).
func (d *desigiler) cut(s *ast.PSigil) {
	d.edits = append(d.edits, edit{s.Span.StartLine, s.Span.StartCol, delSigil})
}

// wrapDo records an "identity " insertion before the `do` head leaf of a
// standalone do-form, turning `(do …)` into `(identity do …)`.
func (d *desigiler) wrapDo(doLeaf *ast.PLeaf) {
	d.edits = append(d.edits, edit{doLeaf.Span.StartLine, doLeaf.Span.StartCol, insIdentity})
}

// walk visits a node sitting in a CODE position — somewhere the runtime
// evaluates. It descends to find nested declaration/control/do forms but
// strips nothing on its own; stripping only happens through the slot
// helpers (stripQuote/…), which the recognized-form handler invokes for
// the specific positions that are structural.
//
// A `'expr` quote is data: walk does not descend into it (a fun or do
// written inside a quote is data, not real code) and never edits it — this
// is what protects map keys, import aliases, and quoted do-data. A `&expr`
// reached here is a first-class thunk value; its sigil stays, but its body
// is code, so we descend.
func (d *desigiler) walk(n ast.PNode) {
	switch node := n.(type) {
	case *ast.PBranch:
		// Parse-time annotations (`--@ (form)` comments preceding the form)
		// are real, isolated-eval Pho code, so structural sigils inside them
		// need migrating too. Descend into each before the form itself.
		for _, ann := range node.Annotations {
			if ann.Form != nil {
				d.walk(ann.Form)
			}
		}
		d.handleBranch(node)
	case *ast.PSigil:
		if node.Sigil == "'" {
			return // data — leave untouched, do not descend
		}
		d.walk(node.Inner) // & thunk value: keep sigil, descend into body
	case *ast.PDot:
		d.walk(node.LHS) // RHS is a member name, not a reference
	case *ast.PMacroCall:
		d.walk(node.Head) // args are quoted at runtime — data
	}
	// Known limitation: a string literal's `%(expr)` interpolation chunks are
	// lexed into a single STRING leaf here (ParsePos doesn't expand them; only
	// the runtime's Lower does), so this walk never descends into interpolated
	// code. Structural sigils written inside `%(...)` are therefore NOT
	// migrated. The migrated stdlib has none, so this stays a latent gap; a
	// full fix would re-parse each chunk at its file offset and map edits back.
}

// handleBranch dispatches on the form head. Only `(`-forms are calls;
// `[` and `{` literals are pure value positions, so we descend into them
// (a nested fun may live in an array/dict value) but strip nothing.
func (d *desigiler) handleBranch(br *ast.PBranch) {
	if br.Open != "(" {
		d.walkAll(br)
		return
	}

	switch headIdent(br) {
	case "fun":
		switch len(br.Children) {
		case 3: // (fun (args) body)
			d.stripQuote(br.Children[1])
			d.stripQuoteAndWalk(br.Children[2])
		case 4: // (fun name (args) body)
			d.stripQuote(br.Children[1])
			d.stripQuote(br.Children[2])
			d.stripQuoteAndWalk(br.Children[3])
		default:
			d.walkAll(br)
		}

	case "method":
		if len(br.Children) == 5 { // (method Owner name (args) body)
			d.walk(br.Children[1]) // owner is a runtime reference
			d.stripQuote(br.Children[2])
			d.stripQuote(br.Children[3])
			d.stripQuoteAndWalk(br.Children[4])
		} else {
			d.walkAll(br)
		}

	case "struct":
		if len(br.Children) == 3 { // (struct Name (fields))
			d.stripQuote(br.Children[1])
			d.stripQuote(br.Children[2])
		} else {
			d.walkAll(br)
		}

	case "var", "const":
		// (var name val name val ...) — odd slots are names (structural),
		// even slots are values (code, possibly holding nested decls).
		for i := 1; i < len(br.Children); i++ {
			if i%2 == 1 {
				d.stripQuote(br.Children[i])
			} else {
				d.walk(br.Children[i])
			}
		}

	case "=":
		if len(br.Children) == 3 {
			d.stripQuote(br.Children[1]) // no-op for a dot/index target
			d.walk(br.Children[2])
		} else {
			d.walkAll(br)
		}

	case "if":
		// (if cond then) or (if cond then else): cond is an ordinary
		// expression; the arms were &blocks.
		if n := len(br.Children); n == 3 || n == 4 {
			d.walk(br.Children[1])
			for _, arm := range br.Children[2:] {
				d.stripBlockAndWalk(arm)
			}
		} else {
			d.walkAll(br)
		}

	case "for":
		switch len(br.Children) {
		case 3: // (for cond body) — while-style: both were &blocks
			d.stripBlockAndWalk(br.Children[1])
			d.stripBlockAndWalk(br.Children[2])
		case 4: // (for name coll body)
			d.stripQuote(br.Children[1])
			d.walk(br.Children[2])
			d.stripBlockAndWalk(br.Children[3])
		default:
			d.walkAll(br)
		}

	case "do":
		// Standalone (do …) over-nests under do-notation; give it the
		// `identity` head, then sequence its statements as code.
		if lf, ok := br.Children[0].(*ast.PLeaf); ok {
			d.wrapDo(lf)
		}
		d.walkAll(br)

	default:
		d.walkAll(br)
	}
}

func (d *desigiler) walkAll(br *ast.PBranch) {
	for _, c := range br.Children {
		d.walk(c)
	}
}

// stripQuote removes a leading ' from a name / parameter-list / field-list
// slot. These slots have nothing further to descend into (names are
// leaves; parameter and field lists contain only names and spread/optional
// wrappers). A slot that is already bare, or a dynamic/dot target, is left
// untouched.
func (d *desigiler) stripQuote(n ast.PNode) {
	if s, ok := n.(*ast.PSigil); ok && s.Sigil == "'" {
		d.cut(s)
	}
}

// stripQuoteAndWalk removes a leading ' from a fun/method body slot and
// then walks the body as code, so declarations and do-forms nested inside
// it are migrated too. A body inside a quote is the one place a `'` wraps
// code rather than data, which is why — unlike walk — we descend through it.
func (d *desigiler) stripQuoteAndWalk(n ast.PNode) {
	if s, ok := n.(*ast.PSigil); ok && s.Sigil == "'" {
		d.cut(s)
		d.walk(s.Inner)
		return
	}
	d.walk(n)
}

// stripBlockAndWalk removes a leading & from an if/for arm slot and walks
// the arm body as code.
func (d *desigiler) stripBlockAndWalk(n ast.PNode) {
	if s, ok := n.(*ast.PSigil); ok && s.Sigil == "&" {
		d.cut(s)
		d.walk(s.Inner)
		return
	}
	d.walk(n)
}

// headIdent returns the form's head identifier, or "" when the head isn't
// a bare leaf (e.g. an immediately-invoked anonymous form).
func headIdent(br *ast.PBranch) string {
	if len(br.Children) == 0 {
		return ""
	}
	if lf, ok := br.Children[0].(*ast.PLeaf); ok {
		return lf.Value
	}
	return ""
}

// apply replays every recorded edit against src. Edits are sorted by source
// offset and applied high-to-low so earlier offsets stay valid as bytes are
// inserted or removed. A sigil deletion verifies the target is actually a
// ' or & first; a mismatch aborts with the source returned unchanged, so a
// position-mapping bug can never silently corrupt a file.
func (d *desigiler) apply(src string) (string, int, error) {
	if len(d.edits) == 0 {
		return src, 0, nil
	}

	lineStarts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}

	type resolved struct {
		off  int
		kind editKind
	}
	rs := make([]resolved, 0, len(d.edits))
	for _, e := range d.edits {
		if e.line < 1 || e.line > len(lineStarts) {
			return src, 0, fmt.Errorf("edit line %d out of range (1..%d)", e.line, len(lineStarts))
		}
		off := lineStarts[e.line-1] + (e.col - 1)
		if off < 0 || off > len(src) {
			return src, 0, fmt.Errorf("edit at %d:%d maps to byte %d, out of range", e.line, e.col, off)
		}
		if e.kind == delSigil {
			if off >= len(src) || (src[off] != '\'' && src[off] != '&') {
				return src, 0, fmt.Errorf("expected a sigil at %d:%d", e.line, e.col)
			}
		}
		rs = append(rs, resolved{off, e.kind})
	}

	// Highest offset first; at equal offsets, deletions before insertions
	// (order is immaterial here since they never coincide, but keep it
	// deterministic).
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].off != rs[j].off {
			return rs[i].off > rs[j].off
		}
		return rs[i].kind < rs[j].kind
	})

	b := []byte(src)
	for _, r := range rs {
		switch r.kind {
		case delSigil:
			b = append(b[:r.off], b[r.off+1:]...)
		case insIdentity:
			ins := []byte("identity ")
			nb := make([]byte, 0, len(b)+len(ins))
			nb = append(nb, b[:r.off]...)
			nb = append(nb, ins...)
			nb = append(nb, b[r.off:]...)
			b = nb
		}
	}
	return string(b), len(rs), nil
}
