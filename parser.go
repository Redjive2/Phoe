package main

import (
	"cmp"
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strings"
)

////////////////////
//     PARSER     //
////////////////////

func SkipComments(input string) string {
	var filtered []string

	rx := regexp.MustCompile("\\n[ \\t]+")
	for _, line := range rx.Split(input, -1) {
		if len(line) >= 2 && line[:2] == "--" {
			continue
		}

		filtered = append(filtered, line)
	}

	var result string
	for _, line := range filtered {
		result += line + " "
	}

	return result
}

type tedit struct {
	Pos int
	Val string
}

func (e tedit) Apply(s string) string {
	return s[:e.Pos] + e.Val + s[e.Pos+1:]
}

var (
	rval   = fmt.Sprint(rand.Intn(1000000))
	lbrack = "#LBRK:" + rval + "#"
	rbrack = "#RBRK:" + rval + "#"
	lbrace = "#LBRC:" + rval + "#"
	rbrace = "#RBRC:" + rval + "#"
	lparen = "#LPAR:" + rval + "#"
	rparen = "#RPAR:" + rval + "#"
	space  = "#SP:" + rval + "#"
	quote  = "#Q:" + rval + "#"
	amp    = "#A:" + rval + "#"
	excl   = "#X:" + rval + "#"
	dot    = "#D:" + rval + "#"
)

func Escape(input string) string {
	var (
		target   = input
		inString bool
		i        int
		edits    []tedit
	)

	for {
		if i >= len(input) {
			break
		}

		ch := input[i]
		if inString {
			if ch == '(' {
				edits = append(edits, tedit{i, lparen})
			}

			if ch == ')' {
				edits = append(edits, tedit{i, rparen})
			}

			if ch == '[' {
				edits = append(edits, tedit{i, lbrack})
			}

			if ch == ']' {
				edits = append(edits, tedit{i, rbrack})
			}

			if ch == '{' {
				edits = append(edits, tedit{i, lbrace})
			}

			if ch == '}' {
				edits = append(edits, tedit{i, rbrace})
			}

			if ch == ' ' {
				edits = append(edits, tedit{i, space})
			}

			if ch == '\'' {
				edits = append(edits, tedit{i, quote})
			}

			if ch == '*' {
				edits = append(edits, tedit{i, amp})
			}

			if ch == '!' {
				edits = append(edits, tedit{i, excl})
			}

			if ch == '.' {
				edits = append(edits, tedit{i, dot})
			}

			if ch == '"' {
				inString = false
			}

			i += 1
			continue
		}

		if ch == '"' {
			inString = true
		}

		if ch == '`' {
			ch = input[i+1]

			if ch == '(' {
				edits = append(edits, tedit{i, lparen})
			}

			if ch == ')' {
				edits = append(edits, tedit{i, rparen})
			}

			if ch == '[' {
				edits = append(edits, tedit{i, lbrack})
			}

			if ch == ']' {
				edits = append(edits, tedit{i, rbrack})
			}

			if ch == '{' {
				edits = append(edits, tedit{i, lbrace})
			}

			if ch == '}' {
				edits = append(edits, tedit{i, rbrace})
			}

			if ch == ' ' {
				edits = append(edits, tedit{i, space})
			}

			if ch == '\'' {
				edits = append(edits, tedit{i, quote})
			}

			if ch == '&' {
				edits = append(edits, tedit{i, amp})
			}

			if ch == '!' {
				edits = append(edits, tedit{i, excl})
			}

			if ch == '.' {
				edits = append(edits, tedit{i, dot})
			}

			i += 3
			continue
		}

		i += 1
	}

	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		target = edit.Apply(target)
	}

	return target
}

func UnEscape(input string) string {
	target := input

	target = strings.ReplaceAll(target, lparen, "(")
	target = strings.ReplaceAll(target, rparen, ")")
	target = strings.ReplaceAll(target, lbrack, "[")
	target = strings.ReplaceAll(target, rbrack, "]")
	target = strings.ReplaceAll(target, lbrace, "{")
	target = strings.ReplaceAll(target, rbrace, "}")
	target = strings.ReplaceAll(target, space, " ")
	target = strings.ReplaceAll(target, quote, "'")
	target = strings.ReplaceAll(target, amp, "&")
	target = strings.ReplaceAll(target, excl, "!")
	target = strings.ReplaceAll(target, dot, ".")

	return target
}

func Lex(input string) []string {
	spaced := SkipComments(input)
	spaced = Escape(spaced)

	spaced = strings.ReplaceAll(spaced, "(", " ( ")
	spaced = strings.ReplaceAll(spaced, ")", " ) ")
	spaced = strings.ReplaceAll(spaced, "[", " ( slice ")
	spaced = strings.ReplaceAll(spaced, "]", " ) ")
	spaced = strings.ReplaceAll(spaced, "{", " ( map ")
	spaced = strings.ReplaceAll(spaced, "}", " ) ")
	spaced = strings.ReplaceAll(spaced, "'", " ' ")
	spaced = strings.ReplaceAll(spaced, "&", " & ")
	spaced = strings.ReplaceAll(spaced, "!", " ! ")
	spaced = strings.ReplaceAll(spaced, ".", " . ")

	rx := regexp.MustCompile("\\s+")
	result := rx.Split(spaced, -1)

	for i, section := range result {
		result[i] = UnEscape(section)
	}

	return result
}

func CompressCodeLiterals(tree ttnode) ttnode {
	tr := tree.(ttbranch)
	var result ttbranch

	var onTick bool
	for _, node := range tr {
		if lf, ok := node.(ttleaf); ok {
			if onTick {
				result = append(result, ListifyTree(lf))
				onTick = false
				continue
			}

			if lf == "'" {
				onTick = true
				continue
			}

			result = append(result, lf)
		} else {
			if onTick {
				result = append(result, ListifyTree(CompressCodeLiterals(node)))
				onTick = false
				continue
			}

			result = append(result, CompressCodeLiterals(node))
		}
	}

	return result
}

func ListifyTree(tree ttnode) ttnode {
	if lf, ok := tree.(ttleaf); ok {
		return "\"" + lf + "\""
	}

	branch := tree.(ttbranch)
	newBranch := make(ttbranch, len(branch)+1)

	newBranch[0] = ttleaf("slice")

	for i, node := range branch {
		newBranch[i+1] = ListifyTree(node)
	}

	return newBranch
}

func ListifyVal(val tval) tval {
	if val.Kind == KindStr {
		str := fmt.Sprint(val.Val)
		return TvStr("\"" + str + "\"")
	}

	var (
		list    = *val.Val.(*[]tval)
		newList = make([]tval, len(list)+1)
	)

	newList[0] = TvStr("slice")

	for i := range list {
		newList[i+1] = ListifyVal(list[i])
	}

	return TvSlice(newList)
}

func TreeifyVal(val tval) ttnode {

	if str, ok := val.Val.(string); ok {
		return ttleaf(str)
	} else if val.Kind == KindStr { // stupid dumb go can't figure out how strings work when they have quotes around them
		return ttleaf("\"" + fmt.Sprint(val.Val) + "\"")
	}

	list := *val.Val.(*[]tval)

	branch := make(ttbranch, len(list)+1)

	branch[0] = ttleaf("list")

	for i := range list {
		branch[i+1] = TreeifyVal(list[i])
	}

	return branch
}

func CompressBlockLiterals(tree ttnode) ttnode {
	tr := tree.(ttbranch)
	var result ttbranch

	var onAmp bool
	for _, node := range tr {
		if lf, ok := node.(ttleaf); ok {
			if onAmp {
				// &leaf -> (block 'leaf)
				result = append(result, ttbranch{ttleaf("block"), ttleaf("'"), lf})
				onAmp = false
				continue
			}

			if lf == "&" {
				onAmp = true
				continue
			}

			result = append(result, lf)
		} else {
			if onAmp {
				// &(...expr) -> (block '(...expr))
				result = append(result, ttbranch{ttleaf("block"), ttleaf("'"), CompressBlockLiterals(node)})
				onAmp = false
				continue
			}

			result = append(result, CompressBlockLiterals(node))
		}
	}

	return result
}

type tChainEdit struct {
	Position, Length int
	NewValue         ttnode
}

func (e tChainEdit) Apply(br ttbranch) ttbranch {
	return slices.Concat(br[:e.Position-1], ttbranch{e.NewValue}, br[e.Position+e.Length-1:])
}

// buildDotChain returns ttbranch{Dot, values[0], ttbranch{Dot, values[1], ...}}
func buildDotChain(values []ttnode) ttbranch {
	if len(values) == 1 {
		fmt.Println("(WARN) weird ahh situation @ 'internal/parser.go:buildDotChain'")
		return ttbranch{values[0]}
	}

	result := ttbranch{ttleaf(Dot), nil, nil}
	currentBranch := &result

	for i := len(values) - 1; i > 1; i-- {
		newBranch := ttbranch{ttleaf(Dot), nil, nil}
		(*currentBranch)[1] = newBranch
		(*currentBranch)[2] = values[i]
		currentBranch = &newBranch
	}

	(*currentBranch)[1] = values[0]
	(*currentBranch)[2] = values[1]

	return result
}

func consumeDotChain(tree ttbranch, position int) (ttbranch, int) {
	var (
		hitDot bool
		values []ttnode
		i      int
	)

	for i = position - 1; i < len(tree); i++ {
		node := tree[i]

		if node == ttleaf(".") {
			hitDot = true
			continue
		}

		if !hitDot && i != position-1 {
			break
		}

		hitDot = false
		values = append(values, node)
	}

	return buildDotChain(values), position + (len(values)*2 - 1)

}

func findEdits(tree ttnode) []tChainEdit {
	if _, ok := tree.(ttleaf); ok {
		return []tChainEdit{}
	}

	var (
		edits []tChainEdit
		tr    = tree.(ttbranch)
	)

	if len(tr) < 3 {
		return edits
	}

	for i := 1; i < len(tr)-1; i++ {
		node := tr[i]

		if node == ttleaf(".") {
			chain, newIndex := consumeDotChain(tr, i)

			edits = append(edits, tChainEdit{i, newIndex - i, chain})
			i = newIndex
		}
	}

	return edits
}

func CompressDotLiterals(tree ttnode) ttnode {
	if _, ok := tree.(ttleaf); ok {
		return tree
	}

	tr := tree.(ttbranch)

	edits := findEdits(tr)
	edits = slices.SortedFunc(slices.Values(edits), func(a, b tChainEdit) int {
		return cmp.Compare(b.Position, a.Position)
	})

	for _, edit := range edits {
		tr = edit.Apply(tr)
	}

	for i, node := range tr {
		tr[i] = CompressDotLiterals(node)
	}

	return tr
}

func CompressMacroLiterals(tree ttnode) ttnode {
	if _, ok := tree.(ttleaf); ok {
		return tree
	}

	tr := tree.(ttbranch)
	var result ttbranch

	for _, node := range tr {
		// (mymacro! a b c) -> (resume (mymacro 'a 'b 'c))
		if branch, ok := node.(ttbranch); ok && len(branch) >= 2 && branch[1] == ttleaf("!") {
			newBranch := make(ttbranch, len(branch)-1)

			newBranch[0] = branch[0]

			for i := 1; i < len(branch)-1; i++ {
				newBranch[i] = CompressMacroLiterals(ListifyTree(branch[i+1]))
			}

			result = append(result, ttbranch{ttleaf("resume"), newBranch})
			continue
		}

		result = append(result, CompressMacroLiterals(node))
	}

	return result
}

func ParseTreeInner(lexed []string, i *int) ttnode {
	var tree ttbranch

	for {
		if *i >= len(lexed) {
			break
		}

		tok := lexed[*i]

		if tok == "(" {
			*i += 1
			subtree := ParseTreeInner(lexed, i)
			tree = append(tree, subtree)
			continue
		}

		if tok == ")" {
			*i += 1
			break
		}

		tree = append(tree, ttleaf(tok))

		*i += 1
	}

	return tree
}

func Parse(lexed []string) ttnode {
	i := 0
	tree := ParseTreeInner(lexed, &i).(ttbranch)

	tree = CompressBlockLiterals(tree).(ttbranch)
	tree = CompressDotLiterals(tree).(ttbranch)
	tree = CompressMacroLiterals(tree).(ttbranch)
	tree = CompressCodeLiterals(tree).(ttbranch)

	return ttnode(tree[1 : len(tree)-1])
}

func Inspect(code ttnode) string {
	if branch, ok := code.(ttbranch); ok {
		if branch[0] == ttleaf(Dot) {
			return Inspect(branch[1]) + "." + Inspect(branch[2])
		}

		if branch[0] == ttleaf("slice") {
			result := "["

			for _, elem := range branch[1:] {
				result += Inspect(elem) + " "
			}

			return result[:len(result)-1] + "]"
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

	return string(code.(ttleaf))
}
