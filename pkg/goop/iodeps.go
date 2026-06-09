package goop

import (
	"slices"
)

func (state *stdDependencies) IoRead(streamId float64, nf float64) string {
	stream, found := state.activeStreams[streamId]
	if !found {
		return ""
	}

	var (
		n = int(nf)
		p = make([]byte, n)
	)

	_, err := stream.Reader.Read(p)
	if err != nil {
		panic(err)
	}

	line := string(p)
	
	return line[:len(line)-1]
}

func (state *stdDependencies) IoReadLine(streamId float64) string {
	stream, found := state.activeStreams[streamId]
	if !found {
		return ""
	}

	var (
		bytes  []byte
		prefix = true
	)

	for prefix {
		newBytes, prefix_, err := stream.Reader.ReadLine()
		prefix = prefix_ // bruh

		bytes = slices.Concat(bytes, newBytes)

		if err != nil {
			panic(err)
		}
	}

	line := string(bytes)

	return line[:len(line)-1]
}
