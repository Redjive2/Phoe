package goop

import "strconv"

func (*stdDependencies) ParseAtoi(str string) float64 {
	n, _ := strconv.ParseFloat(str, 64)
	return n
}
