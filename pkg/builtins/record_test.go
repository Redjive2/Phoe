package builtins

import "testing"

// `Struct.{ X T Y U }` is an OPEN structural record type: a struct instance
// inhabits it by SHAPE (having the fields with conforming values), regardless
// of its nominal struct. Bare `Struct` is any struct instance. Width + depth.
func TestStructRecordRuntime(t *testing.T) {
	const d = "(struct Point X Y)\n(const p Point.{ X 1 Y 2 })\n"
	wantBool(t, d+"(p.Is? Struct.{ X Number })", true)
	wantBool(t, d+"(p.Is? Struct.{ X Number Y Number })", true)
	wantBool(t, d+"(p.Is? Struct.{ X String })", false) // wrong field type
	wantBool(t, d+"(p.Is? Struct.{ Z Number })", false) // missing field
	wantBool(t, d+"(p.Is? Struct)", true)               // bare Struct = any struct
	wantBool(t, "(5.Is? Struct)", false)                // a non-struct is not a struct

	// Structural: a DIFFERENT nominal struct with a matching shape also matches.
	wantBool(t, "(struct A X)\n(struct B X)\n(const b B.{ X 5 })\n(b.Is? Struct.{ X Number })", true)

	// subtype? over records: width + depth.
	wantBool(t, "(subtype? Struct.{ X Number Y Number } Struct.{ X Number })", true)
	wantBool(t, "(subtype? Struct.{ X Number } Struct.{ X Number Y Number })", false)
	wantBool(t, "(subtype? Struct.{ X 5 } Struct.{ X Number })", true)
	wantBool(t, "(subtype? Struct.{ X Number } Unknown)", true)
	wantBool(t, "(subtype? Struct.{ X Number } Number)", false)

	// Literal and composite field types.
	wantBool(t, d+"(p.Is? Struct.{ X 1 })", true)
	wantBool(t, d+"(p.Is? Struct.{ X 2 })", false)
	wantBool(t, "(struct Box V)\n(const bx Box.{ V 200 })\n(bx.Is? Struct.{ V (Or 200 404) })", true)

	// Optional record: (Or Struct.{ … } Nil).
	wantBool(t, d+"(p.Is? (Or Struct.{ X Number } Nil))", true)
	wantBool(t, "(Nil.Is? (Or Struct.{ X Number } Nil))", true)
}
