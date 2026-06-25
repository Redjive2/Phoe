package lint

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// An LSP analyzes half-typed buffers on every keystroke, so every entry
// point must survive partial and malformed input without panicking. A
// panic is recovered at the server layer, but a recovered panic still
// means the feature silently returns nothing (see the (if) crash that
// broke hover/completion for whole files). These tests drive every
// entry point over a corpus of valid source truncated at every byte
// prefix — the byte-by-byte typing an editor actually produces — and
// fail listing any input that panics.

// hardeningCorpus exercises every special form and surface construct the
// walker handles. Kept varied (nesting, dot chains, macros, spread/
// optional, interpolation) so truncation produces a wide range of
// incomplete shapes.
var hardeningCorpus = []string{
	`(fun add (a b) (+ a b))`,
	`(fun f ((spread xs) (optional o)) (keyof xs))`,
	`(method Point.shift (self d) (+ self.x d))`,
	`(struct Point x #y)`,
	`(let var p = Point.{ x 1 #y 2 })`,
	`(let k = (mod 10 3))`,
	`(= p.x 10)`,
	`(= x 5)`,
	`(if (< n 1) (foo) (bar))`,
	`(foreach x in [1 2 3] (io.print_line x))`,
	`(while (< i 10) then (identity do (step)))`,
	`(identity do (let var a = 1) (+ a 2))`,
	`(import 'std/io')`,
	`(goimport ('mathx' m))`,
	`(io.print_line self.#x.#y)`,
	`(myMacro! a b c)`,
	`(return (+ 1 2))`,
	`'hi %name and %(len items) at %obj.field'`,
	`(+ (mod 10 3) (- 4 (* 2 1)))`,
	// Multi-form: an incomplete early form must not poison later ones
	// (the exact shape of the bug this pass follows up on).
	"(if)\n(fun g (n) (+ n 1))",
	"(fun a () (do\n(if (< x 1)\n",
	// Bare special forms — every head reached with a 1-child branch,
	// the shape that broke `if`. The shape/decl/nav/completion handlers
	// must all tolerate the missing operands.
	`(fun)`, `(method)`, `(struct)`, `(let var)`, `(let)`, `(=)`,
	`(if)`, `(foreach)`, `(while)`, `(identity do)`, `(return)`, `(import)`, `(goimport)`,
	`(block)`, `(method m)`, `(fun x)`, `(= a)`,
	// Partial accessors and literals.
	`obj.`, `a.b.c.d`, `x.[1 : 3]`, `x.[1 :]`, `s.[: 2]`, `{`, `[`, `(`,
	`{'k'`, `[1 2`, `(io.`, `(a.#b.#c d)`,
	// Deep nesting truncated mid-form.
	`(do (do (do (if (< n 1)`,
}

// posAt returns the 1-based (line, col) at byte offset off in src.
func posAt(src string, off int) (line, col int) {
	line, col = 1, 1
	for i := 0; i < off && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func TestWalkerSurvivesPartialInput(t *testing.T) {
	type failure struct {
		where string
		src   string
		val   any
	}
	var (
		fails []failure
		seen  = map[string]bool{} // dedup by where+val
	)
	probe := func(where, src string, fn func()) {
		defer func() {
			if r := recover(); r != nil {
				key := fmt.Sprintf("%s|%v", where, r)
				if !seen[key] {
					seen[key] = true
					fails = append(fails, failure{where, src, r})
				}
			}
		}()
		fn()
	}

	for _, full := range hardeningCorpus {
		for n := 0; n <= len(full); n++ {
			prefix := full[:n]
			b := []byte(prefix)

			// Whole-file walkers — cursor-independent, so one probe per
			// prefix covers the entire tree walk.
			probe("AnalyzeFile", prefix, func() { AnalyzeFile("t.pho", b) })
			probe("DocumentSymbols", prefix, func() { DocumentSymbols("t.pho", b) })
			probe("SemanticTokens", prefix, func() { SemanticTokens("t.pho", b) })

			// Cursor walkers — probe at the caret (the just-typed
			// position), the highest-yield spot for truncation panics.
			line, col := posAt(prefix, len(prefix))
			probe("CompletionsAt", prefix, func() { CompletionsAt("t.pho", b, line, col) })
			probe("HoverAt", prefix, func() { HoverAt("t.pho", b, line, col) })
			probe("DefinitionAt", prefix, func() { DefinitionAt("t.pho", b, line, col) })
			probe("ReferencesAt", prefix, func() { ReferencesAt("", "t.pho", b, line, col) })
		}
	}

	if len(fails) > 0 {
		sort.Slice(fails, func(i, j int) bool { return fails[i].where < fails[j].where })
		var b strings.Builder
		fmt.Fprintf(&b, "%d distinct panic(s) on partial input:\n", len(fails))
		for _, f := range fails {
			fmt.Fprintf(&b, "  [%s] %v\n      on input %q\n", f.where, f.val, f.src)
		}
		t.Fatal(b.String())
	}
}

// FuzzLintEntryPoints mutates the corpus to explore far more malformed
// shapes than the hand-written truncations. No entry point may panic on
// any input. Run it with:
//
//	go test -run=^$ -fuzz=FuzzLintEntryPoints -fuzztime=30s ./pkg/lint
//
// Without -fuzz it just replays the seed corpus (and any saved
// regression inputs under testdata/fuzz), so it still runs in CI.
func FuzzLintEntryPoints(f *testing.F) {
	for _, s := range hardeningCorpus {
		f.Add(s)
		for n := 0; n <= len(s); n++ {
			f.Add(s[:n])
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		b := []byte(src)
		AnalyzeFile("t.pho", b)
		DocumentSymbols("t.pho", b)
		SemanticTokens("t.pho", b)
		line, col := posAt(src, len(src))
		CompletionsAt("t.pho", b, line, col)
		HoverAt("t.pho", b, line, col)
		DefinitionAt("t.pho", b, line, col)
		ReferencesAt("", "t.pho", b, line, col)
	})
}
