package goop

import (
	"bufio"
	"os"
)

func (*stdDependencies) OsGetCwd() string {
	wd, _ := os.Getwd()
	return wd
}

func (*stdDependencies) OsChdir(newDir string) {
	if err := os.Chdir(newDir); err != nil {
		hostErr("OsChdir: %v", err)
	}
}

// OsOpen opens path with the given permission ('r | 'w | 'rw), registers
// it as a stream, and returns the stream id (or -1 on failure).
func (state *stdDependencies) OsOpen(path string, perm string) float64 {
	var flag int
	switch perm {
	case "r":
		flag = os.O_RDONLY
	case "w":
		flag = os.O_WRONLY | os.O_CREATE
	case "rw":
		flag = os.O_RDWR | os.O_CREATE
	default:
		hostErr("OsOpen: perm '%s' must be 'r, 'w, or 'rw", perm)
		return -1
	}

	file, err := os.OpenFile(path, flag, 0644)
	if err != nil {
		hostErr("OsOpen: cannot open '%s': %v", path, err)
		return -1
	}

	s := &stream{
		Id:     state.nextId(),
		Reader: bufio.NewReader(file),
		Writer: file,
		closer: file,
	}
	state.putStream(s)

	return s.Id
}

// OsClose closes an opened file's stream and releases its fd. (Cleanup
// runs through the stream's closer, so there's no separate open-file
// table to keep in sync.)
func (state *stdDependencies) OsClose(streamId float64) {
	state.closeStream(streamId)
}
