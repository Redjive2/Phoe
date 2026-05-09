package main

import (
	"fmt"
	"regexp"
	"strconv"
)

func DistributeSpreadExpressions(branch ttbranch) []tval {
	var result []tval

	for _, node := range branch {
		if br, ok := node.(ttbranch); ok &&
			len(br) == 2 &&
			br[0] == ttleaf("spread") {

			list := *br[1].Evaluate().Val.(*[]tval)

			for _, val := range list {
				result = append(result, val)
			}

			continue
		}

		result = append(result, node.Evaluate())
	}

	return result
}

func (br ttbranch) Evaluate() tval {
	if len(br) == 0 {
		return TvNil
	}

	if fname, ok := br[0].(ttleaf); ok {
		fn, found := Resolve(string(fname))

		if !found {
			fmt.Println("(ERR): Operation '" + fname + "' not found @ internal 'ttbranch.Evaluate'.")
			return TvNil
		}

		if fn.Kind == KindConstructor {
			return fn.Val.(tconstructor).Constructor(br[1:])
		}

		return fn.Val.(tfun)(br[1:])
	}

	funBranch := br[0].(ttbranch)

	fn, ok := funBranch.Evaluate().Val.(tfun)

	if !ok {
		fmt.Println("(ERR): '" + Inspect(funBranch) + "' is not a function @ internal 'ttbranch.Evaluate'.")
		return TvNil
	}

	return fn(br[1:])
}

func (lf ttleaf) Evaluate() tval {
	s := string(lf)

	// match numbers
	if regexp.MustCompile("^[0-9]+(\\.[0-9]+)?$").MatchString(s) {
		num, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return TvNum(num)
		}

		fmt.Println("(ERR): Value '" + s + "' could not be parsed as a number @ internal 'ttleaf.Evaluate'.")
		return TvNil
		// match strings
	} else if regexp.MustCompile("^\".*\"$").MatchString(s) {
		return TvStr(s[1 : len(s)-1])
		// match chars
	} else if regexp.MustCompile("^`.`$").MatchString(s) {
		return TvChr(rune(s[1]))
		// match nil
	} else if s == "Nil" {
		return TvNil
		// match bools
	} else if s == "True" || s == "False" {
		return TvBool(s == "True")
		// match identifiers
	} else if regexp.MustCompile("^[a-zA-Z](([a-zA-Z0-9]*)|([a-zA-Z0-9_]*[a-zA-Z0-9]))$").MatchString(s) {
		data, found := Resolve(s)
		if !found {
			fmt.Println("(WARN): Identifier '" + s + "' not found @ internal 'ttleaf.Evaluate'.")
			return TvNil
		}

		return data
	}

	// last resort: for functions not using identifier syntax (+, -, etc)
	data, found := Resolve(s)
	if found {
		return data
	}

	fmt.Println("(ERR): Value '" + s + "' could not be parsed @ internal 'ttleaf.Evaluate'.")
	return TvNil
}

func Derepr(node ttnode) ttnode {
	if branch, ok := node.(ttbranch); ok {
		if len(branch) == 0 {
			return ttbranch{}
		}

		result := make(ttbranch, len(branch)-1)

		for i := 0; i < len(branch)-1; i++ {
			result[i] = Derepr(branch[i+1])
		}

		return result
	}

	lf := node.(ttleaf)

	if lf[0] == '"' {
		lf = lf[1 : len(lf)-1]
	}

	return lf
}
