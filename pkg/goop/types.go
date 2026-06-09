package goop

import (
	"bufio"
	"context"
	"io"
	"os/exec"
)

// stdDependencies is the Go-side struct that backs the `stdDependencies`
// module the Pho stdlib (std/io, std/fmt) imports via goimport.
type stdDependencies struct {
	activeProcesses map[float64]process // maps integer pid (process.Command.Process.Pid) -> process
	activeStreams   map[float64]stream  // maps integer stream.Id -> stream
}

type process struct {
	Command *exec.Cmd
	CancelHook context.CancelFunc
	In, Out, Err    float64 // <- all stream ids accessible via activeStreams
}

type stream struct {
	Id     float64
	Reader *bufio.Reader
	Writer io.Writer
}

// StdDependenciesModule returns a PhoModule wrapping the stdDependencies
// struct, ready to register via Expose.
func StdDependenciesModule() *PhoModule {
	return &PhoModule{
		Name:     "stdDependencies",
		Children: nil,
		Data:     stdDependencies{},
	}
}