package goop

import "fmt"

func (stdDependencies) FmtSprint(data ...any) string {
	return fmt.Sprint(data...)
}