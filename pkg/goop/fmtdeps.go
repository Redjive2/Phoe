package goop

import (
	"strings"

	"pho/pkg/core"
)

// FmtSprint concatenates the display form of each value (no separator —
// callers add their own spacing). Taking core.Tval keeps the Kind tag, so
// arrays/dicts render as Pho syntax instead of Go pointer internals.
func (*stdDependencies) FmtSprint(data ...core.Tval) string {
	var b strings.Builder
	for _, v := range data {
		b.WriteString(core.Stringify(v))
	}
	return b.String()
}
