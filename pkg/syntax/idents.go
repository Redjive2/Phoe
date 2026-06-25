package syntax

// Identifier shape classification for the snake_case / Title_Snake_Case rule.
//
// Under the new naming scheme every identifier lexeme must be either a
// well-formed *value* name (snake_case) or a *type* name (Title_Snake_Case);
// anything else (camelCase, PascalCase, SCREAMING, acronyms) is rejected at the
// reader. A single leading `#` (private marker) and a single trailing `?`
// (predicate convention) are permitted on either kind. See Doc/PlanV1/Syntax.md.

// IdentKind classifies an identifier lexeme by its casing shape.
type IdentKind int

const (
	// IdentInvalid is neither snake_case nor Title_Snake_Case.
	IdentInvalid IdentKind = iota
	// IdentValue is snake_case: bindings, parameters, keywords, method names.
	IdentValue
	// IdentType is Title_Snake_Case: type names.
	IdentType
)

// StrictNames gates reader-level rejection of non-conforming identifiers. It is
// off during the migration (so the suite keeps building against old sources)
// and flipped on at the hard cutover, after the codemod has converted every
// source name. While off, classifyIdent is still available to tooling.
var StrictNames = false

// classifyIdent reports whether name is a well-formed value identifier
// (snake_case), a type identifier (Title_Snake_Case), or neither. A single
// leading '#' and a single trailing '?' are stripped before classification.
//
//	snake_case      words of [a-z0-9]+, first word starting with a letter,
//	                joined by single underscores: my_var, parse_2, vec_3d.
//	Title_Snake_Case words of [A-Z][a-z0-9]*, joined by single underscores:
//	                Type_Name, Integer, My_Struct. PascalCase and acronyms
//	                (TypeName, HTTP) are invalid.
func classifyIdent(name string) IdentKind {
	if len(name) > 0 && name[0] == '#' {
		name = name[1:]
	}
	if len(name) > 0 && name[len(name)-1] == '?' {
		name = name[:len(name)-1]
	}
	if name == "" {
		return IdentInvalid
	}
	switch c := name[0]; {
	case c >= 'a' && c <= 'z':
		if validSnakeWords(name, false) {
			return IdentValue
		}
	case c >= 'A' && c <= 'Z':
		if validSnakeWords(name, true) {
			return IdentType
		}
	}
	return IdentInvalid
}

// validSnakeWords reports whether name is a sequence of words joined by single
// underscores, where each word is valid per validWord(word, titled). An empty
// word (leading, trailing, or doubled underscore) makes the whole name invalid.
func validSnakeWords(name string, titled bool) bool {
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '_' {
			if !validWord(name[start:i], titled) {
				return false
			}
			start = i + 1
		}
	}
	return true
}

// validWord checks a single underscore-delimited word. A type word is
// [A-Z][a-z0-9]* (Title-case); a value word is [a-z0-9]+ (lowercase/digits).
// The empty word is always invalid.
func validWord(word string, titled bool) bool {
	if word == "" {
		return false
	}
	if titled {
		if !(word[0] >= 'A' && word[0] <= 'Z') {
			return false
		}
		for k := 1; k < len(word); k++ {
			if c := word[k]; !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
				return false
			}
		}
		return true
	}
	for k := 0; k < len(word); k++ {
		if c := word[k]; !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
