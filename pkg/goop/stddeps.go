package goop

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"pho/pkg/core"
	"strconv"
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
	Reader io.Reader
	Writer io.Writer
}

var currentId float64 = -1
func nextId() float64 {
	currentId++
	return currentId
}

var reader = bufio.NewReader(os.Stdin)

func (stdDependencies) ReadLine(prompt string) string {
	fmt.Print(prompt)

	answer, err := reader.ReadString('\n')
	if err != nil {
		panic(err)
	}

	return answer[:len(answer)-1]
}

func (stdDependencies) PrintLine(data ...any) {
	fmt.Println(data...)
}

func (stdDependencies) Print(data ...any) {
	fmt.Print(data...)
}

func (stdDependencies) Sprint(data ...any) string {
	return fmt.Sprint(data...)
}

func (stdDependencies) RandomInt(min, max int) float64 {
	return float64(rand.Intn(max-min) + min)
}

func (stdDependencies) RandomFloat() float64 {
	return rand.Float64()
}

func (stdDependencies) Atoi(str string) float64 {
	n, _ := strconv.ParseFloat(str, 64)
	return n
}

func (stdDependencies) PctlQuit(code int, message string) {
	if code != 0 {
		_ = fmt.Errorf("%s", message)
	} else {
		fmt.Println(message)
	}

	os.Exit(code)
}

func (state *stdDependencies) PctlCancel(pid float64) {
	state.activeProcesses[pid].CancelHook()
}

func (state *stdDependencies) PctlSend(pid float64, target core.Tval)

func (state *stdDependencies) PctlSpawn(path string, args *[]core.Tval, config *map[core.Tval]core.Tval) float64 {
	fmt.Println(">>", path, args, config)

	strArgs := make([]string, len(*args))
	for i, arg := range *args {
		strArgs[i] = fmt.Sprint(arg.Val)
	}

	cancelCtx, cancelHook := context.WithCancel(context.Background())
	cmd := exec.CommandContext(cancelCtx, path, strArgs...)

	var (
		readIn, writeIn   = io.Pipe()
		readOut, writeOut = io.Pipe()
		readErr, writeErr = io.Pipe()

		inId = nextId()
		outId = nextId()
		errId = nextId()

		in = stream{inId, readIn, writeIn}
		out = stream{outId, readOut, writeOut}
		err = stream{errId, readErr, writeErr}
	)

	cmd.Stdin = readIn
	cmd.Stdout = writeOut
	cmd.Stderr = writeErr

	cmd.Start()

	pid := float64(cmd.Process.Pid)

	process := process{
		Command: cmd,

		CancelHook: cancelHook,

		In: inId, Out: outId, Err: errId,
	}

	state.activeProcesses[pid] = process
	state.activeStreams[inId] = in
	state.activeStreams[outId] = out
	state.activeStreams[errId] = err

	return pid
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
