package skills_test

// The example gate of BUG-x28fsx (CLI-5). A skill that describes a document
// shape in prose sends an agent to a 422 before it gets the shape right, so
// every skill carries a fenced example (internal/skills/examples.go) and this
// file runs each one through the product:
//
//   - a `kilasflow-workflow` block goes through `kilasflow workflow validate`,
//     the CLI verb an agent would run, against an in-process API server that
//     holds the whole shipped catalogue;
//   - a `kilasflow-request <operation-id>` block is sent with
//     `kilasflow api <operation-id> --body @file`, so a credential or datastore
//     body is accepted by the real handler.
//
// The server is in-process (SQLite in a temp directory), so this runs in
// `go test ./...` and in `make skills-check` with no binary and no network.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/cli"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/skills"
)

// exampleServer is an in-process API server holding the shipped catalogue.
type exampleServer struct {
	url      string
	registry *node.Registry
}

func newExampleServer(t *testing.T) exampleServer {
	t.Helper()

	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "examples.db"),
	}, discard)
	if err != nil {
		t.Fatalf("open the example database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, discard); err != nil {
		t.Fatalf("migrate the example database: %v", err)
	}

	registry := node.NewRegistry()
	registerShipped(t, registry)

	key := make([]byte, credentials.KeySize)
	for index := range key {
		key[index] = byte(index + 7)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("build the credential cipher: %v", err)
	}
	store, err := datastore.NewEngine(db, "")
	if err != nil {
		t.Fatalf("build the datastore engine: %v", err)
	}

	server := httptest.NewServer(api.NewServer(api.Deps{
		Config:       config.Default(),
		Logger:       discard,
		DB:           db,
		NodeRegistry: registry,
		Workflows:    repository.NewWorkflowStore(db.DB),
		Credentials:  repository.NewCredentialStore(db.DB, cipher),
		Datastores:   store,
		Version:      "0.0.0-skill-examples",
	}).Handler())
	t.Cleanup(server.Close)

	return exampleServer{url: server.URL, registry: registry}
}

// run drives the CLI in-process the way an agent's shell would, with no
// environment: the server is named by --url and no configuration file is read.
func (server exampleServer) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code, handled := cli.Run(cli.Env{
		Args:   append(append([]string{}, args...), "--url", server.url, "--json"),
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(string) string { return "" },
	})
	if !handled {
		t.Fatalf("`kilasflow %s` was not claimed by the CLI", strings.Join(args, " "))
	}

	return code, stdout.String(), stderr.String()
}

// validation is the data of `workflow validate --json`.
type validation struct {
	Valid       bool `json:"valid"`
	Diagnostics []struct {
		Severity string `json:"severity"`
		NodeID   string `json:"nodeId"`
		Field    string `json:"field"`
		Reason   string `json:"reason"`
	} `json:"diagnostics"`
	Warnings []struct {
		NodeID string `json:"nodeId"`
		Field  string `json:"field"`
		Reason string `json:"reason"`
	} `json:"warnings"`
}

// validateDocument runs a workflow document through `workflow validate` and
// returns the problems it found, empty when the document is clean. A warning is
// a problem too: an example that only compiles with a caveat is not the minimal,
// complete shape a skill promises.
func (server exampleServer) validateDocument(t *testing.T, document string) []string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "workflow.json")
	if err := os.WriteFile(file, []byte(document), 0o600); err != nil {
		t.Fatalf("write the example document: %v", err)
	}

	code, stdout, stderr := server.run(t, "workflow", "validate", "--file", file)
	if code != cli.ExitOK {
		return []string{fmt.Sprintf("`workflow validate` exited %d: %s%s", code, strings.TrimSpace(stdout), strings.TrimSpace(stderr))}
	}

	var envelope struct {
		OK   bool       `json:"ok"`
		Data validation `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode `workflow validate --json`: %v\n%s", err, stdout)
	}

	var problems []string
	if !envelope.Data.Valid {
		problems = append(problems, "the document is not valid")
	}
	for _, diagnostic := range envelope.Data.Diagnostics {
		problems = append(problems, fmt.Sprintf("%s at %s (node %q): %s", diagnostic.Severity, diagnostic.Field, diagnostic.NodeID, diagnostic.Reason))
	}
	for _, warning := range envelope.Data.Warnings {
		problems = append(problems, fmt.Sprintf("warning at %s (node %q): %s", warning.Field, warning.NodeID, warning.Reason))
	}

	return problems
}

// sendRequest sends a request example to its operation, as `kilasflow api` does
// for an agent, and returns what went wrong. --yes stands in for the user's
// instruction a guarded operation waits for.
func (server exampleServer) sendRequest(t *testing.T, example skills.Example) []string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(file, []byte(example.JSON), 0o600); err != nil {
		t.Fatalf("write the example body: %v", err)
	}

	code, stdout, stderr := server.run(t, "api", example.Operation, "--body", "@"+file, "--yes")
	if code != cli.ExitOK {
		return []string{fmt.Sprintf("`api %s` exited %d: %s%s", example.Operation, code, strings.TrimSpace(stdout), strings.TrimSpace(stderr))}
	}

	return nil
}

// bundleExamples lists the examples of every skill, failing the test on a
// malformed fence.
func bundleExamples(t *testing.T) map[string][]skills.Example {
	t.Helper()

	found := map[string][]skills.Example{}
	for _, skill := range embeddedBundle(t) {
		examples, err := skills.Examples(skill)
		if err != nil {
			t.Errorf("%s: %v", skill.Name, err)

			continue
		}
		found[skill.Name] = examples
	}

	return found
}

// Every example is accepted by the product it teaches: a workflow compiles
// through `kilasflow workflow validate`, and a request body is taken by its
// operation.
func TestEverySkillExampleValidates(t *testing.T) {
	server := newExampleServer(t)

	examples := bundleExamples(t)
	names := make([]string, 0, len(examples))
	for name := range examples {
		names = append(names, name)
	}
	sort.Strings(names)

	checked := 0
	for _, name := range names {
		for _, example := range examples[name] {
			checked++
			t.Run(fmt.Sprintf("%s/%s:%d", name, filepath.Base(example.Path), example.Line), func(t *testing.T) {
				var problems []string
				switch example.Kind {
				case skills.ExampleWorkflow:
					problems = server.validateDocument(t, example.JSON)
				case skills.ExampleRequest:
					problems = server.sendRequest(t, example)
				}
				for _, problem := range problems {
					t.Errorf("%s:%d: %s", example.Path, example.Line, problem)
				}
			})
		}
	}
	if checked == 0 {
		t.Fatal("no skill carries an example, so nothing was validated")
	}
}

// The harness must be able to fail. A model wired into an agent's `main` input
// instead of its `model` port is the mistake the AI skill exists to prevent, and
// the product refuses it; if this document ever validated, the examples above
// would be proving nothing.
func TestTheExampleHarnessRefusesAMiswiredAgent(t *testing.T) {
	server := newExampleServer(t)

	const document = `{
  "schemaVersion": 1,
  "name": "Mis-wired agent",
  "nodes": [
    {"id": "chat", "name": "Chat", "type": "kilasflow.chatTrigger", "typeVersion": 1, "position": {"x": 0, "y": 0}},
    {"id": "agent", "name": "Agent", "type": "kilasflow.agent", "typeVersion": 1, "position": {"x": 240, "y": 0},
      "parameters": {"prompt": {"mode": "expression", "value": "{{ $json.chatInput }}"}}},
    {"id": "model", "name": "Model", "type": "kilasflow.chatModel", "typeVersion": 1, "position": {"x": 240, "y": 200},
      "parameters": {"model": "gpt-4o-mini"}}
  ],
  "connections": [
    {"id": "c1", "kind": "main", "source": {"nodeId": "chat", "port": "main"}, "target": {"nodeId": "agent", "port": "main"}},
    {"id": "c2", "kind": "main", "source": {"nodeId": "model", "port": "model"}, "target": {"nodeId": "agent", "port": "model"}}
  ],
  "settings": {}
}`

	problems := server.validateDocument(t, document)
	if len(problems) == 0 {
		t.Fatal("`workflow validate` accepted a model wired through a main connection")
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "/connections/1/kind") {
		t.Errorf("the refusal does not name the connection that was mis-wired: %s", joined)
	}
}
