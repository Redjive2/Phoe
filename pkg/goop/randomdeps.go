package goop

import (
	"math/rand"
)

// RandomInt returns a uniform integer in [min, max). Pho numbers arrive as
// float64; an empty window is an error (rand.Intn panics on n <= 0).
func (*stdDependencies) RandomInt(min, max float64) float64 {
	if int(max)-int(min) <= 0 {
		hostErr("RandomInt: empty range [%d, %d)", int(min), int(max))
		return min
	}
	return float64(rand.Intn(int(max)-int(min)) + int(min))
}

func (*stdDependencies) RandomFloat() float64 {
	return rand.Float64()
}
