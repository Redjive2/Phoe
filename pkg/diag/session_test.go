package diag

import "testing"

// TestSessionTraceShift pins the call-stack "shift": the innermost frame
// shows the error's own location, and every outer frame shows the call
// site of the frame just inside it (so a caller line points at where it
// invoked the callee, not at the callee's body).
func TestSessionTraceShift(t *testing.T) {
	s := NewSession()
	// Push order is outermost-first; each frame stores its call site.
	s.Push(Frame{Name: "<top level>", File: "f.pho", Span: sp(6, 1, 6, 22)})
	s.Push(Frame{Name: "tally", File: "f.pho", Span: sp(6, 1, 6, 22)})   // tally called at 6:1
	s.Push(Frame{Name: "double", File: "f.pho", Span: sp(5, 38, 5, 51)}) // double called at 5:38

	got := s.Trace("f.pho", sp(4, 20, 4, 27)) // error inside double at 4:20
	want := []Frame{
		{Name: "double", File: "f.pho", Span: sp(4, 20, 4, 27)},     // error's own span
		{Name: "tally", File: "f.pho", Span: sp(5, 38, 5, 51)},      // double's call site
		{Name: "<top level>", File: "f.pho", Span: sp(6, 1, 6, 22)}, // tally's call site
	}
	if len(got) != len(want) {
		t.Fatalf("Trace len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Trace[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// TraceRaw (foreign-panic path): each frame at its own call site,
	// innermost first — no error-span injection.
	raw := s.TraceRaw()
	wantRaw := []Frame{
		{Name: "double", File: "f.pho", Span: sp(5, 38, 5, 51)},
		{Name: "tally", File: "f.pho", Span: sp(6, 1, 6, 22)},
		{Name: "<top level>", File: "f.pho", Span: sp(6, 1, 6, 22)},
	}
	for i := range wantRaw {
		if raw[i] != wantRaw[i] {
			t.Errorf("TraceRaw[%d] = %+v, want %+v", i, raw[i], wantRaw[i])
		}
	}
}

// TestSessionTruncateRestoresDepth: Truncate(base) drops frames pushed
// since base (how the loader restores after a top-level form / cleans up
// after a foreign panic) without disturbing outer frames.
func TestSessionTruncateRestoresDepth(t *testing.T) {
	s := NewSession()
	s.Push(Frame{Name: "outer"})
	base := s.Depth()
	s.Push(Frame{Name: "inner1"})
	s.Push(Frame{Name: "inner2"})
	s.Truncate(base)
	if s.Depth() != 1 {
		t.Fatalf("Depth after Truncate = %d, want 1", s.Depth())
	}
	if tr := s.TraceRaw(); len(tr) != 1 || tr[0].Name != "outer" {
		t.Errorf("after Truncate, frames = %+v, want [outer]", tr)
	}
}

// TestNilSessionStackOps: a nil session (bare test Context) tolerates
// every stack operation.
func TestNilSessionStackOps(t *testing.T) {
	var s *Session
	s.Push(Frame{Name: "x"})
	s.Pop()
	s.Truncate(0)
	if s.Depth() != 0 || s.Trace("f", sp(1, 1, 1, 2)) != nil || s.TraceRaw() != nil {
		t.Error("nil session stack ops not inert")
	}
}
