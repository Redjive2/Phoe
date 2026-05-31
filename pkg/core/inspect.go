package core

// Inspect renders an AST node back to its surface syntax. Used in error
// messages and by the (inspect ...) builtin.
func Inspect(code ttnode) string {
	if branch, ok := code.(ttbranch); ok {
		if branch[0] == ttleaf(Dot) {
			return Inspect(branch[1]) + "." + Inspect(branch[2])
		}

		if branch[0] == ttleaf("slice") {
			if len(branch) == 1 {
				return "[]"
			}

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
