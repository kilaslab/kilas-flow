// Package mcp serves the Model Context Protocol over the stdio transport.
//
// It is the protocol half of `kilasflow mcp serve` (design §6): it speaks
// JSON-RPC 2.0, the initialize handshake, tools/list and tools/call, and knows
// nothing about KilasFlow. The tool list and what a call does come from the
// Handler, which the command tree implements — that is what keeps the adapter a
// mapping onto the CLI rather than a second implementation of it.
//
// Deliberately stdlib-only. The adapter needs three methods over a newline
// framed pipe, the wire surface is already implemented in this repository
// (nodes/ai.go speaks the client half), and a protocol dependency would put a
// module graph this product does not otherwise have into the image that serves
// it. See the FEAT-yxwyav ticket for the decision and its evidence.
//
// The revisions below are the legacy (handshake) ones: 2025-11-25, 2025-06-18
// and 2025-03-26 describe tools/list and tools/call identically, so a server
// that implements the handshake implements all three. The modern revisions
// (2026-07-28 and later) carry version, identity and capabilities as per-request
// `_meta` and drop the handshake entirely; that is out of scope for this ticket,
// and a modern client probing with `server/discover` gets the method-not-found
// this server answers for every method it does not implement, which is the
// fall-back signal the specification defines.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// JSONRPCVersion is the only JSON-RPC version MCP uses.
const JSONRPCVersion = "2.0"

// SupportedVersions are the protocol revisions this server speaks, newest
// first. A client asking for one of them has its own version echoed back, which
// is what version negotiation requires; a client asking for anything else is
// answered with the newest of these.
var SupportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// JSON-RPC error codes, as the specification defines them.
const (
	// CodeParseError is a message that is not JSON at all.
	CodeParseError = -32700
	// CodeInvalidRequest is valid JSON that is not a JSON-RPC request.
	CodeInvalidRequest = -32600
	// CodeMethodNotFound is a request for a method this server does not have.
	CodeMethodNotFound = -32601
	// CodeInvalidParams is a request whose params do not fit the method — an
	// unknown tool, or arguments that do not fit the tool's schema.
	CodeInvalidParams = -32602
	// CodeInternalError is a failure in this server, not in the request.
	CodeInternalError = -32603
)

// maxMessage bounds one framed message. A client that sends more than this is
// refused rather than allowed to grow this process's heap without limit.
const maxMessage = 8 << 20

// errTooLong reports a message past maxMessage. The line is drained to its
// newline and the session continues, because one bad message must not end it.
var errTooLong = errors.New("message is too long")

// Info is what the server tells a client about itself in the initialize
// answer.
type Info struct {
	// Name is the server's programmatic name.
	Name string
	// Title is the display name, when one is nicer than Name.
	Title string
	// Version is the product version this server serves.
	Version string
	// Instructions is optional guidance the client shows the model. It is the
	// one place the adapter can say what the tools are without spending a tool
	// description on it.
	Instructions string
}

// Tool is one callable tool as tools/list reports it.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Content is one content block of a tool result. Only text is used: a tool
// result is the CLI's own JSON envelope, verbatim.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Result is what a tool call answers with.
//
// IsError marks a tool that ran and failed — a refusal, a 404, a usage error.
// It is not a protocol error: the request was well formed and the answer is the
// tool's own, which is the distinction the specification draws and the one that
// keeps an agent's error handling working.
type Result struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Text builds a single-text-block result.
func Text(text string, isError bool) Result {
	return Result{Content: []Content{{Type: "text", Text: text}}, IsError: isError}
}

// ErrUnknownTool is what a Handler returns for a call naming a tool this server
// does not publish. The server answers it as JSON-RPC invalid params, the code
// the specification names for an unknown tool.
var ErrUnknownTool = errors.New("unknown tool")

// BadParams is what a Handler returns for a call whose arguments do not fit the
// tool's schema. The server answers it as JSON-RPC invalid params.
type BadParams struct {
	// Message is the reason, shown to the caller.
	Message string
}

// Error implements error.
func (e *BadParams) Error() string { return e.Message }

// BadParamsf builds a BadParams.
func BadParamsf(format string, args ...any) error {
	return &BadParams{Message: fmt.Sprintf(format, args...)}
}

// Handler is everything the server needs from the product it adapts.
//
// Tools is called once per tools/list and must be stable: the tool list is the
// adapter's own metadata, not state. Call runs one tool; it returns an error
// only for a request the protocol must refuse (an unknown tool, arguments that
// do not fit the schema), and a Result with IsError for a tool that ran and
// failed.
type Handler interface {
	Tools() []Tool
	Call(name string, arguments map[string]any) (Result, error)
}

// Server answers MCP requests for one Handler.
type Server struct {
	info    Info
	handler Handler
}

// NewServer returns a server that answers for handler.
func NewServer(info Info, handler Handler) *Server {
	return &Server{info: info, handler: handler}
}

// Serve reads newline-delimited JSON-RPC messages from in and writes answers to
// out until in ends.
//
// Nothing but protocol messages is ever written to out: a caller that has more
// to say (a log line, a diagnostic) says it on stderr. A returned error is the
// transport's, not a tool's — a tool's failure is an answer.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReaderSize(in, 64<<10)
	writer := bufio.NewWriter(out)

	for {
		line, readErr := readLine(reader, maxMessage)

		switch {
		case readErr == nil, errors.Is(readErr, io.EOF):
			// A whole message, or the last one of a stream that ended without
			// a trailing newline.
		case errors.Is(readErr, errTooLong):
			// Answer with a parse error rather than ending the session: the
			// id is unknowable, so the answer carries none.
			if err := writeMessage(writer, failure(nil, CodeParseError, fmt.Sprintf("message exceeds %d bytes", maxMessage))); err != nil {
				return err
			}

			continue
		default:
			return readErr
		}

		if message := bytes.TrimSpace(line); len(message) > 0 {
			if answer, ok := s.handle(message); ok {
				if err := writeMessage(writer, answer); err != nil {
					return err
				}
			}
		}

		if errors.Is(readErr, io.EOF) {
			return writer.Flush()
		}
	}
}

// readLine reads one newline-delimited message, up to limit bytes.
//
// bufio.Scanner would be shorter, but a line past its buffer ends the session
// with no answer at all; this drains such a line and reports errTooLong so the
// caller can refuse one message and keep serving.
func readLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte

	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > limit {
			// Drain the rest of the line — ErrBufferFull included, which is
			// what a full buffer reports before the newline arrives — so the
			// next message read is the next line and not this one's tail. A
			// line that arrived whole needs no draining.
			for err != nil && errors.Is(err, bufio.ErrBufferFull) {
				_, err = reader.ReadSlice('\n')
			}

			return nil, errTooLong
		}

		line = append(line, chunk...)

		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return line, err
		}
	}
}

// request is one JSON-RPC message, as much of it as this server reads.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// response is one JSON-RPC answer. ID is echoed verbatim: an id may be a string,
// a number or null, and rewriting it would break the correlation the client
// depends on.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the failure half of a response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// result builds a success response.
func result(id json.RawMessage, value any) response {
	return response{JSONRPC: JSONRPCVersion, ID: id, Result: value}
}

// failure builds an error response.
func failure(id json.RawMessage, code int, message string) response {
	return response{JSONRPC: JSONRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message}}
}

// handle answers one message. ok is false for a notification, which by JSON-RPC
// is never answered.
func (s *Server) handle(message []byte) (response, bool) {
	// A JSON-RPC batch is a valid JSON document but not a valid MCP message:
	// batching was removed from the protocol, so it is refused rather than
	// half-answered. Checking the first byte keeps that answer an invalid
	// request rather than a parse error.
	if message[0] == '[' {
		return failure(nil, CodeInvalidRequest, "JSON-RPC batches are not supported"), true
	}
	if message[0] != '{' {
		return failure(nil, CodeInvalidRequest, "a JSON-RPC message must be an object"), true
	}

	var request request
	if err := json.Unmarshal(message, &request); err != nil {
		return failure(nil, CodeParseError, "parse error: "+err.Error()), true
	}

	// A message without an id is a notification: handle it, answer nothing.
	notification := request.ID == nil

	switch request.Method {
	case "initialize":
		return result(request.ID, s.initialize(request.Params)), true
	case "notifications/initialized", "notifications/cancelled":
		return response{}, false
	case "ping":
		return result(request.ID, struct{}{}), true
	case "tools/list":
		return result(request.ID, map[string]any{"tools": s.handler.Tools()}), true
	case "tools/call":
		return s.call(request), true
	default:
		if notification {
			return response{}, false
		}

		return failure(request.ID, CodeMethodNotFound, "method not found: "+request.Method), true
	}
}

// initialize answers the handshake.
func (s *Server) initialize(params json.RawMessage) map[string]any {
	var asked struct {
		ProtocolVersion string `json:"protocolVersion"`
	}

	// A malformed or absent params is not worth refusing: the answer is the
	// same, and a client that cannot parse its own request has bigger
	// problems than the version it gets back.
	_ = json.Unmarshal(params, &asked)

	info := map[string]any{"name": s.info.Name, "version": s.info.Version}
	if s.info.Title != "" {
		info["title"] = s.info.Title
	}

	answer := map[string]any{
		"protocolVersion": negotiate(asked.ProtocolVersion),
		// The tool list is fixed for the life of the process, so there is
		// nothing to announce a change of: the capability is declared, the
		// listChanged sub-capability is not.
		"capabilities": map[string]any{"tools": map[string]any{}},
		"serverInfo":   info,
	}
	if s.info.Instructions != "" {
		answer["instructions"] = s.info.Instructions
	}

	return answer
}

// negotiate answers the version to speak: the client's own when this server
// supports it, and otherwise the newest this server supports.
func negotiate(requested string) string {
	for _, version := range SupportedVersions {
		if version == requested {
			return version
		}
	}

	return SupportedVersions[0]
}

// call runs one tools/call request.
func (s *Server) call(request request) response {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(request.Params) > 0 {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return failure(request.ID, CodeInvalidParams, "invalid params: "+err.Error())
		}
	}
	if params.Name == "" {
		return failure(request.ID, CodeInvalidParams, "tools/call needs the name of a tool")
	}

	answer, err := s.invoke(params.Name, params.Arguments)
	if err != nil {
		var badParams *BadParams
		switch {
		case errors.Is(err, ErrUnknownTool):
			return failure(request.ID, CodeInvalidParams, "unknown tool: "+params.Name)
		case errors.As(err, &badParams):
			return failure(request.ID, CodeInvalidParams, badParams.Message)
		default:
			return failure(request.ID, CodeInternalError, err.Error())
		}
	}

	return result(request.ID, answer)
}

// invoke runs one tool, turning a panic into an answer.
//
// A tool is a CLI verb in this process: a panic in one would end a session that
// the client expects to keep using, and the client cannot tell a dead server
// from a slow one. The specification asks servers to validate inputs and to
// keep the connection usable; recovering here is the cheapest half of that.
func (s *Server) invoke(name string, arguments map[string]any) (answer Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			answer, err = Result{}, fmt.Errorf("the tool panicked: %v", recovered)
		}
	}()

	return s.handler.Call(name, arguments)
}

// writeMessage frames one message: JSON on one line, with the newline the
// transport delimits on. Marshal escapes any newline inside a string, so a
// message can never contain one.
func writeMessage(writer *bufio.Writer, message response) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(message); err != nil {
		return err
	}

	return writer.Flush()
}
