package sidecar

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Frame types on the sidecar socket. The host sends execute or describe; the
// child answers result or error. Every other child-to-host frame is a host
// call, and the only host call this layer serves is an outbound HTTP request
// routed back through the host's egress policy. Anything else is denied
// rather than interpreted.
const (
	frameExecute      = "execute"
	frameDescribe     = "describe"
	frameResult       = "result"
	frameError        = "error"
	frameHTTPRequest  = "http.request"
	frameHTTPResponse = "http.response"
	frameHTTPError    = "http.error"
)

// Limits on child-controlled strings that flow into a CallError. A child that
// reports a 4 MiB "message" must not be able to grow the host's error object
// without bound, and a 4 MiB frame type must not fill a log line.
const (
	maxChildMessage = 2 << 10
	maxFrameType    = 64
)

// Item is one workflow item on the wire. The json shape matches the Code
// node's ({json: ...}) on purpose: the engine already speaks this, so the
// sidecar executor marshals what it was given instead of inventing a second
// item. binary and pairedItem are inspected by the executor (Stage 3) and are
// never populated by the host: the host only passes through what the engine
// handed it.
type Item struct {
	JSON       map[string]any `json:"json"`
	Binary     map[string]any `json:"binary,omitempty"`
	PairedItem any            `json:"pairedItem,omitempty"`
}

// RunContext is the workflow context a node may read. Every field is optional;
// the executor fills what the engine knows and omits the rest.
type RunContext struct {
	NodeName    string  `json:"nodeName,omitempty"`
	NodeType    string  `json:"nodeType,omitempty"`
	TypeVersion float64 `json:"typeVersion,omitempty"`
	ExecutionID string  `json:"executionId,omitempty"`
	WorkflowID  string  `json:"workflowId,omitempty"`
	Mode        string  `json:"mode,omitempty"`
	Timezone    string  `json:"timezone,omitempty"`
}

type executeFrame struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Tenant  string            `json:"tenant"`
	Node    string            `json:"node"`
	Params  map[string]any    `json:"params,omitempty"`
	Items   []Item            `json:"items"`
	Secrets map[string]string `json:"secrets,omitempty"`
	// NodeVersion is the node type version the run targets, so a package that
	// ships the same node name twice can dispatch on (name, version).
	NodeVersion float64 `json:"nodeVersion,omitempty"`
	// ParamsByItem carries per-item resolved parameters for onError modes
	// other than stop, where the engine runs the node once per item.
	ParamsByItem []map[string]any `json:"paramsByItem,omitempty"`
	// Credentials maps a credential type to its decrypted fields. Secrets is
	// the legacy single-map form and is still accepted.
	Credentials map[string]map[string]string `json:"credentials,omitempty"`
	Context     *RunContext                  `json:"context,omitempty"`
}

type terminalFrame struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Items []Item `json:"items,omitempty"`
	// Outputs carries one item slice per declared output port. When the child
	// answers with the legacy Items shape, Outputs is [Items].
	Outputs [][]Item `json:"outputs,omitempty"`
	// Catalogue answers a describe frame with the loaded package catalogue.
	Catalogue json.RawMessage `json:"catalogue,omitempty"`
	Code      string          `json:"code,omitempty"`
	Message   string          `json:"message,omitempty"`
}

type describeFrame struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// HTTPRequest is one outbound request a node asked the host to make. The host
// is the only party that owns the egress policy, so a community node never
// opens a socket itself.
type HTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"-"`
	Timeout time.Duration     `json:"-"`
}

// HTTPResponse is the host's answer to an HTTPRequest.
type HTTPResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"-"`
}

// HostHandler serves host calls made by a sidecar run. A nil handler on a
// Request denies every host call: the boundary refuses rather than silently
// widening.
type HostHandler interface {
	HTTP(ctx context.Context, req HTTPRequest) (HTTPResponse, error)
}

// HostCallCoder is a HostHandler error that names its own failure.
//
// The protocol carries a code beside the message on an http.error frame, and
// the child's helper raises an error carrying it, so a package can tell "too
// large" from "not allowed" instead of pattern-matching a sentence. A handler
// that does not implement this gets the generic http-error code.
type HostCallCoder interface {
	HostCallCode() (code, message string)
}

// The host-call wire frames. Request carries the run id so a response can be
// paired even when a package fires calls concurrently (they are served one at
// a time, in arrival order).
type httpRequestFrame struct {
	Type      string            `json:"type"`
	ID        string            `json:"id"`
	Call      string            `json:"call"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body64    string            `json:"bodyBase64,omitempty"`
	TimeoutMs int               `json:"timeoutMs,omitempty"`
}

type httpResponseFrame struct {
	Type       string            `json:"type"`
	ID         string            `json:"id"`
	Call       string            `json:"call"`
	Status     int               `json:"status"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body64     string            `json:"bodyBase64,omitempty"`
}

type httpErrorFrame struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Call    string `json:"call"`
	Code    string `json:"code"`
	Message string `json:"message"`
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
		return frame, &CallError{Code: CodeHostCallDenied, Detail: fmt.Sprintf("sidecar asked the host for %q, which this deployment does not serve", truncate(frame.Type, maxFrameType))}
	}
	return frame, nil
}

// truncate bounds a child-controlled string at n bytes, marking that it was
// cut. It never splits a UTF-8 rune in half in a way that would make the
// result invalid more than the child's own bytes already are.
func truncate(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n] + "…(truncated)"
}
