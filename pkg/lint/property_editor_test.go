package lint

import (
	"strings"
	"testing"
)

// Editor features for the property builtin and anonymous methods: dot
// completion lists a struct-field property, document symbols nest it under its
// struct, hover renders a free-standing property, and semantic tokens tag the
// property name @property and the get/set keywords @keyword.
func TestPropertyEditorFeatures(t *testing.T) {
	lines := []string{
		"(struct Temp #celsius)",                                    // 1
		"(property Temp.Fahrenheit",                                 // 2
		"    (get (self) (+ self.celsius 32))",                      // 3
		"    (set (self f) (= self.celsius f)))",                    // 4
		"(let var backing = 0)",                                     // 5
		"(property tally (get () backing) (set (v) (= backing v)))", // 6
		"(let var t = Temp.{ #celsius = 0 })",                       // 7
		"(var x t.)",                                                // 8
	}
	src := []byte(strings.Join(lines, "\n") + "\n")

	// 1. Completion after `t.` (line 8, just past the dot) lists the
	//    struct-field property as a member.
	if defs := CompletionsAt("p.pho", src, 8, 10); !containsName(defs, "Fahrenheit") {
		t.Errorf("t. completions missing property Fahrenheit: %v", defNames(defs))
	}

	// 2. Document symbols nest the property under its struct.
	syms := DocumentSymbols("p.pho", src)
	var temp *Symbol
	for i := range syms {
		if syms[i].Name == "Temp" {
			temp = &syms[i]
		}
	}
	if temp == nil {
		t.Fatal("no Temp struct symbol")
	}
	nested := false
	for _, c := range temp.Children {
		if c.Name == "Fahrenheit" {
			nested = true
		}
	}
	if !nested {
		t.Errorf("Fahrenheit property not nested under Temp; children %+v", temp.Children)
	}

	// 3. Hover on the free-standing property name `Tally` (line 6, col 12).
	if md, _, ok := HoverAt("p.pho", src, 6, 12); !ok || !strings.Contains(md, "property") {
		t.Errorf("hover on a property should mention 'property'; ok=%v md=%q", ok, md)
	}

	// 4. Semantic tokens: the property name is @property and get/set @keyword.
	tokText := func(line, sc, ec int) string {
		if line < 1 || line > len(lines) {
			return ""
		}
		s := lines[line-1]
		if sc-1 < 0 || ec-1 > len(s) || sc > ec {
			return ""
		}
		return s[sc-1 : ec-1]
	}
	var gotProp, gotGet, gotSet bool
	for _, tk := range SemanticTokens("p.pho", src) {
		text := tokText(tk.Span.StartLine, tk.Span.StartCol, tk.Span.EndCol)
		switch {
		case text == "Fahrenheit" && tk.Type == SemTokProperty:
			gotProp = true
		case text == "get" && tk.Type == SemTokKeyword:
			gotGet = true
		case text == "set" && tk.Type == SemTokKeyword:
			gotSet = true
		}
	}
	if !gotProp {
		t.Errorf("property name not tagged @property")
	}
	if !gotGet || !gotSet {
		t.Errorf("get/set not tagged @keyword (get=%v set=%v)", gotGet, gotSet)
	}
}
