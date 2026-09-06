package sidecar

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Frame types on the sidecar socket. The host sends execute; the child
// answers result or error. Anything else from the child is a host call, which
// this layer denies rather than interprets.
const (
	frameExecute = "execute"
	frameResult  = "result"
	frameError   = "error"
)

// Item is one workflow item on the wire. The shape matches the Code node's
// ({json: ...}) on purpose: the engine already speaks this, so the sidecar
// executor marshals what it was given instead of inventing a second item.
type Item struct {
	JSON map[string]any `json:"json"`
}

type executeFrame struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Tenant  string            `json:"tenant"`
	Node    string            `json:"node"`
	Params  map[string]any    `json:"params,omitempty"`
	Items   []Item            `json:"items"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

type terminalFrame struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Items   []Item `json:"items,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// writeFrame sends one NDJSON frame. The newline is the framing: a child that
// logs to the socket instead of its stdout desynchronises the stream, which
// is why stdout is never the socket.
func writeFrame(writer io.Writer, frame any) error {
	line, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode sidecar frame: %w", err)
	}
	line = append(line, '\n')
	if _, err := writer.Write(line); err != nil {
		return fmt.Errorf("send sidecar frame: %w", err)
	}
	return nil
}

// frameReader reads one NDJSON line at a time with a hard cap, so a child
// that dumps megabytes onto the socket is refused instead of buffered.
type frameReader struct {
	reader *bufio.Reader
	limit  int
}

func newFrameReader(stream io.Reader, limit int) *frameReader {
	return &frameReader{reader: bufio.NewReader(stream), limit: limit}
}

// next returns one raw frame line without its newline. A line past the limit
// is an error, not a larger allocation.
func (fr *frameReader) next() ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := fr.reader.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(line)+len(chunk) > fr.limit {
			return nil, &CallError{Code: CodeFrameTooLarge, Detail: "sidecar sent a message past the frame limit"}
		}
		line = append(line, chunk...)
		if !isPrefix {
			return line, nil
		}
	}
}

func decodeTerminal(line []byte) (terminalFrame, error) {
	var frame terminalFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		return frame, &CallError{Code: CodeProtocolViolation, Detail: "sidecar answered with a message that is not a result or an error"}
	}
	if frame.Type != frameResult && frame.Type != frameError {
		return frame, &CallError{Code: CodeHostCallDenied, Detail: fmt.Sprintf("sidecar asked the host for %q, which this deployment does not serve", frame.Type)}
	}
	return frame, nil
}
