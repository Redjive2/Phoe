package builtins

import "testing"

// `Struct.{ X T Y U }` is an OPEN structural record type: a struct instance
// inhabits it by SHAPE (having the fields with conforming values), regardless
// of its nominal struct. Bare `Struct` is any struct instance. Width + depth.
func TestStructRecordRuntime(t *testing.T) {
	const d = "(struct Point x y)\n(let p = Point.{ x 1 y 2 })\n"
	wantBool(t, d+"(p.is? Struct.{ x Number })", true)
	wantBool(t, d+"(p.is? Struct.{ x Number y Number })", true)
	wantBool(t, d+"(p.is? Struct.{ x String })", false) // wrong field type
	wantBool(t, d+"(p.is? Struct.{ z Number })", false) // missing field
	wantBool(t, d+"(p.is? Struct)", true)               // bare Struct = any struct
	wantBool(t, "(5.is? Struct)", false)                // a non-struct is not a struct

	// Structural: a DIFFERENT nominal struct with a matching shape also matches.
	wantBool(t, "(struct A x)\n(struct B x)\n(let b = B.{ x 5 })\n(b.is? Struct.{ x Number })", true)

	// subtype? over records: width + depth.
	wantBool(t, "(subtype? Struct.{ x Number y Number } Struct.{ x Number })", true)
	wantBool(t, "(subtype? Struct.{ x Number } Struct.{ x Number y Number })", false)
	wantBool(t, "(subtype? Struct.{ x 5 } Struct.{ x Number })", true)
	wantBool(t, "(subtype? Struct.{ x Number } Unknown)", true)
	wantBool(t, "(subtype? Struct.{ x Number } Number)", false)

	// Literal and composite field types.
	wantBool(t, d+"(p.is? Struct.{ x 1 })", true)
	wantBool(t, d+"(p.is? Struct.{ x 2 })", false)
	wantBool(t, "(struct Box v)\n(let bx = Box.{ v 200 })\n(bx.is? Struct.{ v (Or 200 404) })", true)

	// Optional record: (Or Struct.{ … } Nil).
	wantBool(t, d+"(p.is? (Or Struct.{ x Number } none))", true)
	wantBool(t, "(none.is? (Or Struct.{ x Number } none))", true)
}
