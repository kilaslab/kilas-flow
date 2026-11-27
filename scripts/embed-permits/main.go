// Command embed-permits exports the embed permit table from the Go rules that
// enforce it, so the docs state what the server does instead of a copy of it.
//
// Usage:
//
//	go run ./scripts/embed-permits          # regenerate scripts/embed-permits/embed-permits.json
//	go run ./scripts/embed-permits --check  # fail if the committed table is stale
//
// The table answers one question for every operation in the OpenAPI document:
// would the authority gate (internal/api/middleware, EmbedPermit — the same
// decision EmbedAuth enforces) let an embed session that holds exactly this one
// scope make this request? It is evaluated for each of the seven embed scopes,
// on a session whose own workflow or datastore id is substituted into the path,
// and records the refusal's status and sentence verbatim.
//
// The operations come from the document a server built from this tree serves
// (api.NewServer with no backing services), so an operation added later is in
// the table without anyone remembering to add it. scripts/generate-api-reference.mjs
// reads the committed JSON instead of restating the rules, and the check below
// is what fails CI when the gate changes and the file does not.
//
// Only what the gate decides is in the table. The handlers add checks the gate
// cannot see — which workflow an execution belongs to, which datastore an id
// names (404, not 403), which credentials a confined document may attach — and
// those stay prose.
//
// This command lives in its own directory for the same reason
// scripts/skills-index does: scripts/config-reference.go already declares
// func main in package main.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

// tablePath is the committed table, slash-separated from the repository root.
const tablePath = "scripts/embed-permits/embed-permits.json"

const regenerate = "make generate-embed-permits"

// The ids substituted into a path. They differ from each other so a path naming
// the "other" one is a request for a resource the session does not own.
const (
	ownWorkflow    = "wf_own"
	otherWorkflow  = "wf_other"
	ownDatastore   = "ds_own"
	otherDatastore = "ds_other"
	otherResource  = "res_1"
)

// subject is one kind of embed session and the scopes it may carry.
type subject struct {
	Kind   string   `json:"kind"`
	Scopes []string `json:"scopes"`
}

// verdict is one scope's answer to one operation.
type verdict struct {
	Allow bool `json:"allow"`
	// Status and Detail are the refusal a host page reads back, verbatim.
	Status int    `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// operation is every scope's answer to one operation.
type operation struct {
	Method string `json:"method"`
	// Path is the served path with its `{name}` parameters, /api/v1 included.
	Path string `json:"path"`
	// Verdicts is keyed by scope.
	Verdicts map[string]verdict `json:"verdicts"`
	// Requires is the weakest scope that opens the operation: every other scope
	// that opens it implies it. Empty when no scope opens the operation, or when
	// the scopes that do are independent of one another.
	Requires string `json:"requires,omitempty"`
	// OwnWorkflowOnly is set when the gate itself refuses the request once the
	// path or query names a workflow other than the session's own.
	OwnWorkflowOnly bool `json:"ownWorkflowOnly,omitempty"`
}

// table is the file this command writes.
type table struct {
	Source     string               `json:"source"`
	Subjects   []subject            `json:"subjects"`
	Operations map[string]operation `json:"operations"`
}

func main() {
	check := flag.Bool("check", false, "compare the rendered table against the committed file and fail on any difference")
	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "embed-permits: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := splice.RepoRoot()
	if err != nil {
		return err
	}
	built, err := build()
	if err != nil {
		return err
	}
	rendered, err := render(built)
	if err != nil {
		return err
	}

	path := filepath.Join(root, filepath.FromSlash(tablePath))
	committed, readErr := os.ReadFile(path)
	switch {
	case readErr == nil && bytes.Equal(committed, rendered):
		fmt.Printf("%s: %d operations, up to date\n", tablePath, len(built.Operations))

		return nil
	case readErr == nil && check:
		return fmt.Errorf("%s is stale: %s\nrun: %s", tablePath, splice.StaleSummary(string(committed), string(rendered)), regenerate)
	case os.IsNotExist(readErr) && check:
		return fmt.Errorf("%s does not exist\nrun: %s", tablePath, regenerate)
	case readErr != nil && !os.IsNotExist(readErr):
		return fmt.Errorf("read %s: %w", tablePath, readErr)
	}

	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tablePath, err)
	}
	fmt.Printf("%s: %d operations, %d bytes written\n", tablePath, len(built.Operations), len(rendered))

	return nil
}

// render is the committed form: indented, keys in a fixed order, one newline at
// the end.
func render(built table) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(built); err != nil {
		return nil, fmt.Errorf("encode the table: %w", err)
	}

	return buffer.Bytes(), nil
}

// documented is one operation as the OpenAPI document declares it.
type documented struct {
	id          string
	method      string
	path        string
	queryParams []string
}

// build evaluates the gate for every operation in the document.
func build() (table, error) {
	operations, err := documentedOperations()
	if err != nil {
		return table{}, err
	}

	subjects := []subject{
		{Kind: "workflow", Scopes: scopeNames(embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun, embed.ScopePublish, embed.ScopeActivate)},
		{Kind: "datastore", Scopes: scopeNames(embed.ScopeDatastoreRead, embed.ScopeDatastoreWrite)},
	}
	built := table{
		Source:     "Generated by `go run ./scripts/embed-permits` from internal/api/middleware.EmbedPermit; do not edit.",
		Subjects:   subjects,
		Operations: make(map[string]operation, len(operations)),
	}

	for _, entry := range operations {
		result := operation{Method: entry.method, Path: entry.path, Verdicts: map[string]verdict{}}
		var allowing []embed.Scope
		for _, kind := range subjects {
			for _, name := range kind.Scopes {
				scope := embed.Scope(name)
				status, detail := ask(kind.Kind, scope, entry, false)
				if status == 0 {
					result.Verdicts[name] = verdict{Allow: true}
					allowing = append(allowing, scope)
					if kind.Kind == "workflow" {
						if otherStatus, _ := ask(kind.Kind, scope, entry, true); otherStatus != 0 {
							result.OwnWorkflowOnly = true
						}
					}
					continue
				}
				result.Verdicts[name] = verdict{Status: status, Detail: detail}
			}
		}
		result.Requires = weakest(allowing)
		if _, taken := built.Operations[entry.id]; taken {
			return table{}, fmt.Errorf("operation id %q appears twice in the document", entry.id)
		}
		built.Operations[entry.id] = result
	}

	return built, nil
}

func scopeNames(scopes ...embed.Scope) []string {
	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, string(scope))
	}

	return names
}

// weakest is the allowing scope every other allowing scope implies, or "" when
// there is none (nothing allows the operation, or the allowing scopes are
// independent). Implication is the embed vocabulary's own: Session.Allows.
func weakest(allowing []embed.Scope) string {
	for _, candidate := range allowing {
		all := true
		for _, other := range allowing {
			if !(embed.Session{Scopes: []embed.Scope{other}}).Allows(candidate) {
				all = false
				break
			}
		}
		if all {
			return string(candidate)
		}
	}

	return ""
}

// ask is the gate's answer for a session holding only scope, making entry's
// request on a path that names the session's own subject — or, with other, a
// workflow that is not the session's.
func ask(kind string, scope embed.Scope, entry documented, other bool) (int, string) {
	session := embed.Session{
		ID: "es_table", TenantID: "tenant_table", Scopes: []embed.Scope{scope},
		Origin: "https://host.example",
	}
	workflowID := ownWorkflow
	if kind == "workflow" {
		session.WorkflowID = ownWorkflow
		if other {
			workflowID = otherWorkflow
		}
	} else {
		session.DatastoreID = ownDatastore
	}

	target := substitute(entry.path, kind, workflowID)
	query := url.Values{}
	for _, name := range entry.queryParams {
		if name == "workflowId" {
			query.Set(name, workflowID)
		}
	}
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	request := httptest.NewRequest(entry.method, target, nil)

	return middleware.EmbedPermit(session, request)
}

// substitute fills a path's parameters: the first parameter of a workflow or a
// datastore path is the session's own id when its kind matches the path, and any
// other parameter is a placeholder the gate does not read.
func substitute(path, kind, workflowID string) string {
	rest := strings.TrimPrefix(path, "/api/v1/")
	first, _, _ := strings.Cut(rest, "/")
	segments := strings.Split(path, "/")
	filled := false
	for index, segment := range segments {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		replacement := otherResource
		if !filled {
			switch {
			case first == "workflows" && kind == "workflow":
				replacement = workflowID
			case first == "workflows":
				replacement = otherWorkflow
			case first == "datastores" && kind == "datastore":
				replacement = ownDatastore
			case first == "datastores":
				replacement = otherDatastore
			}
		}
		filled = true
		segments[index] = replacement
	}

	return strings.Join(segments, "/")
}

// documentedOperations reads the OpenAPI document an in-process server serves.
func documentedOperations() ([]documented, error) {
	cfg := config.Default()
	handler := api.NewServer(api.Deps{
		Config:  cfg,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "embed-permits",
	}).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, api.OpenAPIPath+".json", nil))
	if recorder.Code != http.StatusOK {
		return nil, fmt.Errorf("GET %s.json answered %d", api.OpenAPIPath, recorder.Code)
	}

	var document struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		return nil, fmt.Errorf("decode the OpenAPI document: %w", err)
	}

	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true}
	var found []documented
	for path, item := range document.Paths {
		for method, details := range item {
			if !methods[method] {
				continue
			}
			if details.OperationID == "" {
				return nil, fmt.Errorf("%s %s has no operation id", strings.ToUpper(method), path)
			}
			entry := documented{id: details.OperationID, method: strings.ToUpper(method), path: path}
			for _, parameter := range details.Parameters {
				if parameter.In == "query" {
					entry.queryParams = append(entry.queryParams, parameter.Name)
				}
			}
			found = append(found, entry)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("the OpenAPI document holds no operations")
	}
	sort.Slice(found, func(left, right int) bool { return found[left].id < found[right].id })

	return found, nil
}
