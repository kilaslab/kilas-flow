package cli

import (
	"strings"
	"testing"
)

func TestReadStreamParsesFrames(t *testing.T) {
	frames := "retry: 2000\n" +
		": connected\n" +
		"\n" +
		"id: 1\n" +
		"event: execution.started\n" +
		"data: {\"id\":1,\"type\":\"execution.started\"}\n" +
		"\n" +
		"id: 2\n" +
		"event: node.output\n" +
		"data: {\"id\":2,\n" +
		"data: \"type\":\"node.output\"}\n" +
		"\n" +
		"id: 3\n" +
		"event: execution.completed\n" +
		"data: {\"id\":3,\"type\":\"execution.completed\"}\n" +
		"\n" +
		"id: 4\n" +
		"event: node.started\n" +
		"data: {\"id\":4}\n" +
		"\n"

	var got []streamEvent
	terminal, err := readStream(strings.NewReader(frames), func(event streamEvent) bool {
		got = append(got, event)

		return true
	})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if !terminal {
		t.Fatal("readStream did not report the terminal event")
	}

	// The stream stops at the terminal frame: what the server sent after it is
	// not part of this run's outcome.
	if len(got) != 3 {
		t.Fatalf("events = %+v, want the three before the terminal frame", got)
	}
	if got[0].Name != "execution.started" || got[0].ID != 1 {
		t.Errorf("first = %+v, want execution.started with id 1", got[0])
	}
	// Two data lines are one frame's payload, joined by a newline, which is
	// what the event-stream format says they mean.
	if string(got[1].Data) != "{\"id\":2,\n\"type\":\"node.output\"}" {
		t.Errorf("second data = %q, want the joined payload", got[1].Data)
	}
	if got[2].Name != "execution.completed" {
		t.Errorf("third = %+v, want the terminal event", got[2])
	}
}

func TestReadStreamIgnoresFramesWithoutData(t *testing.T) {
	// A frame carrying only an id or an event name is not an event: the
	// event-stream format dispatches on the data field.
	frames := "id: 7\nevent: node.started\n\n" +
		": heartbeat\n\n" +
		"id: 8\nevent: execution.completed\ndata: {\"id\":8}\n\n"

	var got []streamEvent
	terminal, err := readStream(strings.NewReader(frames), func(event streamEvent) bool {
		got = append(got, event)

		return true
	})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if !terminal || len(got) != 1 {
		t.Fatalf("events = %+v (terminal=%v), want only the frame with data", got, terminal)
	}
	if got[0].ID != 8 {
		t.Fatalf("id = %d, want 8", got[0].ID)
	}
}

func TestReadStreamHandlesCarriageReturnsAndUnknownFields(t *testing.T) {
	frames := "retry: 500\r\nunknown: value\r\nevent: node.started\r\ndata: {\"id\":1}\r\n\r\n"

	var got []streamEvent
	if _, err := readStream(strings.NewReader(frames), func(event streamEvent) bool {
		got = append(got, event)

		return true
	}); err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if len(got) != 1 || got[0].Name != "node.started" {
		t.Fatalf("events = %+v, want the one frame", got)
	}
}

func TestReadStreamStopsWhenTheConsumerSaysSo(t *testing.T) {
	frames := "id: 1\nevent: node.started\ndata: {}\n\nid: 2\nevent: node.completed\ndata: {}\n\n"

	var got []streamEvent
	terminal, err := readStream(strings.NewReader(frames), func(event streamEvent) bool {
		got = append(got, event)

		return false
	})
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if terminal || len(got) != 1 {
		t.Fatalf("events = %+v (terminal=%v), want the read to stop after the first", got, terminal)
	}
}

func TestReadStreamCarriesANonJSONPayloadAsText(t *testing.T) {
	// The envelope has to stay valid JSON whatever the server sends, and a
	// frame whose payload is not JSON is still worth reporting.
	frames := "event: node.output\ndata: not json at all\n\n"

	var got []streamEvent
	if _, err := readStream(strings.NewReader(frames), func(event streamEvent) bool {
		got = append(got, event)

		return true
	}); err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if len(got) != 1 || string(got[0].Data) != `"not json at all"` {
		t.Fatalf("data = %q, want the payload as a JSON string", got[0].Data)
	}
}

func TestReadStreamReportsAReadError(t *testing.T) {
	_, err := readStream(errorReader{}, func(streamEvent) bool { return true })
	if err == nil {
		t.Fatal("readStream swallowed a read error")
	}
}

// errorReader fails every read, which is what a cancelled request looks like.
type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errStreamBroken }

// errStreamBroken is the read failure the test above expects.
var errStreamBroken = &ExitError{Code: ExitFailure, ErrCode: "network_error", Message: "the stream was cut"}
