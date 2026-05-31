package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// LSP wire format is JSON-RPC 2.0 framed by Content-Length headers, sent
// over stdin/stdout. Each message is:
//
//     Content-Length: <N>\r\n
//     \r\n
//     <N bytes of JSON>
//
// We don't pull in a third-party JSON-RPC library — the protocol is
// small enough that ~100 lines covers it.

// rawMessage is the union of JSON-RPC request, response, and
// notification. Different fields are populated depending on the kind;
// the dispatcher figures it out.
type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`     // present on requests/responses
	Method  string          `json:"method,omitempty"` // present on requests/notifications
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// transport reads/writes JSON-RPC messages on a pair of streams.
// Writes are serialized through writeMu so notifications fired from
// goroutines don't interleave bytes mid-message.
type transport struct {
	in      *bufio.Reader
	out     io.Writer
	writeMu sync.Mutex
}

func newTransport(in io.Reader, out io.Writer) *transport {
	return &transport{in: bufio.NewReader(in), out: out}
}

// readMessage parses one Content-Length-framed message. Returns
// io.EOF cleanly when the peer closes; any other error indicates a
// protocol violation.
func (t *transport) readMessage() (*rawMessage, error) {
	contentLength := -1
	for {
		line, err := t.in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // header/body separator
		}
		const cl = "Content-Length:"
		if strings.HasPrefix(line, cl) {
			n, err := strconv.Atoi(strings.TrimSpace(line[len(cl):]))
			if err != nil {
				return nil, fmt.Errorf("malformed Content-Length: %q", line)
			}
			contentLength = n
		}
		// Other headers (Content-Type, etc.) are ignored.
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(t.in, buf); err != nil {
		return nil, err
	}

	var msg rawMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w (body: %q)", err, buf)
	}
	return &msg, nil
}

// writeMessage frames and sends a JSON-RPC message. Caller is
// responsible for setting JSONRPC = "2.0" on the value being marshaled.
func (t *transport) writeMessage(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if _, err := fmt.Fprintf(t.out, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = t.out.Write(body)
	return err
}

// notify sends a notification (no id, no response expected).
func (t *transport) notify(method string, params any) error {
	pBytes, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return t.writeMessage(struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  pBytes,
	})
}

// reply sends a successful response to a request.
func (t *transport) reply(id json.RawMessage, result any) error {
	rBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return t.writeMessage(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Result:  rBytes,
	})
}

// replyError sends an error response to a request. Used when a handler
// panics so the client gets an answer instead of waiting forever.
// The error code -32603 is the JSON-RPC "internal error" sentinel.
func (t *transport) replyError(id json.RawMessage, message string) error {
	return t.writeMessage(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Error: struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32603, Message: message},
	})
}
