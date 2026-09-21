package sidecar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// testHostHandler is a HostHandler that records what it was asked for and
// answers from a script.
type testHostHandler struct {
	requests []HTTPRequest
	respond  func(HTTPRequest) (HTTPResponse, error)
}

func (handler *testHostHandler) HTTP(ctx context.Context, req HTTPRequest) (HTTPResponse, error) {
	handler.requests = append(handler.requests, req)
	if handler.respond == nil {
		return HTTPResponse{Status: 200, Body: []byte("ok")}, nil
	}
	return handler.respond(req)
}

func TestHostCallIsServedThroughTheHandler(t *testing.T) {
	handler := &testHostHandler{respond: func(req HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{
			Status:     201,
			StatusText: "Created",
			Headers:    map[string]string{"content-type": "application/json"},
			Body:       []byte(`{"ok":true}`),
		}, nil
	}}
	var answer httpResponseFrame
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			if _, err := readFrameInto(reader, &frame); err != nil {
				return
			}
			_ = writeFrame(child, httpRequestFrame{
				Type: frameHTTPRequest, ID: frame.ID, Call: "1", Method: "POST",
				URL:     "https://api.example.test/v1/thing",
				Headers: map[string]string{"x-test": "yes"},
				Body64:  base64.StdEncoding.EncodeToString([]byte("payload")),
			})
			line, err := reader.next()
			if err != nil {
				return
			}
			_ = json.Unmarshal(line, &answer)
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	if _, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.http", Host: handler}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(handler.requests) != 1 {
		t.Fatalf("host requests = %d, want 1", len(handler.requests))
	}
	request := handler.requests[0]
	if request.Method != "POST" || request.URL != "https://api.example.test/v1/thing" || string(request.Body) != "payload" {
		t.Errorf("host saw %+v, want the child's request", request)
	}
	if answer.Type != frameHTTPResponse || answer.Status != 201 || answer.StatusText != "Created" {
		t.Errorf("child answer = %+v, want a 201 response frame", answer)
	}
	body, err := base64.StdEncoding.DecodeString(answer.Body64)
	if err != nil || string(body) != `{"ok":true}` {
		t.Errorf("child response body = %q (%v), want the handler's body", answer.Body64, err)
	}
}

// codedRefusal is a HostHandler error that names its own failure code, the way
// the egress proxy's does when a response is past the deployment's size limit.
type codedRefusal struct {
	code    string
	message string
}

func (refusal codedRefusal) Error() string { return refusal.message }

func (refusal codedRefusal) HostCallCode() (string, string) { return refusal.code, refusal.message }

// TestACodedHostCallRefusalKeepsItsCode is the protocol half of the size limit:
// a handler that knows why it refused says so on the http.error frame, so a
// package can tell "too large" from "not allowed" instead of reading the
// sentence.
func TestACodedHostCallRefusalKeepsItsCode(t *testing.T) {
	handler := &testHostHandler{respond: func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{}, codedRefusal{code: "response-too-large", message: "the response is larger than this deployment allows"}
	}}
	var answer httpErrorFrame
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			if _, err := readFrameInto(reader, &frame); err != nil {
				return
			}
			_ = writeFrame(child, httpRequestFrame{Type: frameHTTPRequest, ID: frame.ID, Call: "9", Method: "GET", URL: "https://blocked.test/"})
			line, err := reader.next()
			if err != nil {
				return
			}
			_ = json.Unmarshal(line, &answer)
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	if _, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.http", Host: handler}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if answer.Type != frameHTTPError {
		t.Fatalf("child answer = %+v, want an http.error frame", answer)
	}
	if got, want := answer.Code, "response-too-large"; got != want {
		t.Errorf("code = %q, want %q", got, want)
	}
	if !strings.Contains(answer.Message, "larger than this deployment allows") {
		t.Errorf("message = %q, want the handler's own message", answer.Message)
	}
}

func TestHostCallErrorReachesTheChildAsHTTPError(t *testing.T) {
	handler := &testHostHandler{respond: func(HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{}, errors.New("egress refused: host not allowed")
	}}
	var answer httpErrorFrame
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			if _, err := readFrameInto(reader, &frame); err != nil {
				return
			}
			_ = writeFrame(child, httpRequestFrame{Type: frameHTTPRequest, ID: frame.ID, Call: "7", Method: "GET", URL: "https://blocked.test/"})
			line, err := reader.next()
			if err != nil {
				return
			}
			_ = json.Unmarshal(line, &answer)
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	if _, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.http", Host: handler}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if answer.Type != frameHTTPError || answer.Call != "7" {
		t.Errorf("child answer = %+v, want an http.error frame", answer)
	}
	if !strings.Contains(answer.Message, "egress refused") {
		t.Errorf("message = %q, want the handler's error", answer.Message)
	}
}

func TestUnknownHostCallTypeIsDeniedEvenWithAHandler(t *testing.T) {
	handler := &testHostHandler{}
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			if _, err := readFrameInto(reader, &frame); err != nil {
				return
			}
			_ = writeFrame(child, map[string]any{"type": "dns.lookup", "id": frame.ID, "hostname": "example.test"})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.http", Host: handler})
		return err
	}())
	if err.Code != CodeHostCallDenied {
		t.Errorf("code = %q, want %q", err.Code, CodeHostCallDenied)
	}
	if len(handler.requests) != 0 {
		t.Errorf("the handler saw %d requests, want 0: only http.request is served", len(handler.requests))
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d, want 0: a refused host call is not trusted again", pool.Live())
	}
}

func TestHostCallsAreCapped(t *testing.T) {
	limits := testLimits()
	limits.MaxHostCalls = 3
	handler := &testHostHandler{}
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			if _, err := readFrameInto(reader, &frame); err != nil {
				return
			}
			for i := 0; i < 10; i++ {
				_ = writeFrame(child, httpRequestFrame{Type: frameHTTPRequest, ID: frame.ID, Call: "x", Method: "GET", URL: "https://example.test/"})
				if _, err := reader.next(); err != nil {
					return
				}
			}
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, limits)
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.http", Host: handler})
		return err
	}())
	if err.Code != CodeHostCallDenied {
		t.Errorf("code = %q, want %q", err.Code, CodeHostCallDenied)
	}
	if len(handler.requests) != limits.MaxHostCalls {
		t.Errorf("host served %d calls, want the cap of %d", len(handler.requests), limits.MaxHostCalls)
	}
}

func TestMultipleOutputsRoundTrip(t *testing.T) {
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			if _, err := readFrameInto(reader, &frame); err != nil {
				return
			}
			_ = writeFrame(child, terminalFrame{
				Type: frameResult, ID: frame.ID,
				Outputs: [][]Item{
					{{JSON: map[string]any{"port": "main"}}},
					{{JSON: map[string]any{"port": "error"}}},
				},
			})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	result, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.multi"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Outputs) != 2 {
		t.Fatalf("Outputs = %d ports, want 2", len(result.Outputs))
	}
	if result.Items[0].JSON["port"] != "main" {
		t.Errorf("Items = %+v, want the first output port", result.Items)
	}
	if result.Outputs[1][0].JSON["port"] != "error" {
		t.Errorf("Outputs[1] = %+v, want the second port", result.Outputs[1])
	}
}

func TestDiscoverReturnsTheCatalogueAndReapsTheProcess(t *testing.T) {
	reaped := false
	spawn := func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame describeFrame
			if _, err := readFrameInto(reader, &frame); err != nil {
				return
			}
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Catalogue: json.RawMessage(`{"packages":[{"name":"kf-fixture"}]}`)})
		}()
		return &Child{Conn: host, Kill: func() error {
			reaped = true
			_ = host.Close()
			_ = child.Close()
			return nil
		}}, nil
	}
	catalogue, err := Discover(context.Background(), spawn, testLimits())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if !strings.Contains(string(catalogue), "kf-fixture") {
		t.Errorf("catalogue = %s, want the child's packages", catalogue)
	}
	if !reaped {
		t.Error("Discover() did not reap the describe process")
	}
}

func TestDiscoverTimesOut(t *testing.T) {
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	spawn := func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			_, _ = reader.next() // read describe, never answer
			<-hold               // hold the connection open so the deadline is what fires
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}
	limits := testLimits()
	limits.Timeout = 100 * time.Millisecond
	_, err := Discover(context.Background(), spawn, limits)
	var callErr *CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("Discover() error = %v, want *CallError", err)
	}
	if callErr.Code != CodeSidecarTimeout {
		t.Errorf("code = %q, want %q", callErr.Code, CodeSidecarTimeout)
	}
}

// readFrameInto decodes one frame from the reader into target, so the fake
// children do not each repeat the unmarshal.
func readFrameInto(reader *frameReader, target any) ([]byte, error) {
	line, err := reader.next()
	if err != nil {
		return nil, err
	}
	return line, json.Unmarshal(line, target)
}
