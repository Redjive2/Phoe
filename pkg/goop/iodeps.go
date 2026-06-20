package goop

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// readableStream resolves a stream id and checks it can be read from.
// Failures print an interpreter error and return ok=false.
func (state *stdDependencies) readableStream(streamId float64, caller string) (*stream, bool) {
	s, found := state.getStream(streamId)
	if !found {
		hostErr("%s: unknown stream id %v", caller, streamId)
		return nil, false
	}
	if s.Reader == nil {
		streamName := fmt.Sprint(streamId)

		switch streamId {
		case 0:
			streamName += " (Stdin)"
		case 1:
			streamName += " (Stdout)"
		case 2:
			streamName += " (Stderr)"
		}

		hostErr("%s: stream '%s' is not readable", caller, streamName)
		return nil, false
	}
	return s, true
}

// writableStream is readableStream's write-side twin.
func (state *stdDependencies) writableStream(streamId float64, caller string) (*stream, bool) {
	s, found := state.getStream(streamId)
	if !found {
		hostErr("%s: unknown stream id %v", caller, streamId)
		return nil, false
	}
	if s.Writer == nil {
		hostErr("%s: stream %v is not writable", caller, streamId)
		return nil, false
	}
	return s, true
}

// IoRead reads up to n bytes from the stream and returns what was actually
// read. EOF before n bytes just shortens the result — no panic, no error.
func (state *stdDependencies) IoRead(streamId float64, nf float64) string {
	stream, ok := state.readableStream(streamId, "IoRead")
	if !ok || int(nf) <= 0 {
		return ""
	}

	p := make([]byte, int(nf))
	n, _ := io.ReadFull(stream.Reader, p)
	return string(p[:n])
}

// IoReadLine writes the prompt to the host's stdout (if non-empty), then
// reads one line from the stream. The line terminator is not included; at
// EOF whatever was read so far is returned.
func (state *stdDependencies) IoReadLine(streamId float64, prompt string) string {
	stream, ok := state.readableStream(streamId, "IoReadLine")
	if !ok {
		return ""
	}

	if prompt != "" {
		fmt.Print(prompt)
	}

	line, _ := stream.Reader.ReadString('\n')
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

// IoReadUntil reads from the stream until the full stop delimiter is seen
// (or EOF). The delimiter is not included in the result. stop may be more
// than one byte: bufio.ReadString only matches a single byte, so we scan
// byte-by-byte and compare the accumulated tail against the whole
// delimiter (the bufio.Reader still buffers underneath, so this isn't a
// syscall per byte).
func (state *stdDependencies) IoReadUntil(streamId float64, stop string) string {
	stream, ok := state.readableStream(streamId, "IoReadUntil")
	if !ok {
		return ""
	}
	if stop == "" {
		hostErr("IoReadUntil: empty delimiter")
		return ""
	}

	var buf []byte
	for {
		c, err := stream.Reader.ReadByte()
		if err != nil {
			return string(buf) // EOF or read error: return what we have
		}
		buf = append(buf, c)
		if len(buf) >= len(stop) && string(buf[len(buf)-len(stop):]) == stop {
			return string(buf[:len(buf)-len(stop)])
		}
	}
}

// IoWrite writes content to the stream.
func (state *stdDependencies) IoWrite(streamId float64, content string) {
	stream, ok := state.writableStream(streamId, "IoWrite")
	if !ok {
		return
	}

	if _, err := stream.Writer.Write([]byte(content)); err != nil {
		hostErr("IoWrite: write to stream %v failed: %v", streamId, err)
	}
}

// IoNewPipe registers an in-memory pipe as a single stream that is both
// readable and writable, returning its id (for fan-in / fan-out wiring).
func (state *stdDependencies) IoNewPipe() float64 {
	pr, pw := io.Pipe()

	s := &stream{
		Id:     state.nextId(),
		Reader: bufio.NewReader(pr),
		Writer: pw,
		closer: pw, // closing the writer signals EOF to readers
	}
	state.putStream(s)

	return s.Id
}
