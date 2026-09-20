package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// maxStreamLine bounds one line of an event stream. The server sends one event
// per line, and an event's payload is a redacted node output: a megabyte is far
// more than any real one, and a bound is what stops a broken stream from
// growing a buffer until the process dies.
const maxStreamLine = 1 << 20

// terminalEvents are the event names that end a run's feed. The server closes
// the stream after sending one, so a client that stops here does not depend on
// the close arriving.
var terminalEvents = map[string]bool{
	"execution.completed": true,
	"execution.failed":    true,
	"execution.cancelled": true,
}

// streamEvent is one frame of the execution event feed, in the shape the
// documented exceptions emit: an id to resume from, the event's name, and its
// payload as the server sent it.
type streamEvent struct {
	ID   uint64          `json:"id"`
	Name string          `json:"event"`
	Data json.RawMessage `json:"data,omitempty"`
}

// readStream reads an event stream and hands each frame to emit, which returns
// false to stop.
//
// It reports whether a terminal event was seen. The format is the one every
// event stream uses: `field: value` lines, a blank line ending a frame, `:`
// lines are comments (the server opens with one), and two `data:` lines in one
// frame are joined by a newline. A frame with no `data:` field is not an event.
func readStream(body io.Reader, emit func(streamEvent) bool) (bool, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLine)

	var (
		frameID   uint64
		frameName string
		payload   strings.Builder
		hasData   bool
	)

	dispatch := func() (bool, bool) {
		if !hasData {
			return false, true
		}

		event := streamEvent{ID: frameID, Name: frameName, Data: eventPayload(payload.String())}
		frameID, frameName, hasData = 0, "", false
		payload.Reset()

		if !emit(event) {
			return false, false
		}

		return terminalEvents[event.Name], true
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if terminal, keepGoing := dispatch(); terminal || !keepGoing {
				return terminal, nil
			}

			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			// A line with no colon is a field with an empty value, which the
			// format says to ignore unless it is one of the three below.
			continue
		}
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "event":
			frameName = value
		case "data":
			if hasData {
				payload.WriteString("\n")
			}
			payload.WriteString(value)
			hasData = true
		case "id":
			// An id the parser cannot read is ignored rather than fatal: the
			// frame is still an event, and resuming from the wrong id is a
			// request the caller can make again.
			if parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
				frameID = parsed
			}
		}
	}

	// A stream that ends without its final blank line still ends a frame.
	if terminal, keepGoing := dispatch(); terminal || !keepGoing {
		return terminal, nil
	}

	return false, scanner.Err()
}

// eventPayload keeps a frame's payload as JSON, falling back to a JSON string
// so the envelope that carries it stays valid whatever the server sent.
func eventPayload(data string) json.RawMessage {
	if json.Valid([]byte(data)) {
		return json.RawMessage(data)
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return json.RawMessage(`""`)
	}

	return encoded
}

// stream performs a streaming request and hands each frame to emit.
//
// The Accept header is what makes this an event stream rather than a buffered
// response, and it is the reason the client has a second entry point at all.
func (ctx *Context) stream(path string, query url.Values, header http.Header, emit func(streamEvent) bool) (bool, error) {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Accept", "text/event-stream")

	resp, err := ctx.Client.Stream(ctx.Ctx, http.MethodGet, apiPath(path), query, header)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	return readStream(resp.Body, emit)
}
