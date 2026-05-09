package main

import "fmt"

func ParseArgs(caller string, pat []string, argv []ttnode) ([]any, bool) {
	var (
		result  = make([]any, len(argv))
		success = true
	)

	for i := range argv {
		arg := argv[i].Evaluate()

		if i < len(pat) {
			result[i] = arg.Val

			if arg.Kind != pat[i] && pat[i] != "..." {
				fmt.Println(
					"(ERR): arg '" + Inspect(argv[i]) + "' at position '" + fmt.Sprint(i) +
						"' is of type '" + arg.Kind + "' but type '" + pat[i] +
						"' was expected @ '" + caller + "'.")

				success = false
			}
		}
	}

	if pat[len(pat)-1] == "..." {
		for i := len(pat) - 1; i < len(argv); i++ {
			result[i] = argv[i].Evaluate().Val
		}
	}

	return result, success
}
