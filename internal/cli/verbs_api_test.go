package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// servedDocument renders a minimal `/api/openapi.json`: one operation per row,
// keyed the way huma keys it (the server-absolute path, then the method).
func servedDocument(rows ...[3]string) string {
	paths := map[string]map[string]any{}
	for _, row := range rows {
		item, present := paths[row[1]]
		if !present {
			item = map[string]any{}
			paths[row[1]] = item
		}
		item[strings.ToLower(row[0])] = map[string]any{"operationId": row[2]}
	}

	body, err := json.Marshal(map[string]any{"openapi": "3.1.0", "paths": paths})
	if err != nil {
		panic(err)
	}

	return string(body)
}

// verbByPath returns one registered verb, so a test drives the definition the
// binary actually ships rather than a copy of it.
func verbByPath(t *testing.T, path string) Verb {
	t.Helper()

	for _, verb := range registry() {
		if verb.Path == path {
			return verb
		}
	}

	t.Fatalf("no %q verb is registered", path)

	return Verb{}
}

// driveVerb runs one verb with a client the test controls, through the same
// flag parsing and rendering Run uses, and returns the exit code and both
// streams. It exists because Run builds its own *http.Client, so a test that
// wants to count the requests a verb makes has to hand it one.
func driveVerb(t *testing.T, verb Verb, client *Client, args ...string) (int, string, string) {
	t.Helper()

	var out, errOut bytes.Buffer
	env := Env{
		Args:    args,
		Stdout:  &out,
		Stderr:  &errOut,
		Stdin:   strings.NewReader(""),
		Getenv:  func(string) string { return "" },
		TTY:     true,
		Version: "0.0.0-test",
	}

	words := strings.Fields(verb.Path)
	rest := args
	if len(rest) >= len(words) && strings.Join(rest[:len(words)], " ") == verb.Path {
		rest = rest[len(words):]
	}

	fs := flag.NewFlagSet(verb.Path, flag.ContinueOnError)
	fs.SetOutput(&errOut)
	flags := registerGlobalFlags(fs)
	var verbFlags any
	if verb.Flags != nil {
		verbFlags = verb.Flags(fs)
	}

	positional, err := parseFlags(fs, flags, rest)
	if err != nil {
		t.Fatalf("parse %q: %v", args, err)
	}

	ctx := &Context{
		Env:       env,
		Verb:      verb,
		Flags:     flags,
		VerbFlags: verbFlags,
		Client:    client,
		Ctx:       context.Background(),
	}

	return renderResultWithDuration(env, ctx, verb.Run(ctx, positional), 0), out.String(), errOut.String()
}

// countingTransport answers with one canned response and counts the requests,
// so a test can prove a refusal never reached the server.
type countingTransport struct {
	document string
	requests atomic.Int64
	paths    []string
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.requests.Add(1)
	c.paths = append(c.paths, req.URL.Path)

	body := c.document
	if req.URL.Path != operationsPath {
		body = `{}`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestAPIListPrintsTheOperationsTheRunningServerServes(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows", "list-workflows"},
			[3]string{"POST", "/api/v1/workflows", "create-workflow"},
			[3]string{"GET", "/api/v1/health", "get-health"},
		)),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"api", "--list", "--json", "--url", srv.URL},
		TTY:  true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	ids := stringSlice(t, data["operations"])
	want := []string{"create-workflow", "get-health", "list-workflows"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("data.operations = %v, want the document's ids in sorted order %v", ids, want)
	}
	if data["count"] != float64(3) {
		t.Fatalf("data.count = %v, want 3", data["count"])
	}
	meta, _ := doc["meta"].(map[string]any)
	if meta["operation"] != "api" {
		t.Fatalf("meta.operation = %v, want api for a listing", meta["operation"])
	}
}

func TestAPIListQuietPrintsOneIdPerLine(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows", "list-workflows"},
			[3]string{"GET", "/api/v1/health", "get-health"},
		)),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"api", "--list", "--quiet", "--url", srv.URL},
		TTY:  true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if stdout != "get-health\nlist-workflows\n" {
		t.Fatalf("stdout = %q, want one sorted id per line", stdout)
	}
}

func TestAPISendsTheResolvedMethodPathQueryAndHeader(t *testing.T) {
	type captured struct {
		method string
		path   string
		query  url.Values
		header http.Header
		body   string
	}
	received := make(chan captured, 1)

	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"POST", "/api/v1/workflows/{id}/run", "run-workflow"},
		)),
		"/api/v1/workflows/wf_1/run": func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			received <- captured{
				method: r.Method,
				path:   r.URL.Path,
				query:  r.URL.Query(),
				header: r.Header.Clone(),
				body:   string(raw),
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"exec_1","status":"queued"}`)
		},
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{
			"api", "run-workflow", "--url", srv.URL, "--json",
			"--path", "id=wf_1",
			"--query", "source=cli",
			"--header", "X-Trace=abc",
			"--body", `{"input":{"a":1}}`,
		},
		TTY: true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	sent := <-received
	if sent.method != http.MethodPost {
		t.Errorf("method = %s, want POST", sent.method)
	}
	if sent.path != "/api/v1/workflows/wf_1/run" {
		t.Errorf("path = %s, want the resolved template", sent.path)
	}
	if sent.query.Get("source") != "cli" {
		t.Errorf("query = %v, want source=cli", sent.query)
	}
	if sent.header.Get("X-Trace") != "abc" {
		t.Errorf("X-Trace = %q, want abc", sent.header.Get("X-Trace"))
	}
	if sent.body != `{"input":{"a":1}}` {
		t.Errorf("body = %s, want the JSON body unchanged", sent.body)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["id"] != "exec_1" {
		t.Fatalf("data = %v, want the response body under data", data)
	}
	meta, _ := doc["meta"].(map[string]any)
	if meta["operation"] != "run-workflow" {
		t.Fatalf("meta.operation = %v, want the resolved operation id", meta["operation"])
	}
}

func TestAPIReadsTheBodyFromAFileAndFromStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--body-file", []string{"--body-file", path}, `{"from":"file"}`},
		{"--body @file", []string{"--body", "@" + path}, `{"from":"file"}`},
		{"--body -", []string{"--body", "-"}, `{"from":"stdin"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan string, 1)
			srv := stubAPI(t, map[string]http.HandlerFunc{
				operationsPath: jsonBody(http.StatusOK, servedDocument(
					[3]string{"POST", "/api/v1/workflows", "create-workflow"},
				)),
				"/api/v1/workflows": func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					received <- string(raw)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":"wf_1"}`)
				},
			})

			args := append([]string{"api", "create-workflow", "--url", srv.URL, "--json"}, tc.args...)
			code, _, _, stderr := runCLI(t, Env{
				Args:  args,
				TTY:   true,
				Stdin: strings.NewReader(`{"from":"stdin"}`),
			})
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
			}
			if got := <-received; got != tc.want {
				t.Fatalf("server received %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAPIRejectsAnInlineBodyThatIsNotJSON(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"POST", "/api/v1/workflows", "create-workflow"},
		)),
	})

	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"api", "create-workflow", "--url", srv.URL, "--json", "--body", "name=x"},
		TTY:  true,
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "usage" {
		t.Fatalf("error.code = %v, want usage", failure["code"])
	}
}

func TestAPIReturnsANonJSONBodyAsBase64WithItsContentType(t *testing.T) {
	const csv = "id,name\n1,one\n"

	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/datastores/{id}/rows/export", "export-datastore-rows"},
		)),
		"/api/v1/datastores/ds_1/rows/export": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/csv")
			_, _ = io.WriteString(w, csv)
		},
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{
			"api", "export-datastore-rows", "--url", srv.URL, "--json",
			"--path", "id=ds_1",
		},
		TTY: true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["contentType"] != "text/csv" {
		t.Fatalf("data.contentType = %v, want text/csv", data["contentType"])
	}
	raw, err := base64.StdEncoding.DecodeString(data["raw"].(string))
	if err != nil {
		t.Fatalf("data.raw is not base64: %v", err)
	}
	if string(raw) != csv {
		t.Fatalf("data.raw decoded to %q, want the CSV bytes", raw)
	}
}

// The api escape hatch's --out follows the same rule as the export's: a write
// the filesystem refuses is exit 1 with error.code output_error, never the
// exit 2 a caller reads as "the invocation was malformed".
func TestAPIOutFileReportsAnUnwritableFileAsAFailure(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/health", "get-health"},
		)),
		"/api/v1/health": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		},
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{
			"api", "get-health", "--url", srv.URL, "--json",
			"--out", filepath.Join(t.TempDir(), "no-such-dir", "health.json"),
		},
		TTY: true,
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitFailure, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "output_error" {
		t.Fatalf("error.code = %v, want output_error", failure["code"])
	}
}

func TestAPIOutDashWritesTheRawBodyToStdout(t *testing.T) {
	const csv = "id,name\n1,one\n"

	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/datastores/{id}/rows/export", "export-datastore-rows"},
		)),
		"/api/v1/datastores/ds_1/rows/export": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/csv")
			_, _ = io.WriteString(w, csv)
		},
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{
			"api", "export-datastore-rows", "--url", srv.URL, "--out", "-",
			"--path", "id=ds_1",
		},
		TTY: true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if stdout != csv {
		t.Fatalf("stdout = %q, want the raw body and no envelope", stdout)
	}
}

func TestAPIOutFileWritesTheBytesAndReportsThePath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "rows.csv")

	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/datastores/{id}/rows/export", "export-datastore-rows"},
		)),
		"/api/v1/datastores/ds_1/rows/export": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/csv")
			_, _ = io.WriteString(w, "id\n1\n")
		},
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{
			"api", "export-datastore-rows", "--url", srv.URL, "--json", "--out", target,
			"--path", "id=ds_1",
		},
		TTY: true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(written) != "id\n1\n" {
		t.Fatalf("file holds %q, want the response bytes", written)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["path"] != target {
		t.Fatalf("data = %v, want the written path", data)
	}
	if data["bytes"] != float64(len("id\n1\n")) {
		t.Fatalf("data.bytes = %v, want the byte count", data["bytes"])
	}
}

func TestAPIQuietPrintsTheResponsesIdentifier(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows/{id}", "get-workflow"},
			[3]string{"GET", "/api/v1/workflows", "list-workflows"},
		)),
		"/api/v1/workflows/wf_1": jsonBody(http.StatusOK, `{"id":"wf_1","name":"Manual"}`),
		"/api/v1/workflows":      jsonBody(http.StatusOK, `[{"id":"wf_1"}]`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"api", "get-workflow", "--url", srv.URL, "--quiet", "--path", "id=wf_1"},
		TTY:  true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if stdout != "wf_1\n" {
		t.Fatalf("stdout = %q, want the response's id alone", stdout)
	}

	// A listing has no single identifier, so --quiet prints nothing rather than
	// guessing at one.
	code, _, stdout, stderr = runCLI(t, Env{
		Args: []string{"api", "list-workflows", "--url", srv.URL, "--quiet"},
		TTY:  true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no output for a body with no identifier", stdout)
	}
}

func TestAPIUnknownOperationIsRefusedBeforeTheOperationRequest(t *testing.T) {
	transport := &countingTransport{document: servedDocument(
		[3]string{"GET", "/api/v1/workflows", "list-workflows"},
	)}
	client := &Client{BaseURL: "http://server.test", HTTP: &http.Client{Transport: transport}}

	code, stdout, _ := driveVerb(t, verbByPath(t, "api"), client,
		"api", "get-workfow", "--json", "--url", "http://server.test", "--path", "id=wf_1")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q)", code, ExitUsage, stdout)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "usage" {
		t.Fatalf("error.code = %v, want usage", failure["code"])
	}
	message, _ := failure["message"].(string)
	if !strings.Contains(message, `unknown operation id "get-workfow"`) {
		t.Fatalf("error.message = %q, want it to name the refused id", message)
	}
	if !strings.Contains(message, "api --list") {
		t.Fatalf("error.message = %q, want it to point at `kilasflow api --list`", message)
	}
	meta, _ := doc["meta"].(map[string]any)
	if meta["operation"] != "api" {
		t.Fatalf("meta.operation = %v, want api: no operation was resolved", meta["operation"])
	}

	if got := transport.paths; len(got) != 1 || got[0] != operationsPath {
		t.Fatalf("requests = %v, want only the index read %s", got, operationsPath)
	}
}

func TestAPIUnknownOperationSendsNothingWhenTheIndexIsAlreadyRead(t *testing.T) {
	transport := &countingTransport{}
	client := &Client{
		BaseURL:    "http://server.test",
		HTTP:       &http.Client{Transport: transport},
		operations: map[string]Operation{"list-workflows": {ID: "list-workflows", Method: http.MethodGet, Path: "/api/v1/workflows"}},
	}

	code, stdout, _ := driveVerb(t, verbByPath(t, "api"), client,
		"api", "get-workfow", "--json", "--url", "http://server.test")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q)", code, ExitUsage, stdout)
	}
	if got := transport.requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want none: the refusal needs no server", got)
	}
}

func TestAPIMissingPathParameterIsAUsageError(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows/{id}/versions/{versionId}", "get-workflow-version"},
		)),
	})

	code, handled, stdout, _ := runCLI(t, Env{
		Args: []string{"api", "get-workflow-version", "--url", srv.URL, "--json", "--path", "id=wf_1"},
		TTY:  true,
	})
	if !handled || code != ExitUsage {
		t.Fatalf("exit = %d handled = %v, want %d", code, handled, ExitUsage)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "usage" {
		t.Fatalf("error.code = %v, want usage", failure["code"])
	}
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "versionId") || !strings.Contains(message, "--path") {
		t.Fatalf("error.message = %q, want it to name the missing --path versionId", message)
	}
}

// An empty --path value is the shape `--path id=$WF_ID` takes when WF_ID is
// unset, and it is the mistake the escape hatch cannot detect afterwards: the
// substituted path is a real-looking route that the SPA catch-all answers with
// 200 text/html, so neither the status code nor the body says anything is
// wrong. It is refused before the request, exactly as a missing --path is.
func TestAPIEmptyPathParameterIsAUsageError(t *testing.T) {
	for _, value := range []string{"id=", "id=   "} {
		t.Run(value, func(t *testing.T) {
			requests := 0
			srv := stubAPI(t, map[string]http.HandlerFunc{
				operationsPath: jsonBody(http.StatusOK, servedDocument(
					[3]string{"GET", "/api/v1/workflows/{id}", "get-workflow"},
				)),
				// What `/api/v1/workflows/` actually answers on a real server.
				"/api/v1/workflows/": func(w http.ResponseWriter, _ *http.Request) {
					requests++
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = io.WriteString(w, "<!doctype html><html>the SPA</html>")
				},
			})

			out := filepath.Join(t.TempDir(), "wf.json")
			code, handled, stdout, _ := runCLI(t, Env{
				Args: []string{
					"api", "get-workflow", "--url", srv.URL, "--json",
					"--path", value, "--out", out,
				},
				TTY: true,
			})
			if !handled || code != ExitUsage {
				t.Fatalf("exit = %d handled = %v, want %d (stdout=%q)", code, handled, ExitUsage, stdout)
			}

			doc := envelope(t, stdout)
			failure, _ := doc["error"].(map[string]any)
			if failure["code"] != "usage" {
				t.Fatalf("error.code = %v, want usage", failure["code"])
			}
			message, _ := failure["message"].(string)
			if !strings.Contains(message, "--path id=<value>") {
				t.Fatalf("error.message = %q, want it to name the empty --path id", message)
			}
			if requests != 0 {
				t.Fatalf("requests = %d, want none: the refusal must precede the request", requests)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("--out wrote %s despite the refusal", out)
			}
		})
	}
}

func TestAPILeftoverPathParametersAreIgnored(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows/{id}", "get-workflow"},
		)),
		"/api/v1/workflows/wf_1": jsonBody(http.StatusOK, `{"id":"wf_1"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{
			"api", "get-workflow", "--url", srv.URL, "--json",
			"--path", "id=wf_1", "--path", "versionId=v_1", "--path", "type=kilasflow.manual",
		},
		TTY: true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	if doc["ok"] != true {
		t.Fatalf("envelope = %v, want ok", doc)
	}
}

func TestAPICarriesAServerRefusalVerbatim(t *testing.T) {
	const problem = `{"title":"Forbidden","status":403,"detail":"this key may not run workflows"}`

	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"POST", "/api/v1/workflows/{id}/run", "run-workflow"},
		)),
		"/api/v1/workflows/wf_1/run": problemBody(http.StatusForbidden, problem),
	})

	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"api", "run-workflow", "--url", srv.URL, "--json", "--path", "id=wf_1"},
		TTY:  true,
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d", code, ExitRefused)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "scope_denied" {
		t.Fatalf("error.code = %v, want scope_denied", failure["code"])
	}
	if failure["status"] != float64(http.StatusForbidden) {
		t.Fatalf("error.status = %v, want 403", failure["status"])
	}
	detail, _ := failure["detail"].(map[string]any)
	if detail["problem"] == nil {
		t.Fatalf("error.detail = %v, want the problem document verbatim", failure["detail"])
	}
}

func TestAPIListAndAnOperationIdAreMutuallyExclusive(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows", "list-workflows"},
		)),
	})

	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"api", "list-workflows", "--list", "--url", srv.URL, "--json"},
		TTY:  true,
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "usage" {
		t.Fatalf("error.code = %v, want usage", failure["code"])
	}
}

func TestAPIWithNoIdIsAUsageError(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument()),
	})

	code, _, stdout, _ := runCLI(t, Env{Args: []string{"api", "--url", srv.URL, "--json"}, TTY: true})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "usage" {
		t.Fatalf("error.code = %v, want usage", failure["code"])
	}
}

func TestResolveOperationEscapesPathValuesAndMergesQuery(t *testing.T) {
	index := map[string]Operation{
		"get-workflow-version": {ID: "get-workflow-version", Method: http.MethodGet, Path: "/api/v1/workflows/{id}/versions/{versionId}"},
	}

	method, target, err := resolveOperation(index, "get-workflow-version",
		map[string]string{"id": "wf 1/2", "versionId": "v_1"}, url.Values{"verbose": {"true"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if method != http.MethodGet {
		t.Fatalf("method = %s, want GET", method)
	}
	want := "/api/v1/workflows/wf%201%2F2/versions/v_1?verbose=true"
	if target != want {
		t.Fatalf("target = %s, want %s", target, want)
	}
}

func TestOperationsRejectsADocumentThatIsNotJSON(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<!doctype html><html></html>")
		},
	})

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := client.Operations(context.Background()); err == nil {
		t.Fatal("Operations accepted an HTML document")
	}
}

func TestOperationsRejectsADuplicateOperationId(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows", "list-workflows"},
			[3]string{"GET", "/api/v1/other", "list-workflows"},
		)),
	})

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := client.Operations(context.Background()); err == nil {
		t.Fatal("Operations accepted two operations with one id")
	}
}

func TestAPIUnknownFlagIsAUsageError(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{"GET", "/api/v1/workflows", "list-workflows"},
		)),
	})

	code, handled, _, stderr := runCLI(t, Env{
		Args: []string{"api", "list-workflows", "--url", srv.URL, "--nope", "x"},
		TTY:  true,
	})
	if !handled || code != ExitUsage {
		t.Fatalf("exit = %d handled = %v, want %d", code, handled, ExitUsage)
	}
	if !strings.Contains(stderr, "nope") {
		t.Fatalf("stderr = %q, want it to name the refused flag", stderr)
	}
}

func TestPairFlagRejectsAValueWithoutAName(t *testing.T) {
	flags := &pairFlags{}
	if err := flags.Set("just-a-name"); err == nil {
		t.Fatal("a --path value without '=' was accepted")
	}
	if err := flags.Set("=v"); err == nil {
		t.Fatal("a --query value with an empty name was accepted")
	}
}

// stringSlice reads a JSON array of strings out of a decoded envelope.
func stringSlice(t *testing.T, value any) []string {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%v is not an array", value)
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("%v is not a string", item)
		}
		out = append(out, text)
	}

	return out
}
