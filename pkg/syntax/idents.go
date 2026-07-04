package syntax

// Identifier shape classification for the kebab-case / Title-Kebab-Case rule.
//
// Under the naming scheme every identifier lexeme must be either a well-formed
// *value* name (kebab-case) or a *type* name (Title-Kebab-Case); anything else
// (camelCase, PascalCase, SCREAMING, acronyms) is rejected at the reader. A
// single leading `#` (private marker) and the trailing effect suffixes are
// permitted on either kind. See Doc/PlanV1/Syntax.md.

// IdentKind classifies an identifier lexeme by its casing shape.
type IdentKind int

const (
	// IdentInvalid is neither kebab-case nor Title-Kebab-Case.
	IdentInvalid IdentKind = iota
	// IdentValue is kebab-case: bindings, parameters, keywords, method names.
	IdentValue
	// IdentType is Title-Kebab-Case: type names.
	IdentType
)

// StrictNames gates reader-level rejection of non-conforming identifiers. It is
// off during the migration (so the suite keeps building against old sources)
// and flipped on at the hard cutover, after the codemod has converted every
// source name. While off, classifyIdent is still available to tooling.
var StrictNames = false

// ClassifyIdent is the exported form of classifyIdent, for tooling (the linter)
// that enforces the naming rule while reader-level StrictNames is still off.
func ClassifyIdent(name string) IdentKind { return classifyIdent(name) }

// classifyIdent reports whether name is a well-formed value identifier
// (kebab-case), a type identifier (Title-Kebab-Case), or neither. A leading '#'
// (private marker) and the trailing effect suffixes '?' (predicate), '!'
// (environmental effect) and '=' (self/value mutation) are stripped before
// classification — the suffixes, when present, read `name?!=`, so they peel off
// outermost-first ('=' then '!' then '?').
//
//	kebab-case      words of [a-z0-9]+, first word starting with a letter,
//	                joined by single hyphens: my-var, parse-2, vec-3d.
//	Title-Kebab-Case words of [A-Z][a-z0-9]*, joined by single hyphens:
//	                Type-Name, Integer, My-Struct. PascalCase and acronyms
//	                (TypeName, HTTP) are invalid.
func classifyIdent(name string) IdentKind {
	if len(name) > 0 && name[0] == '#' {
		name = name[1:]
	}
	if len(name) > 0 && name[len(name)-1] == '=' {
		name = name[:len(name)-1]
	}
	if len(name) > 0 && name[len(name)-1] == '!' {
		name = name[:len(name)-1]
	}
	if len(name) > 0 && name[len(name)-1] == '?' {
		name = name[:len(name)-1]
	}
	if name == "" {
		return IdentInvalid
	}
	switch c := name[0]; {
	case c >= 'a' && c <= 'z':
		if validKebabWords(name, false) {
			return IdentValue
		}
	case c >= 'A' && c <= 'Z':
		if validKebabWords(name, true) {
			return IdentType
		}
	}
	return IdentInvalid
}

// validKebabWords reports whether name is a sequence of words joined by single
// hyphens, where each word is valid per validWord(word, titled). An empty word
// (leading, trailing, or doubled hyphen) makes the whole name invalid.
func validKebabWords(name string, titled bool) bool {
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '-' {
			if !validWord(name[start:i], titled) {
				return false
			}
			start = i + 1
		}
	}
	return true
}

// validWord checks a single hyphen-delimited word. A type word is
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
