package goop

import "math/rand"

func (stdDependencies) RandomInt(min, max int) float64 {
	return float64(rand.Intn(max-min) + min)
}

func (stdDependencies) RandomFloat() float64 {
	return rand.Float64()
}