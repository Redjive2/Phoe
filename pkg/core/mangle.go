package core

import (
	"fmt"
	"math/rand/v2"
)

var (
	// ManglerSuffix is applied to the end of internal operator names to prevent people from calling them in their code
	ManglerSuffix = "_" + fmt.Sprint(rand.IntN(10000000)) + "m"

	WithEnv = "withenv" + ManglerSuffix
	Dot     = "dot" + ManglerSuffix

	// Strinterp and Strcoerce are emitted by the syntax lower pass to
	// implement string interpolation. The user-facing `"%name"` lowers
	// to (Strinterp lit (Strcoerce name) lit ...). Both names are
	// mangled for the same reason as Dot: hide them from user code so
	// they can't be redefined or invoked directly.
	Strinterp = "strinterp" + ManglerSuffix
	Strcoerce = "strcoerce" + ManglerSuffix
)
