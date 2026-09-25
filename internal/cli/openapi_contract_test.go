package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
)

// This file is the acceptance proof for the escape hatch: it walks every
// operation the published contract page promises against a real in-process
// server, so "kilasflow api reaches every documented operation" is a test
// result rather than a claim about a table someone maintained by hand.
//
// The server is built with api.NewServer, which is the composition the binary
// runs, and the CLI talks to it over a real socket. Importing internal/api here
// is deliberate and test-only: the CLI itself must never depend on the server
// (see doc.go).

// contractPagePath is the page whose tables are the contract.
const contractPagePath = "docs/src/content/docs/reference/api-contract.md"

// contractRowCount is how many operations the page promises. It is asserted
// rather than derived: a parser that silently matches nothing would otherwise
// make the walk pass by walking no rows at all.
const contractRowCount = 53

// Synthetic path parameters, so every row resolves. A placeholder the row does
// not carry is ignored; an id that names no existing resource still proves the
// route exists, which is what this walk is about.
const (
	contractID       = "00000000-0000-0000-0000-000000000000"
	contractNodeType = "kilasflow.manual"
	contractName     = "smoke"
)

// stubPinger stands in for the database: a Pinger is all NewServer needs to
// build the full route tree without one.
type stubPinger struct{}

func (stubPinger) Ping(context.Context) error { return nil }

// contractRow is one operation row of the contract page.
type contractRow struct {
	Method string
	Path   string
	ID     string
}

// bootContractServer serves the real API over a real socket, with no database
// and no stores: the routes are what this walk is about, and a handler that
// cannot do its work answers a problem document, which is still an answer from
// the operation rather than from the SPA.
func bootContractServer(t *testing.T) *httptest.Server {
	t.Helper()

	handler := api.NewServer(api.Deps{
		Config:  config.Default(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:      stubPinger{},
		Version: "0.0.0-test",
	}).Handler()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// repoRoot walks up from this file until it finds the module root, so the test
// reads the page the repository ships rather than a copy of it.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}

	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", file)
		}
		dir = parent
	}
}

// contractRows parses the operation tables out of the contract page.
//
// A row is a markdown table line whose second column is an HTTP method, whose
// third is a backticked path and whose fourth is a backticked operation id.
// The page's other tables — the embed boundary's verdicts, the versioning
// numbers, the drift gates — do not carry that shape.
func contractRows(t *testing.T, page string) []contractRow {
	t.Helper()

	methods := map[string]bool{
		http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
		http.MethodDelete: true, http.MethodPatch: true,
	}

	rows := make([]contractRow, 0, contractRowCount)
	for _, line := range strings.Split(page, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}

		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}
		method := strings.TrimSpace(cells[1])
		path, pathOK := backticked(cells[2])
		id, idOK := backticked(cells[3])
		if !methods[method] || !pathOK || !idOK || !strings.HasPrefix(path, "/") {
			continue
		}

		rows = append(rows, contractRow{Method: method, Path: path, ID: id})
	}

	return rows
}

// backticked reads a table cell that must hold exactly one `code` span.
func backticked(cell string) (string, bool) {
	cell = strings.TrimSpace(cell)
	if len(cell) < 3 || !strings.HasPrefix(cell, "`") || !strings.HasSuffix(cell, "`") {
		return "", false
	}

	inner := cell[1 : len(cell)-1]
	if inner == "" || strings.Contains(inner, "`") || strings.ContainsAny(inner, " \t") {
		return "", false
	}

	return inner, true
}

// contractPathArgs are the synthetic --path values every row is driven with.
func contractPathArgs() []string {
	return []string{
		"--path", "id=" + contractID,
		"--path", "versionId=" + contractID,
		"--path", "type=" + contractNodeType,
		"--path", "name=" + contractName,
	}
}

// answeredByTheOperation reports whether a response came from the API rather
// than from the SPA's catch-all, which answers 200 text/html for an unknown
// /api/v1 path and 405 text/plain for a non-GET one. A status code alone cannot
// tell the two apart, which is why the CLI refuses an unknown operation id
// before it sends anything.
func answeredByTheOperation(doc map[string]any) (bool, string) {
	if failure, failed := doc["error"].(map[string]any); failed {
		if detail, present := failure["detail"].(map[string]any); present && detail["problem"] != nil {
			return true, ""
		}

		return false, fmt.Sprintf("HTTP %v with no problem document", failure["status"])
	}

	data, _ := doc["data"].(map[string]any)
	contentType, _ := data["contentType"].(string)
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return false, "a text/html body"
	}

	return true, ""
}

// envelopeFailure reads the error code of a decoded envelope, empty on success.
func envelopeFailure(doc map[string]any) string {
	failure, failed := doc["error"].(map[string]any)
	if !failed {
		return ""
	}

	code, _ := failure["code"].(string)

	return code
}

func TestAPIPrefixMatchesTheServerConstant(t *testing.T) {
	if apiPrefix != api.APIPrefix {
		t.Fatalf("apiPrefix = %q, but the server serves %q", apiPrefix, api.APIPrefix)
	}
}

// TestTheSPACatchAllAnswersAnUnknownAPIPath is the control for the walk below:
// it proves that a wrong /api/v1 path is answered by the SPA with HTML and a
// 200, which is why an unknown operation id has to be refused before the
// request rather than detected from a status code.
func TestTheSPACatchAllAnswersAnUnknownAPIPath(t *testing.T) {
	srv := bootContractServer(t)

	resp, err := srv.Client().Get(srv.URL + apiPrefix + "/definitely-not-an-operation")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want the SPA's 200: if this changed, the escape hatch's refusal could be relaxed", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
	if !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatalf("body = %q, want the SPA document", body)
	}
}

func TestAPIEscapeHatchWalksTheContract(t *testing.T) {
	page, err := os.ReadFile(filepath.Join(repoRoot(t), contractPagePath))
	if err != nil {
		t.Fatalf("read the contract page: %v", err)
	}

	rows := contractRows(t, string(page))
	if len(rows) != contractRowCount {
		t.Fatalf("collected %d operation rows from %s, want %d: the page or the parser changed",
			len(rows), contractPagePath, contractRowCount)
	}

	srv := bootContractServer(t)
	client := &Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 30 * time.Second}}

	index, err := client.Operations(context.Background())
	if err != nil {
		t.Fatalf("read %s: %v", operationsPath, err)
	}

	// Every row must resolve to the identical method and path the server
	// serves, before any of them is called: a row that drifted from the
	// document is a broken promise in the page, not a bug in the CLI.
	for _, row := range rows {
		operation, served := index[row.ID]
		if !served {
			t.Errorf("%s lists %q, which the running server does not serve", contractPagePath, row.ID)

			continue
		}
		if operation.Method != row.Method {
			t.Errorf("%s: method = %s, want the page's %s", row.ID, operation.Method, row.Method)
		}
		if want := apiPrefix + row.Path; operation.Path != want {
			t.Errorf("%s: path = %s, want the page's %s", row.ID, operation.Path, want)
		}
	}

	// Every row is then driven through the CLI: the id resolves (never the
	// usage refusal), the envelope names the operation that was called, and the
	// answer came from the API rather than from the SPA. `--yes` is always
	// passed: BUG-r1m83f made the escape hatch ask a guarded row's own
	// confirmation gate, exactly as the verb it wraps would, so a row this
	// walk drives without it would be refused before the request and never
	// reach the server this test is about. An unguarded row ignores the flag.
	for _, row := range rows {
		t.Run(row.ID, func(t *testing.T) {
			args := append([]string{"api", row.ID, "--yes", "--url", srv.URL, "--json"}, contractPathArgs()...)
			code, handled, stdout, stderr := runCLI(t, Env{Args: args, TTY: true})
			if !handled {
				t.Fatalf("`api %s` fell through to the server path", row.ID)
			}

			doc := envelope(t, stdout)
			meta, _ := doc["meta"].(map[string]any)
			if meta["operation"] != row.ID {
				t.Errorf("meta.operation = %v, want %s", meta["operation"], row.ID)
			}

			switch envelopeFailure(doc) {
			case "usage":
				t.Errorf("the escape hatch refused %q, which the contract page lists (stderr=%q)", row.ID, stderr)
			case "":
				ok, reason := answeredByTheOperation(doc)
				if !ok {
					t.Errorf("`api %s` was answered by %s, not by the operation", row.ID, reason)
				}
			default:
				// A refusal or a failure from the operation itself is an
				// answer: the path resolved and the API handled it.
				ok, reason := answeredByTheOperation(doc)
				if !ok {
					t.Errorf("`api %s` failed with %s, not a problem document: %s", row.ID, envelopeFailure(doc), reason)
				}
			}

			// A real request for the rows that change nothing and need no
			// parameter: these are the ones whose success or failure is a fact
			// about the server rather than about the request shape.
			if row.Method == http.MethodGet && !strings.Contains(row.Path, "{") {
				if code == ExitUsage {
					t.Errorf("exit = %d for a GET row with no path parameter", code)
				}
			}
		})
	}

	// `api --list` is the served document, not a subset of it and not a table
	// compiled into the binary.
	code, _, stdout, stderr := runCLI(t, Env{Args: []string{"api", "--list", "--json", "--url", srv.URL}, TTY: true})
	if code != ExitOK {
		t.Fatalf("`api --list` exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	listed := stringSlice(t, envelope(t, stdout)["data"].(map[string]any)["operations"])
	served := sortedOperations(index)
	if strings.Join(listed, "\n") != strings.Join(served, "\n") {
		t.Errorf("`api --list` = %d ids, the served document holds %d; want the same set", len(listed), len(served))
	}
	if !sort.StringsAreSorted(listed) {
		t.Errorf("`api --list` is not sorted: %v", listed)
	}
	for _, row := range rows {
		if _, present := index[row.ID]; !present {
			continue
		}
		if !contains(listed, row.ID) {
			t.Errorf("`api --list` omits %q, which the served document holds", row.ID)
		}
	}

	t.Logf("walked %d contract rows against %d served operations", len(rows), len(index))
}

// contains reports whether a sorted list holds a value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}

	return false
}

// TestOperationsComeFromTheDocumentNotTheContractPage keeps the two counts
// honest: the page is a subset written around the workflow-facing core, and a
// test that conflated them would either claim to walk the whole surface or
// accept a stale page as the surface.
func TestOperationsComeFromTheDocumentNotTheContractPage(t *testing.T) {
	srv := bootContractServer(t)
	client := &Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 30 * time.Second}}

	index, err := client.Operations(context.Background())
	if err != nil {
		t.Fatalf("read %s: %v", operationsPath, err)
	}

	page, err := os.ReadFile(filepath.Join(repoRoot(t), contractPagePath))
	if err != nil {
		t.Fatalf("read the contract page: %v", err)
	}

	var undocumented []string
	rows := contractRows(t, string(page))
	documented := make(map[string]bool, len(rows))
	for _, row := range rows {
		documented[row.ID] = true
	}
	for id := range index {
		if !documented[id] {
			undocumented = append(undocumented, id)
		}
	}
	sort.Strings(undocumented)

	if len(index) <= len(rows) {
		t.Fatalf("the served document holds %d operations and the page lists %d; the page is meant to be a subset of a larger surface",
			len(index), len(rows))
	}
	if len(undocumented) == 0 {
		t.Fatalf("no operation is missing from the page: it is meant to enumerate the workflow-facing core, not everything")
	}
	t.Logf("the served document holds %d operations; the page documents %d and omits %d, all reachable through `api <operation-id>`",
		len(index), len(rows), len(undocumented))

	// The ids the page omits must still be reachable, which is the whole point
	// of reading the document instead of the page.
	missing := undocumented[0]
	code, _, stdout, stderr := runCLI(t, Env{
		Args: append([]string{"api", missing, "--url", srv.URL, "--json"}, contractPathArgs()...),
		TTY:  true,
	})
	if code == ExitUsage && envelopeFailure(envelope(t, stdout)) == "usage" {
		t.Fatalf("`api %s` was refused although the server serves it (stderr=%q)", missing, stderr)
	}
}
