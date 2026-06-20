package core

import (
	"fmt"
	"math/rand/v2"
)

var (
	// ManglerSuffix is applied to the end of internal operator names to prevent people from calling them in their code
	ManglerSuffix = "_" + fmt.Sprint(rand.IntN(10000000)) + "m"

	Dot = "dot" + ManglerSuffix

	// Do is the real sequencing accessor behind `do` notation. The lower
	// pass rewrites a bare `do` in a form into a (Do …) call wrapping every
	// following sibling (see splitDoForm); mangling the name hides it from
	// user code, exactly as with Dot.
	Do = "do" + ManglerSuffix

	// Strinterp and Strcoerce are emitted by the syntax lower pass to
	// implement string interpolation. The user-facing `"%name"` lowers
	// to (Strinterp lit (Strcoerce name) lit ...). Both names are
	// mangled for the same reason as Dot: hide them from user code so
	// they can't be redefined or invoked directly.
	Strinterp = "strinterp" + ManglerSuffix
	Strcoerce = "strcoerce" + ManglerSuffix

	// Macrocall backs the `(name! arg ...)` macro-call sugar: the lower
	// pass rewrites it to (Macrocall name 'arg ...), which resolves name to
	// a macro, calls it with the quoted args, and resumes the result.
	// Mangled like the others so user code can't invoke it directly.
	Macrocall = "macrocall" + ManglerSuffix
)
