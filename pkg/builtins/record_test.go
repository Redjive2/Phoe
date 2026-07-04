package builtins

import "testing"

// `Struct.{ T X U Y }` is an OPEN structural record type: a struct instance
// inhabits it by SHAPE (having the fields with conforming values), regardless
// of its nominal struct. Bare `Struct` is any struct instance. Width + depth.
func TestStructRecordRuntime(t *testing.T) {
	const d = "(struct Point x y)\n(let p = Point.{ x = 1 y = 2 })\n"
	wantBool(t, d+"(p.is? Struct.{ Number x })", true)
	wantBool(t, d+"(p.is? Struct.{ Number x Number y })", true)
	wantBool(t, d+"(p.is? Struct.{ String x })", false) // wrong field type
	wantBool(t, d+"(p.is? Struct.{ Number z })", false) // missing field
	wantBool(t, d+"(p.is? Struct)", true)               // bare Struct = any struct
	wantBool(t, "(5.is? Struct)", false)                // a non-struct is not a struct

	// Structural: a DIFFERENT nominal struct with a matching shape also matches.
	wantBool(t, "(struct A x)\n(struct B x)\n(let b = B.{ x = 5 })\n(b.is? Struct.{ Number x })", true)

	// subtype? over records: width + depth.
	wantBool(t, "(subtype? Struct.{ Number x Number y } Struct.{ Number x })", true)
	wantBool(t, "(subtype? Struct.{ Number x } Struct.{ Number x Number y })", false)
	wantBool(t, "(subtype? Struct.{ 5 x } Struct.{ Number x })", true)
	wantBool(t, "(subtype? Struct.{ Number x } Unknown)", true)
	wantBool(t, "(subtype? Struct.{ Number x } Number)", false)

	// Literal and composite field types.
	wantBool(t, d+"(p.is? Struct.{ 1 x })", true)
	wantBool(t, d+"(p.is? Struct.{ 2 x })", false)
	wantBool(t, "(struct Box v)\n(let bx = Box.{ v = 200 })\n(bx.is? Struct.{ (Or 200 404) v })", true)

	// Optional record: (Or Struct.{ … } Nil).
	wantBool(t, d+"(p.is? (Or Struct.{ Number x } None))", true)
	wantBool(t, "(none.is? (Or Struct.{ Number x } None))", true)
}
