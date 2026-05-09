package main

import (
	"fmt"
	"math/rand/v2"
)

var (
	// ManglerSuffix is applied to the end of internal operator names to prevent people from calling them in their code
	ManglerSuffix = "_" + fmt.Sprint(rand.IntN(10000000)) + "m"

	WithEnv = "withenv" + ManglerSuffix
	Dot     = "dot" + ManglerSuffix
)
