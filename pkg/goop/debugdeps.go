package goop

import (
	"fmt"
	"strings"

	"pho/pkg/core"
)

// DebugLog prints the display form of each value to stdout (no separator —
// the std/debug library formats its own spacing) followed by a newline.
func (*stdDependencies) DebugLog(args ...core.Tval) {
	parts := make([]string, len(args))
	for i, v := range args {
		parts[i] = core.Stringify(v)
	}
	fmt.Println(strings.Join(parts, ""))
}
