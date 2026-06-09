package goop

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"pho/pkg/core"
)

var currentId float64 = -1
func nextId() float64 {
	currentId++
	return currentId
}

func (stdDependencies) PctlQuit(code int, message string) {
	if code != 0 {
		_ = fmt.Errorf("%s", message)
	} else {
		fmt.Println(message)
	}

	os.Exit(code)
}

func (state *stdDependencies) PctlWire(sourceStream, targetStream any) {
	fmt.Println("pctl.Wire", sourceStream, targetStream)
}

func (state *stdDependencies) PctlCancel(pid float64) {
	state.activeProcesses[pid].CancelHook()
}

func (state *stdDependencies) PctlStdoutId(pid float64) float64 {
	return state.activeProcesses[pid].Out
}

func (state *stdDependencies) PctlStdinId(pid float64) float64 {
	return state.activeProcesses[pid].In
}

func (state *stdDependencies) PctlStderrId(pid float64) float64 {
	return state.activeProcesses[pid].Err
}

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

		bReadIn = bufio.NewReader(readIn)
		bReadOut = bufio.NewReader(readOut)
		bReadErr = bufio.NewReader(readErr)

		inId = nextId()
		outId = nextId()
		errId = nextId()

		in = stream{inId, bReadIn, writeIn}
		out = stream{outId, bReadOut, writeOut}
		err = stream{errId, bReadErr, writeErr}
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