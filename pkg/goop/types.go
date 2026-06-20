package goop

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
)

// stdDependencies is the Go-side struct that backs the `stdDependencies`
// module the Pho stdlib (std/io, std/fmt, std/pctl, ...) imports via
// goimport. PhoModule.Data must hold a *pointer* to it: half the methods
// have pointer receivers, and reflection can't see those through a value.
//
// The stream/process maps are touched both by the main (single-threaded)
// evaluator and by the per-spawn reaper goroutine, so mu guards every map
// access. The lock is held only around map operations — never during a
// blocking read/write — so a read on one stream can't stall bookkeeping
// on another or deadlock against the reaper.
type stdDependencies struct {
	mu              sync.Mutex
	activeProcesses map[float64]*process // pid -> process
	activeStreams   map[float64]*stream  // stream id -> stream
	lastId          float64
}

// nextId allocates a fresh stream id. Callers must not already hold mu.
func (state *stdDependencies) nextId() float64 {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.lastId++
	return state.lastId
}

func (state *stdDependencies) putStream(s *stream) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.activeStreams[s.Id] = s
}

func (state *stdDependencies) getStream(id float64) (*stream, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	s, ok := state.activeStreams[id]
	return s, ok
}

func (state *stdDependencies) delStream(id float64) {
	state.mu.Lock()
	defer state.mu.Unlock()
	delete(state.activeStreams, id)
}

func (state *stdDependencies) putProcess(pid float64, p *process) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.activeProcesses[pid] = p
}

func (state *stdDependencies) getProcess(id float64) (*process, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	p, ok := state.activeProcesses[id]
	return p, ok
}

func (state *stdDependencies) delProcess(pid float64) {
	state.mu.Lock()
	defer state.mu.Unlock()
	delete(state.activeProcesses, pid)
}

// closeStream removes a stream from the registry and closes its underlying
// fd (if any). Host std streams have a nil closer and are never closed.
func (state *stdDependencies) closeStream(id float64) {
	s, ok := state.getStream(id)
	if !ok {
		return
	}
	state.delStream(id)
	if s.closer != nil {
		s.closer.Close()
	}
}

type process struct {
	Command      *exec.Cmd
	CancelHook   context.CancelFunc
	In, Out, Err float64 // stream ids, resolvable via activeStreams
}

// stream is one endpoint registered in activeStreams. Reader is non-nil
// for streams Pho code can read from (a child's stdout/stderr, the host's
// own stdin, an opened file); Writer for streams it can write to (a
// child's stdin, the host's own stdout/stderr, a file). closer is the
// owned fd to release on cleanup (a pipe end or *os.File); it is nil for
// the host's std streams, which must never be closed.
type stream struct {
	Id     float64
	Reader *bufio.Reader
	Writer io.Writer
	closer io.Closer
}

// Reserved stream ids for the host process, which the stdlib reaches as
// pid 0 via (pctl.ThisProcess).
const (
	stdinId  float64 = 0
	stdoutId float64 = 1
	stderrId float64 = 2
)

// StdDependenciesModule returns a PhoModule wrapping the stdDependencies
// state, ready to register via Expose. The host process is preinstalled as
// pid 0 with its three std streams, so (pctl.Stdout) etc. work out of the
// box; child-process ids are allocated after the reserved ones.
func StdDependenciesModule() *PhoModule {
	state := &stdDependencies{
		activeProcesses: map[float64]*process{
			0: {In: stdinId, Out: stdoutId, Err: stderrId},
		},
		activeStreams: map[float64]*stream{
			stdinId:  {Id: stdinId, Reader: bufio.NewReader(os.Stdin)},
			stdoutId: {Id: stdoutId, Writer: os.Stdout},
			stderrId: {Id: stderrId, Writer: os.Stderr},
		},
		lastId: stderrId,
	}

	return &PhoModule{
		Name:     "stdDependencies",
		Children: nil,
		Data:     state,
	}
}
