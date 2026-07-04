package core

import (
	"fmt"
	"strings"
)

// Stringify returns the display string for a runtime value. Used by the
// string-interpolation Strcoerce builtin and by Go-side helpers (fmt,
// debug) that render Pho values:
//
//   - str:  passed through verbatim (no requoting).
//   - num:  Go's default float formatting; whole numbers print
//     without a trailing ".0".
//   - bool: "true" / "false" — matches the source-syntax value literals.
//   - nil:  "none".
//   - chr:  the rune as a one-rune string.
//   - atom: the source form, ":" + the atom's name.
//   - array/dict: bracketed forms with recursive stringification,
//     mirroring the source-syntax sugar.
//   - fun/method/etc.: angle-bracketed type tag — these don't have a
//     natural source representation; better a stable placeholder
//     than something that pretends to round-trip.
func Stringify(v Tval) string {
	return stringify(v, nil)
}

// stringify renders v, guarding against cycles. Arrays, dicts, and instances
// are pointer-backed and mutable in place, so user code can make one contain
// itself (e.g. `(= a.[0] a)`); rendering it without a guard recurses forever
// and overflows the Go stack — an UNCATCHABLE host crash. `seen` holds the
// composite pointers on the current render path: a repeat is a cycle, rendered
// as an ellipsis. Pointers are removed on the way out (path-, not visit-,
// tracking) so a value referenced twice in sibling positions still renders in
// full — only a genuine back-edge is elided. `seen` is allocated lazily, so the
// common scalar render allocates nothing.
func stringify(v Tval, seen map[any]bool) string {
	switch v.Kind {
	case KindStr:
		return v.Val.(string)
	case KindNum:
		return fmt.Sprintf("%v", v.Val.(float64))
	case KindBool:
		if v.Val.(bool) {
			return "true"
		}
		return "false"
	case KindNil:
		return "none"
	case KindChr:
		return string(v.Val.(rune))
	case KindAtom:
		return ":" + v.Val.(*Atom).Name()
	case KindArray:
		ptr := v.Val.(*[]Tval)
		if seen[ptr] {
			return "[...]"
		}
		seen = markSeen(seen, ptr)
		defer delete(seen, ptr)
		items := *ptr
		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = stringify(item, seen)
		}
		return "[" + strings.Join(parts, " ") + "]"
	case KindDict:
		ptr := v.Val.(*map[Tval]Tval)
		if seen[ptr] {
			return "{...}"
		}
		seen = markSeen(seen, ptr)
		defer delete(seen, ptr)
		items := *ptr
		parts := make([]string, 0, 2*len(items))
		for k, val := range items {
			parts = append(parts, stringify(k, seen), stringify(val, seen))
		}
		return "{" + strings.Join(parts, " ") + "}"
	case KindInstance:
		instance := v.Val.(*tinstance)
		if seen[instance] {
			return "(<...>)"
		}
		seen = markSeen(seen, instance)
		defer delete(seen, instance)

		// Recover the struct's declared name by pointer identity against
		// its origin env. A miss (anonymous / detached struct) falls back
		// to "<struct>" so the rendering is still well-formed.
		name := "<struct>"
		for n, otherStruct := range instance.Struct.Origin.Structs {
			if instance.Struct == otherStruct {
				name = n
				break
			}
		}

		// Iterate the declared field order (a slice), not the Fields map,
		// so repeated renders of the same instance are deterministic.
		str := name + ".{"
		for _, fieldName := range instance.Struct.Fields {
			str += " " + fieldName + " = " + stringify(instance.Fields[fieldName], seen)
		}
		return str + " }"
	case KindType:
		return v.Val.(*PhoType).Name()
	}

	return "<" + v.Kind + ">"
}

// markSeen adds ptr to the render-path set, allocating it on first use.
func markSeen(seen map[any]bool, ptr any) map[any]bool {
	if seen == nil {
		seen = map[any]bool{}
	}
	seen[ptr] = true
	return seen
}

// Inspect renders an AST node back to its surface syntax. Used in error
// messages and by the (inspect ...) builtin.
func Inspect(code ttnode) string {
	if code == nil {
		return "<nil>"
	}
	code = Strip(code) // span wrappers are invisible to rendering

	if branch, ok := code.(ttbranch); ok {
		if len(branch) == 0 {
			return "()"
		}

		if len(branch) == 3 && branch[0] == ttleaf(Dot) {
			return Inspect(branch[1]) + "." + Inspect(branch[2])
		}

		if len(branch) == 3 && branch[0] == ttleaf(Slash) {
			return Inspect(branch[1]) + "/" + Inspect(branch[2])
		}

		if branch[0] == ttleaf(Slice) {
			if len(branch) == 1 {
				return "[]"
			}

			result := "["

			for _, elem := range branch[1:] {
				result += Inspect(elem) + " "
			}

			return result[:len(result)-1] + "]"
		}

		// Dict literals lower to (Map k v ...); render them back as the new
		// `[k -> v]` form (Syntax.md Phase 4b) so Inspect mirrors the surface
		// syntax the way the slice case does. Args alternate key, value; an
		// empty map renders `[]` (distinct from `{}`, which is no longer used).
		if branch[0] == ttleaf(Map) {
			if len(branch) == 1 {
				return "[]"
			}

			pairs := make([]string, 0, (len(branch)-1)/2)
			for i := 1; i+1 < len(branch); i += 2 {
				pairs = append(pairs, Inspect(branch[i])+" -> "+Inspect(branch[i+1]))
			}

			return "[" + strings.Join(pairs, " ") + "]"
		}

		// (core.Do …) is `do` notation. Render the mangled head as the
		// readable `do` keyword, the way dot/slice/map un-mangle — so the
		// internal name never leaks into messages or (inspect ...). A
		// head-position `(do …)` re-parses straight back to (core.Do …).
		if branch[0] == ttleaf(Do) {
			result := "(do"
			for _, elem := range branch[1:] {
				result += " " + Inspect(elem)
			}
			return result + ")"
		}

		// (Macrocall name 'a 'b) is the `(~name a b)` macro-call sugar.
		// Render the head with its `~` prefix so the mangled name never leaks;
		// the args are already-quoted code, shown as their data form.
		if branch[0] == ttleaf(Macrocall) && len(branch) >= 2 {
			result := "(~" + Inspect(branch[1])
			for _, elem := range branch[2:] {
				result += " " + Inspect(elem)
			}
			return result + ")"
		}

		result := "("

		for i, node := range branch {
			result += Inspect(node)

			if i != len(branch)-1 {
				result += " "
			}
		}

		return result + ")"
	}

	// A spread-spliced value node (see expandSpread) has no source form;
	// render its value so error messages that inspect a call's arguments
	// stay readable instead of panicking on the type assertion below.
	if v, ok := code.(ttvalue); ok {
		return Stringify(v.v)
	}

	return string(code.(ttleaf))
}

// SynthSpans renders an executable tree (the Derepr'd output of a macro
// expansion) to Pho source text AND returns a structurally identical tree
// whose forms are wrapped in ttspanned carrying spans into that text. The
// wrappers are transparent, so the returned tree evaluates exactly like
// node; the spans let an error in macro-generated code caret the precise
// offending sub-form within the rendered text (see ttspanned.Evaluate's
// expansion branch).
//
// The rendering mirrors Inspect exactly (dot → a.b, slice → [..], map →
// {..}) so spans line up with the text the user reads. Only forms
// (branches) are wrapped — never leaves — matching syntax.Lower, so the
// evaluator's head-leaf comparisons keep working.
func SynthSpans(node ttnode) (ttnode, string) {
	var b strings.Builder
	wrapped := synth(&b, node)
	return wrapped, b.String()
}

// synth renders one node, appending its text to b, and returns the node
// wrapped (if it's a form) with the span it occupies. The text is a
// single line, so line is always 1 and column is the byte offset + 1.
func synth(b *strings.Builder, node ttnode) ttnode {
	node = Strip(node) // generated trees are span-free, but be defensive
	branch, ok := node.(ttbranch)
	if !ok {
		b.WriteString(string(node.(ttleaf)))
		return node // leaves stay unwrapped, as in Lower
	}

	start := b.Len()
	out := synthBranch(b, branch)
	return &ttspanned{
		node: out,
		span: Span{StartLine: 1, StartCol: start + 1, EndLine: 1, EndCol: b.Len() + 1},
	}
}

// synthBranch renders a branch with the same surface sugar as Inspect and
// returns a structurally identical branch with its child forms wrapped.
func synthBranch(b *strings.Builder, branch ttbranch) ttbranch {
	if len(branch) == 0 {
		b.WriteString("()")
		return branch
	}

	// Dot chain: a.b — the Dot head stays an (unwrapped) leaf so dispatch
	// still recognizes it.
	if len(branch) == 3 && branch[0] == ttleaf(Dot) {
		out := make(ttbranch, 3)
		out[0] = branch[0]
		out[1] = synth(b, branch[1])
		b.WriteByte('.')
		out[2] = synth(b, branch[2])
		return out
	}

	// Slash chain: a/b — package navigation; head stays an (unwrapped) leaf.
	if len(branch) == 3 && branch[0] == ttleaf(Slash) {
		out := make(ttbranch, 3)
		out[0] = branch[0]
		out[1] = synth(b, branch[1])
		b.WriteByte('/')
		out[2] = synth(b, branch[2])
		return out
	}

	// Map literals: (Map k v …) → `[k -> v …]` (Syntax.md Phase 4b). The head
	// stays the mangled leaf for dispatch; the text writes `[`, each key/value
	// pair joined by ` -> ` with pairs space-separated, then `]`. Empty map → `[]`.
	if branch[0] == ttleaf(Map) {
		out := make(ttbranch, len(branch))
		out[0] = branch[0]
		b.WriteByte('[')
		for i := 1; i < len(branch); i++ {
			if i > 1 {
				if i%2 == 1 {
					b.WriteByte(' ') // boundary between pairs
				} else {
					b.WriteString(" -> ") // key → value within a pair
				}
			}
			out[i] = synth(b, branch[i])
		}
		b.WriteByte(']')
		return out
	}

	// Bracket literals: [..] (lists).
	if open, close, ok := bracketLiteral(branch[0]); ok {
		out := make(ttbranch, len(branch))
		out[0] = branch[0]
		b.WriteString(open)
		for i := 1; i < len(branch); i++ {
			if i > 1 {
				b.WriteByte(' ')
			}
			out[i] = synth(b, branch[i])
		}
		b.WriteString(close)
		return out
	}

	// Macro-call sugar: (Macrocall name 'a 'b) → (~name a b). The head stays
	// the mangled leaf for dispatch; the text writes `~` then the name.
	if branch[0] == ttleaf(Macrocall) && len(branch) >= 2 {
		out := make(ttbranch, len(branch))
		out[0] = branch[0]
		b.WriteByte('(')
		b.WriteByte('~')
		out[1] = synth(b, branch[1])
		for i := 2; i < len(branch); i++ {
			b.WriteByte(' ')
			out[i] = synth(b, branch[i])
		}
		b.WriteByte(')')
		return out
	}

	// `do` notation: (core.Do …) → (do …). The head stays the mangled leaf
	// so dispatch still recognizes it; only the rendered text shows `do`,
	// mirroring how the Dot head is kept while ".".is written.
	if branch[0] == ttleaf(Do) {
		out := make(ttbranch, len(branch))
		out[0] = branch[0]
		b.WriteString("(do")
		for i := 1; i < len(branch); i++ {
			b.WriteByte(' ')
			out[i] = synth(b, branch[i])
		}
		b.WriteByte(')')
		return out
	}

	// Generic call form: (a b c).
	out := make(ttbranch, len(branch))
	b.WriteByte('(')
	for i, child := range branch {
		if i > 0 {
			b.WriteByte(' ')
		}
		out[i] = synth(b, child)
	}
	b.WriteByte(')')
	return out
}

// bracketLiteral maps the mangled array head (core.Slice) to its surface
// brackets, mirroring Inspect. Maps (core.Map) are handled separately in
// synthBranch — they use `[k -> v]` form (Syntax.md Phase 4b), not bare
// brackets — so they are intentionally not listed here.
func bracketLiteral(head ttnode) (open, close string, ok bool) {
	switch head {
	case ttleaf(Slice):
		return "[", "]", true
	}
	return "", "", false
}
