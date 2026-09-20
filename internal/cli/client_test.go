package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testToken = "kfa1_live_abcdef_secretvalue"

func TestClientSendsTheTokenAndReadsTheResponse(t *testing.T) {
	var (
		gotAuth   string
		gotAccept string
	)

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotAccept = r.Header.Get("Accept")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(healthBody))
		},
	})

	client := &Client{BaseURL: srv.URL, Token: testToken, HTTP: &http.Client{Timeout: time.Second}, Now: time.Now}
	resp, err := client.Do(context.Background(), http.MethodGet, apiPrefix+"/health", nil, nil, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if string(resp.Body) != healthBody {
		t.Fatalf("body = %q, want %q", resp.Body, healthBody)
	}
	if resp.ContentType != "application/json" {
		t.Fatalf("content type = %q", resp.ContentType)
	}
	if gotAuth != "Bearer "+testToken {
		t.Fatalf("Authorization = %q, want the bearer token", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q", gotAccept)
	}
}

func TestClientSendsQueryAndBody(t *testing.T) {
	var (
		gotQuery string
		gotBody  string
	)

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows": func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(r.Body)
			gotBody = buf.String()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"wf_1"}`))
		},
	})

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Now: time.Now}
	if _, err := client.Do(context.Background(), http.MethodPost, apiPrefix+"/workflows",
		url.Values{"limit": {"5"}}, nil, []byte(`{"name":"demo"}`)); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotQuery != "limit=5" {
		t.Fatalf("query = %q, want limit=5", gotQuery)
	}
	if gotBody != `{"name":"demo"}` {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestCallerContentTypeReplacesTheJSONDefault(t *testing.T) {
	var (
		gotContentType []string
		gotQueryValues []string
	)

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/datastores/ds_1/rows": func(w http.ResponseWriter, r *http.Request) {
			// Values, not Get: a header with the default still beside the
			// caller's value answers application/json to every server that
			// reads the first one, which is the defect.
			gotContentType = r.Header.Values("Content-Type")
			gotQueryValues = r.Header.Values("X-Tenant")
			w.WriteHeader(http.StatusNoContent)
		},
	})

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Now: time.Now}
	header := http.Header{"Content-Type": {"text/csv"}, "X-Tenant": {"operator", "second"}}
	if _, err := client.Do(context.Background(), http.MethodPost, apiPrefix+"/datastores/ds_1/rows",
		nil, header, []byte("id,name\n1,one\n")); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if len(gotContentType) != 1 || gotContentType[0] != "text/csv" {
		t.Fatalf("Content-Type values = %q, want exactly text/csv", gotContentType)
	}
	// A header the client has no default for is still added value by value.
	if len(gotQueryValues) != 2 || gotQueryValues[0] != "operator" {
		t.Fatalf("X-Tenant values = %q, want both values in order", gotQueryValues)
	}
}

func TestClientTurnsAProblemDocumentIntoAnExitError(t *testing.T) {
	const problem = `{"title":"Conflict","status":409,"detail":"version mismatch"}`

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1": problemBody(http.StatusConflict, problem),
	})

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Now: time.Now}
	_, err := client.Do(context.Background(), http.MethodPut, apiPrefix+"/workflows/wf_1", nil, nil, []byte(`{}`))

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want an ExitError", err)
	}
	if exitErr.Code != ExitConflict || exitErr.ErrCode != "conflict" || exitErr.Status != http.StatusConflict {
		t.Fatalf("ExitError = %+v, want a 409 conflict", exitErr)
	}
	if !strings.Contains(exitErr.Message, "version mismatch") {
		t.Fatalf("message = %q, want the problem's detail", exitErr.Message)
	}

	var want, got any
	if err := json.Unmarshal([]byte(problem), &want); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if err := json.Unmarshal(exitErr.Problem, &got); err != nil {
		t.Fatalf("problem is not JSON: %v", err)
	}
	if !jsonEqual(t, want, got) {
		t.Fatalf("problem = %s, want the document verbatim", exitErr.Problem)
	}
}

func TestClientTruncatesANonJSONErrorBody(t *testing.T) {
	long := strings.Repeat("x", 5000)

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(long))
		},
	})

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Now: time.Now}
	_, err := client.Do(context.Background(), http.MethodGet, apiPrefix+"/health", nil, nil, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want an ExitError", err)
	}
	if exitErr.Code != ExitFailure || exitErr.ErrCode != "server_error" {
		t.Fatalf("ExitError = %+v, want a server error", exitErr)
	}
	if len(exitErr.Problem) != 0 {
		t.Fatalf("a non-JSON body was parsed as a problem document: %s", exitErr.Problem)
	}
	if len(exitErr.Body) != maxErrorBodyBytes {
		t.Fatalf("body is %d bytes, want it truncated to %d", len(exitErr.Body), maxErrorBodyBytes)
	}
	if !strings.Contains(exitErr.Message, "HTTP 500") {
		t.Fatalf("message = %q, want the method, path and status", exitErr.Message)
	}
}

func TestClientReportsATransportFailureAsANetworkError(t *testing.T) {
	client := &Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}, Now: time.Now}
	_, err := client.Do(context.Background(), http.MethodGet, apiPrefix+"/health", nil, nil, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want an ExitError", err)
	}
	if exitErr.Code != ExitFailure || exitErr.ErrCode != "network_error" {
		t.Fatalf("ExitError = %+v, want a network error", exitErr)
	}
	if exitErr.Message == "" {
		t.Fatal("a transport failure produced an empty message")
	}
}

func TestRedactJSONHidesCredentialFields(t *testing.T) {
	in := []byte(`{"token":"abc","nested":{"password":"p","ok":"1"},"list":[{"secret":"s"}],"safe":"v","API_KEY":"k"}`)

	out := redactJSON(in)

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("redacted output is not JSON: %v (%s)", err, out)
	}
	for _, key := range []string{"token", "API_KEY"} {
		if doc[key] != "[redacted]" {
			t.Fatalf("%s = %v, want [redacted]", key, doc[key])
		}
	}
	if doc["safe"] != "v" {
		t.Fatalf("safe = %v, want it untouched", doc["safe"])
	}
	nested, _ := doc["nested"].(map[string]any)
	if nested["password"] != "[redacted]" || nested["ok"] != "1" {
		t.Fatalf("nested = %v", nested)
	}
	list, _ := doc["list"].([]any)
	first, _ := list[0].(map[string]any)
	if first["secret"] != "[redacted]" {
		t.Fatalf("list = %v", list)
	}

	plain := []byte("not json at all")
	if got := redactJSON(plain); string(got) != string(plain) {
		t.Fatalf("redactJSON changed a non-JSON body: %q", got)
	}
}

func TestTokenNeverAppearsInVerboseOutput(t *testing.T) {
	var verbose bytes.Buffer
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": jsonBody(http.StatusOK, healthBody),
	})

	client := &Client{BaseURL: srv.URL, Token: testToken, HTTP: srv.Client(), Now: time.Now, Verbose: &verbose}
	if _, err := client.Do(context.Background(), http.MethodGet, apiPrefix+"/health",
		url.Values{"token": {testToken}}, nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if !strings.Contains(verbose.String(), "> GET ") {
		t.Fatalf("verbose output is missing the request line: %q", verbose.String())
	}
	if strings.Contains(verbose.String(), testToken) {
		t.Fatalf("verbose output leaked the token: %q", verbose.String())
	}
	if !strings.Contains(verbose.String(), "[redacted]") {
		t.Fatalf("verbose output did not redact the query value: %q", verbose.String())
	}
}

func TestTokenNeverAppearsInAnErrorMessage(t *testing.T) {
	client := &Client{BaseURL: "http://127.0.0.1:1", Token: testToken, HTTP: &http.Client{Timeout: time.Second}, Now: time.Now}

	_, err := client.Do(context.Background(), http.MethodGet,
		apiPrefix+"/health", url.Values{"api_key": {testToken}}, nil, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want an ExitError", err)
	}
	if strings.Contains(exitErr.Message, testToken) {
		t.Fatalf("the transport error leaked the token: %q", exitErr.Message)
	}

	// A token embedded in a URL that arrives as text is redacted too, which is
	// how a token in a query string reaches an error message.
	if got := client.redactText(`Get "http://h/api/v1/x?token=` + testToken + `": connection refused`); strings.Contains(got, testToken) {
		t.Fatalf("redactText leaked the token: %q", got)
	}
}

func TestTimeoutFlagBoundsTheClient(t *testing.T) {
	client := newClient(&GlobalFlags{Timeout: 1500 * time.Millisecond}, "http://example.test", testToken)
	if client.HTTP == nil {
		t.Fatal("the CLI built a client with no HTTP client")
	}
	if client.HTTP.Timeout != 1500*time.Millisecond {
		t.Fatalf("timeout = %v, want the flag's value", client.HTTP.Timeout)
	}
	if client.BaseURL != "http://example.test" || client.Token != testToken {
		t.Fatalf("client = %+v, want the resolved URL and token", client)
	}
}

func TestTimeoutIsEnforcedOnARealRequest(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusOK)
		},
	})

	started := time.Now()
	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"health", "--json", "--url", srv.URL, "--timeout", "100ms"},
		TTY:  true,
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d (stdout=%q)", code, ExitFailure, stdout)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the request ran for %v; --timeout was not applied", elapsed)
	}

	doc := envelope(t, stdout)
	errDoc, _ := doc["error"].(map[string]any)
	if errDoc["code"] != "network_error" {
		t.Fatalf("error.code = %v, want network_error", errDoc["code"])
	}
}
