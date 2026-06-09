package goop

import "fmt"

func (stdDependencies) DebugLog(args ...any) {
	fmt.Println(args...)
}