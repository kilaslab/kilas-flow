package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fixture is a Handler that answers the protocol's own edge cases: a tool that
// panics, and one that refuses its arguments.
type fixture struct{}

// Tools implements Handler.
func (fixture) Tools() []Tool {
	return []Tool{{Name: "read", Description: "read something", InputSchema: map[string]any{"type": "object"}}}
}

// Call implements Handler.
func (fixture) Call(name string, arguments map[string]any) (Result, error) {
	switch name {
	case "read":
		if _, present := arguments["nonsense"]; present {
			return Result{}, BadParamsf("read has no argument %q", "nonsense")
		}

		return Text("read ok", false), nil
	case "panic":
		panic("the fixture panicked")
	default:
		return Result{}, ErrUnknownTool
	}
}

// TestServeRefusesMalformedMessagesAndKeepsServing is the transport's contract:
// a message the server cannot read is answered with the JSON-RPC code that names
// why, and the session survives it. A server that ended on the first bad frame
// would be indistinguishable from a slow one to the client.
func TestServeRefusesMalformedMessagesAndKeepsServing(t *testing.T) {
	cases := []struct {
		name    string
		message string
		code    int
	}{
		{name: "not JSON", message: "{this is not json", code: CodeParseError},
		{name: "a batch", message: `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, code: CodeInvalidRequest},
		{name: "not an object", message: `"ping"`, code: CodeInvalidRequest},
		{name: "an unknown method", message: `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, code: CodeMethodNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answers := serve(t, tc.message, `{"jsonrpc":"2.0","id":9,"method":"ping"}`)
			if len(answers) != 2 {
				t.Fatalf("the server wrote %d messages, want two: %v", len(answers), answers)
			}

			failure, _ := answers[0].Error, answers[0].Result
			if answers[0].Error == nil {
				t.Fatalf("the bad message was answered with a result: %v", failure)
			}
			if answers[0].Error.Code != tc.code {
				t.Errorf("error.code = %d, want %d", answers[0].Error.Code, tc.code)
			}

			// The session is still usable: the ping that followed was answered.
			if answers[1].Error != nil {
				t.Fatalf("the session did not survive the bad message: %v", answers[1].Error)
			}
		})
	}
}

// TestServeAnswersAToolFailureInTheResultNotTheProtocol: an unknown tool and
// arguments that do not fit the schema are protocol errors, a tool that ran and
// failed is a result with isError, and a handler that panics is answered rather
// than allowed to end the session.
func TestServeAnswersAToolFailureInTheResultNotTheProtocol(t *testing.T) {
	answers := serve(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"nonsense":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"panic"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read"}}`,
	)
	if len(answers) != 4 {
		t.Fatalf("the server wrote %d messages, want four: %v", len(answers), answers)
	}

	for index, wanted := range []int{CodeInvalidParams, CodeInvalidParams, CodeInternalError} {
		if answers[index].Error == nil {
			t.Fatalf("message %d was answered with a result: %v", index+1, answers[index].Result)
		}
		if answers[index].Error.Code != wanted {
			t.Errorf("message %d answered code %d, want %d", index+1, answers[index].Error.Code, wanted)
		}
	}

	if answers[3].Error != nil {
		t.Fatalf("the session did not survive the panic: %v", answers[3].Error)
	}
}

// TestServeAnswersNotificationsWithNothing: a notification has no id and is
// never answered, which is what keeps a client's stream of them from being read
// as replies.
func TestServeAnswersNotificationsWithNothing(t *testing.T) {
	answers := serve(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/something/else"}`,
	)
	if len(answers) != 0 {
		t.Fatalf("the server answered %d notification(s): %v", len(answers), answers)
	}
}

// TestNegotiateEchoesASupportedVersionAndFallsBackToTheNewest: version
// negotiation is the client's version when it is one this server speaks, and the
// newest this server speaks otherwise.
func TestNegotiateEchoesASupportedVersionAndFallsBackToTheNewest(t *testing.T) {
	for _, version := range SupportedVersions {
		if got := negotiate(version); got != version {
			t.Errorf("negotiate(%q) = %q, want it echoed", version, got)
		}
	}

	// A modern revision is one this server does not speak: it answers with the
	// newest it does, and the client decides.
	if got := negotiate("2026-07-28"); got != SupportedVersions[0] {
		t.Errorf("negotiate(2026-07-28) = %q, want %q", got, SupportedVersions[0])
	}
	if got := negotiate(""); got != SupportedVersions[0] {
		t.Errorf("negotiate(\"\") = %q, want %q", got, SupportedVersions[0])
	}
}

// TestReadLineRefusesAMessagePastTheLimit: the bound is enforced by draining the
// line rather than by ending the session, so the message after it is still read.
func TestReadLineRefusesAMessagePastTheLimit(t *testing.T) {
	long := strings.Repeat("x", 64) + "\n"
	reader := bufio.NewReaderSize(strings.NewReader(long+"short\n"), 16)

	if _, err := readLine(reader, 32); !errors.Is(err, errTooLong) {
		t.Fatalf("readLine on an over-long line = %v, want errTooLong", err)
	}

	line, err := readLine(reader, 32)
	if err != nil {
		t.Fatalf("readLine after an over-long line: %v", err)
	}
	if string(line) != "short\n" {
		t.Fatalf("readLine = %q, want the line after the over-long one", line)
	}
}

// TestTextIsOneTextBlock keeps the result shape the specification asks for.
func TestTextIsOneTextBlock(t *testing.T) {
	result := Text("hello", true)
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != "hello" {
		t.Fatalf("Text() = %+v, want one text block", result)
	}
	if !result.IsError {
		t.Error("Text(…, true) did not mark the result as an error")
	}
}

// serve runs the server over the given frames and decodes what it wrote.
func serve(t *testing.T, frames ...string) []response {
	t.Helper()

	var out bytes.Buffer

	server := NewServer(Info{Name: "fixture", Version: "1"}, fixture{})
	if err := server.Serve(strings.NewReader(strings.Join(frames, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// The decoder consumes the buffer, so the written text is kept first.
	written := out.String()

	answers := make([]response, 0, len(frames))
	decoder := json.NewDecoder(strings.NewReader(written))
	for decoder.More() {
		var answer response
		if err := decoder.Decode(&answer); err != nil {
			t.Fatalf("the server wrote a frame that is not JSON: %v (%q)", err, written)
		}

		answers = append(answers, answer)
	}

	if lines := strings.Count(written, "\n"); lines != len(answers) {
		t.Fatalf("the server wrote %d lines but %d frames: %q", lines, len(answers), written)
	}

	return answers
}

// TestServeWritesOneFramePerLine is the transport's framing rule: a client reads
// messages by newline, so a message may never contain one.
func TestServeWritesOneFramePerLine(t *testing.T) {
	var out bytes.Buffer

	server := NewServer(Info{Name: "fixture", Version: "1", Instructions: "a line\nand another"}, fixture{})
	frames := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	if err := server.Serve(strings.NewReader(frames), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("the answer spans %d lines: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `\n`) {
		t.Fatalf("the newline in the instructions was not escaped: %q", lines[0])
	}
	if err := json.Unmarshal([]byte(lines[0]), &response{}); err != nil {
		t.Fatalf("the frame is not valid JSON: %v", err)
	}
}
