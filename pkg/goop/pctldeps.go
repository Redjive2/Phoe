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

// PctlQuit ends the host process. A nonzero code routes the message to
// stderr; code 0 prints it normally.
func (*stdDependencies) PctlQuit(code float64, message string) {
	if code != 0 {
		fmt.Fprintln(os.Stderr, message)
	} else if message != "" {
		fmt.Println(message)
	}

	os.Exit(int(code))
}

// streamIdOfValue extracts a stream id from what the Pho side hands us:
// either a raw id number, or an io.Reader / io.Writer instance boxing one
// in its (private) id field. Go is the trusted host layer, so reading the
// private field directly is fine here.
func streamIdOfValue(v core.Tval) (float64, bool) {
	switch v.Kind {
	case core.KindNum:
		return v.Val.(float64), true
	case core.KindInstance:
		inst := v.Val.(*core.Instance)
		if idVal, found := inst.Fields["id"]; found {
			if id, ok := idVal.Val.(float64); ok {
				return id, true
			}
		}
	}
	return 0, false
}

// wireIds pumps everything readable from the source stream into the target
// stream on a background goroutine (think `source | target`). The stream
// pointers are resolved up front; the goroutine then touches only those,
// not the registry. NOTE: a wired stream has a single consumer — don't
// also read it directly, since a bufio.Reader isn't safe for concurrent
// use.
func (state *stdDependencies) wireIds(sourceId, targetId float64, caller string) {
	source, ok := state.readableStream(sourceId, caller)
	if !ok {
		return
	}
	target, ok := state.writableStream(targetId, caller)
	if !ok {
		return
	}

	go func() {
		if _, err := io.Copy(target.Writer, source.Reader); err != nil {
			hostErr("%s: wire from stream %v to %v broke: %v", caller, sourceId, targetId, err)
		}
	}()
}

// PctlWire connects a source stream (a Reader/Writer instance or raw id)
// to a target stream, copying until the source is exhausted.
func (state *stdDependencies) PctlWire(source, target core.Tval) {
	sourceId, ok := streamIdOfValue(source)
	if !ok {
		hostErr("PctlWire: cannot wire from a value of kind '%s' — expected a stream", source.Kind)
		return
	}
	targetId, ok := streamIdOfValue(target)
	if !ok {
		hostErr("PctlWire: cannot wire into a value of kind '%s' — expected a stream", target.Kind)
		return
	}

	state.wireIds(sourceId, targetId, "PctlWire")
}

// PctlCancel kills a spawned process via its cancel hook.
func (state *stdDependencies) PctlCancel(pid float64) {
	proc, found := state.getProcess(pid)
	if !found || proc.CancelHook == nil {
		hostErr("PctlCancel: no cancellable process with pid %v", pid)
		return
	}

	proc.CancelHook()
}

// PctlCloseStdin closes the write end of a child's stdin, so a child that
// reads to EOF (cat, sort, ...) can finish instead of blocking forever.
func (state *stdDependencies) PctlCloseStdin(pid float64) {
	proc, found := state.getProcess(pid)
	if !found {
		hostErr("PctlCloseStdin: no process with pid %v", pid)
		return
	}
	state.closeStream(proc.In)
}

// PctlClose reclaims a finished process: it closes the process's three
// streams and drops the registry entries. This is how a program bounds the
// registry — the reaper only collects the exit status, never the entries,
// so post-exit reads keep working until an explicit Close.
func (state *stdDependencies) PctlClose(pid float64) {
	proc, found := state.getProcess(pid)
	if !found {
		hostErr("PctlClose: no process with pid %v", pid)
		return
	}
	state.closeStream(proc.In)
	state.closeStream(proc.Out)
	state.closeStream(proc.Err)
	state.delProcess(pid)
}

func (state *stdDependencies) processOf(pid float64, caller string) (*process, bool) {
	proc, found := state.getProcess(pid)
	if !found {
		hostErr("%s: no process with pid %v", caller, pid)
	}
	return proc, found
}

func (state *stdDependencies) PctlStdoutId(pid float64) float64 {
	if proc, found := state.processOf(pid, "PctlStdoutId"); found {
		return proc.Out
	}
	return -1
}

func (state *stdDependencies) PctlStdinId(pid float64) float64 {
	if proc, found := state.processOf(pid, "PctlStdinId"); found {
		return proc.In
	}
	return -1
}

func (state *stdDependencies) PctlStderrId(pid float64) float64 {
	if proc, found := state.processOf(pid, "PctlStderrId"); found {
		return proc.Err
	}
	return -1
}

// PctlSpawn starts a child process and registers three streams for it: its
// stdin (writable) and its stdout/stderr (readable). Returns the pid, or
// -1 if the process could not be started.
//
// The child's three std fds are wired to pipes WE own (os.Pipe), not the
// cmd.StdinPipe/StdoutPipe helpers: those are closed by cmd.Wait, which
// would race with — and truncate — Pho's incremental reads. With pipes we
// own, the reaper goroutine's Wait only collects the exit status and never
// touches our read ends.
//
// config supports the keys "stdout", "stderr" and "stdin", each naming a
// stream to wire the child's stream to/from — e.g. { 'stdout (pctl.Stdout) }
// forwards the child's output to the host's.
func (state *stdDependencies) PctlSpawn(path string, args *[]core.Tval, config *map[core.Tval]core.Tval) float64 {
	var strArgs []string
	if args != nil {
		for _, arg := range *args {
			strArgs = append(strArgs, core.Stringify(arg))
		}
	}

	cancelCtx, cancelHook := context.WithCancel(context.Background())
	cmd := exec.CommandContext(cancelCtx, path, strArgs...)

	inR, inW, err1 := os.Pipe()
	outR, outW, err2 := os.Pipe()
	errR, errW, err3 := os.Pipe()
	if err1 != nil || err2 != nil || err3 != nil {
		closeAll(inR, inW, outR, outW, errR, errW)
		hostErr("PctlSpawn: cannot create pipes for '%s'", path)
		cancelHook()
		return -1
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW

	if err := cmd.Start(); err != nil {
		closeAll(inR, inW, outR, outW, errR, errW)
		hostErr("PctlSpawn: failed to spawn '%s': %v", path, err)
		cancelHook()
		return -1
	}

	// The child has its own dup of each end it was handed; close the
	// parent's copies. Then closing inW signals EOF on the child's stdin,
	// and the child exiting closes its stdout/stderr write ends so our
	// reads on outR/errR see EOF.
	closeAll(inR, outW, errW)

	in := &stream{Id: state.nextId(), Writer: inW, closer: inW}
	out := &stream{Id: state.nextId(), Reader: bufio.NewReader(outR), closer: outR}
	er := &stream{Id: state.nextId(), Reader: bufio.NewReader(errR), closer: errR}

	pid := float64(cmd.Process.Pid)
	state.putProcess(pid, &process{
		Command:    cmd,
		CancelHook: cancelHook,
		In:         in.Id, Out: out.Id, Err: er.Id,
	})
	state.putStream(in)
	state.putStream(out)
	state.putStream(er)

	// Reap on exit so the child doesn't zombie, and release the cancel
	// context. The map entries are deliberately NOT removed here: reads
	// resolve a stream by id, and the idiomatic `(p.Stdout).ReadLine`
	// resolves that id from the pid *after* the child has already finished,
	// so the process and its streams must stay queryable and drainable
	// post-exit. Programs reclaim explicitly with (p.Close).
	go func() {
		_ = cmd.Wait()
		cancelHook()
	}()

	if config != nil {
		for key, val := range *config {
			name, _ := key.Val.(string)
			id, ok := streamIdOfValue(val)
			if !ok {
				hostErr("PctlSpawn: config key '%s' must name a stream, got kind '%s'", name, val.Kind)
				continue
			}

			switch name {
			case "stdout":
				state.wireIds(out.Id, id, "PctlSpawn")
			case "stderr":
				state.wireIds(er.Id, id, "PctlSpawn")
			case "stdin":
				state.wireIds(id, in.Id, "PctlSpawn")
			default:
				hostErr("PctlSpawn: unknown spawn config key '%s' (expected 'stdout, 'stderr or 'stdin)", name)
			}
		}
	}

	return pid
}

// closeAll closes every non-nil closer, ignoring errors. Used to release
// pipe ends on a spawn failure and to drop the parent's copies after Start.
func closeAll(closers ...io.Closer) {
	for _, c := range closers {
		if c != nil {
			c.Close()
		}
	}
}
