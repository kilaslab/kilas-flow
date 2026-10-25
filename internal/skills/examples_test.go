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
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/shippedcatalogue"
	"github.com/kilaslab/kilas-flow/internal/skills"
	"github.com/kilaslab/kilas-flow/internal/workflow"
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
	if err := shippedcatalogue.Register(registry); err != nil {
		t.Fatalf("register the shipped catalogue: %v", err)
	}

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

// A request example is only worth running if a wrong body fails. `data` was the
// mistake the audit made for a credential (CLI-5): the operation wants `fields`,
// and the handler refuses a body without it.
func TestTheExampleHarnessRefusesAMalformedRequest(t *testing.T) {
	server := newExampleServer(t)

	problems := server.sendRequest(t, skills.Example{
		Path: "skills/kilasflow-fixture/references/EXAMPLE.md", Kind: skills.ExampleRequest,
		Operation: "create-credential", JSON: `{"name": "Upstream", "type": "httpHeaderAuth", "data": {}}`,
	})
	if len(problems) == 0 {
		t.Fatal("`api create-credential` accepted a body that carries `data` instead of `fields`")
	}
}

// kilasflow-node-configuration teaches the stored shape of every property kind
// an agent has to write by hand, and the compiler does not refuse a legacy or
// undeclared shape on its own (property.ReadLocator accepts a bare string, for
// one). So the example is read against the live catalogue: each parameter is
// looked up in its node's definition, the kinds the skill promises must each
// appear in a stored (not expression) value, and a locator must carry `__rl`,
// a mapper must decode, an assignment collection must hold its list and a
// key/value or fixed collection must be an object.
func TestNodeConfigurationExampleStoresEveryComplexKind(t *testing.T) {
	registry := node.NewRegistry()
	if err := shippedcatalogue.Register(registry); err != nil {
		t.Fatalf("register the shipped catalogue: %v", err)
	}

	var skill skills.Skill
	for _, candidate := range embeddedBundle(t) {
		if candidate.Name == "kilasflow-node-configuration" {
			skill = candidate
		}
	}
	if skill.Name == "" {
		t.Fatal("the bundle has no kilasflow-node-configuration skill")
	}

	examples, err := skills.Examples(skill)
	if err != nil {
		t.Fatalf("read the examples: %v", err)
	}

	covered := map[property.Kind]bool{}
	for _, example := range examples {
		if example.Kind != skills.ExampleWorkflow {
			continue
		}

		var document struct {
			Nodes []workflow.Node `json:"nodes"`
		}
		if err := json.Unmarshal([]byte(example.JSON), &document); err != nil {
			t.Fatalf("%s:%d: decode the document: %v", example.Path, example.Line, err)
		}

		for _, instance := range document.Nodes {
			definition, found := registry.Resolve(instance.Type, instance.TypeVersion)
			if !found {
				t.Errorf("%s:%d: node %q: the catalogue has no %s", example.Path, example.Line, instance.ID, instance.Type)

				continue
			}
			kinds := map[string]property.Kind{}
			for _, parameter := range definition.Parameters {
				kinds[parameter.Key] = parameter.Kind
			}

			for key, value := range instance.Parameters {
				where := fmt.Sprintf("%s:%d: node %q parameter %q", example.Path, example.Line, instance.ID, key)
				kind := kinds[key]
				if expression.IsExpression(value) {
					continue
				}

				switch kind {
				case property.KindResourceLocator:
					if stored, ok := value.(map[string]any); !ok || stored[property.LocatorSentinel] != true {
						t.Errorf("%s: a resource locator is stored as {\"__rl\": true, \"mode\", \"value\"}, got %v", where, value)
						continue
					}
				case property.KindResourceMapper:
					if _, ok := property.ReadMapping(value); !ok {
						t.Errorf("%s: a resource mapper is stored as {\"mappingMode\", \"value\", \"matchingColumns\"}, got %v", where, value)
						continue
					}
				case property.KindAssignmentCollection:
					stored, ok := value.(map[string]any)
					if _, listed := stored["assignments"].([]any); !ok || !listed {
						t.Errorf("%s: an assignment collection is stored as {\"assignments\": [ … ]}, got %v", where, value)
						continue
					}
				case property.KindKeyValue, property.KindFixedCollection:
					if _, ok := value.(map[string]any); !ok {
						t.Errorf("%s: a %s is stored as an object, got %T", where, kind, value)
						continue
					}
				}
				covered[kind] = true
			}
		}
	}

	for _, want := range []property.Kind{
		property.KindAssignmentCollection, property.KindResourceLocator, property.KindResourceMapper,
		property.KindKeyValue, property.KindFixedCollection,
	} {
		if !covered[want] {
			t.Errorf("the kilasflow-node-configuration example stores no %s value; it promises every complex kind", want)
		}
	}
}

// kilasflow-import-export promises a document that survives the trip to n8n and
// back, and a promise about a translation is only worth what a round trip
// proves: the example is saved, exported in the n8n format with nothing lossy,
// imported again with nothing unsupported but the workflow-level settings n8n
// adds, and the node types and names that
// come back are the ones that went out.
func TestImportExportExampleRoundTripsThroughN8n(t *testing.T) {
	server := newExampleServer(t)

	var examples []skills.Example
	for _, skill := range embeddedBundle(t) {
		if skill.Name != "kilasflow-import-export" {
			continue
		}
		found, err := skills.Examples(skill)
		if err != nil {
			t.Fatalf("read the examples: %v", err)
		}
		examples = found
	}

	tested := 0
	for _, example := range examples {
		if example.Kind != skills.ExampleWorkflow {
			continue
		}
		tested++

		dir := t.TempDir()
		original := filepath.Join(dir, "original.json")
		if err := os.WriteFile(original, []byte(example.JSON), 0o600); err != nil {
			t.Fatalf("write the example: %v", err)
		}

		var created struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		server.decode(t, &created, "workflow", "create", "--file", original)
		if created.Data.ID == "" {
			t.Fatal("`workflow create` answered no id")
		}

		var exported struct {
			Data struct {
				Workflow json.RawMessage `json:"workflow"`
				Lossy    []any           `json:"lossy"`
			} `json:"data"`
		}
		server.decode(t, &exported, "workflow", "export", created.Data.ID, "--format", "n8n")
		if len(exported.Data.Lossy) != 0 {
			t.Errorf("%s:%d: the n8n export is lossy: %v", example.Path, example.Line, exported.Data.Lossy)
		}

		body, err := json.Marshal(map[string]any{"format": "n8n", "workflow": exported.Data.Workflow})
		if err != nil {
			t.Fatalf("encode the import body: %v", err)
		}
		importBody := filepath.Join(dir, "import.json")
		if err := os.WriteFile(importBody, body, 0o600); err != nil {
			t.Fatalf("write the import body: %v", err)
		}

		var imported struct {
			Data struct {
				Unsupported []struct {
					Severity string `json:"severity"`
					Field    string `json:"field"`
					NodeID   string `json:"nodeId"`
					Reason   string `json:"reason"`
				} `json:"unsupported"`
				Workflow struct {
					LatestVersion struct {
						Document struct {
							Nodes []struct {
								Name string `json:"name"`
								Type string `json:"type"`
							} `json:"nodes"`
						} `json:"document"`
					} `json:"latestVersion"`
				} `json:"workflow"`
			} `json:"data"`
		}
		server.decode(t, &imported, "api", "import-workflow", "--yes", "--body", "@"+importBody)
		// The exporter's `settings.executionOrder: "v1"` is the order this server
		// runs, so the way back reports nothing for it: any issue is a node or a
		// value that did not survive.
		for _, issue := range imported.Data.Unsupported {
			t.Errorf("%s:%d: the import reports %s at %q (node %q): %s",
				example.Path, example.Line, issue.Severity, issue.Field, issue.NodeID, issue.Reason)
		}

		var want struct {
			Nodes []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal([]byte(example.JSON), &want); err != nil {
			t.Fatalf("decode the example: %v", err)
		}
		got := imported.Data.Workflow.LatestVersion.Document.Nodes
		if len(got) != len(want.Nodes) {
			t.Fatalf("%s:%d: %d nodes went out and %d came back", example.Path, example.Line, len(want.Nodes), len(got))
		}
		for index, node := range want.Nodes {
			if got[index].Name != node.Name || got[index].Type != node.Type {
				t.Errorf("%s:%d: node %d went out as %s %q and came back as %s %q",
					example.Path, example.Line, index, node.Type, node.Name, got[index].Type, got[index].Name)
			}
		}
	}
	if tested == 0 {
		t.Fatal("kilasflow-import-export carries no workflow example to round-trip")
	}
}

// decode runs a verb and decodes its envelope, failing the test on a non-zero
// exit.
func (server exampleServer) decode(t *testing.T, into any, args ...string) {
	t.Helper()

	code, stdout, stderr := server.run(t, args...)
	if code != cli.ExitOK {
		t.Fatalf("`kilasflow %s` exited %d: %s%s", strings.Join(args, " "), code, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	}
	if err := json.Unmarshal([]byte(stdout), into); err != nil {
		t.Fatalf("decode `kilasflow %s`: %v\n%s", strings.Join(args, " "), err, stdout)
	}
}

// The compiler does not refuse a parameter or a setting a node never declared:
// a key the executor does not read is stored and ignored, and a typo such as
// "metod" validates. An example is the shape an agent copies, so every key it
// writes has to be one the node's own definition declares.
//
// allowedUndeclaredParameters are the keys a node reads without listing them,
// each with the reason; a key is added here only when the node's code reads it.
var allowedUndeclaredParameters = map[string]map[string]string{
	"kilasflow.embeddings": {
		"mode": "read when the node computes its ports: cluster removes the main input, though the definition does not list it",
	},
}

// compilerSettings are the settings the compiler accepts beyond the shared
// block a definition lists: the error mode that adds a node's error port.
var compilerSettings = map[string]bool{"onError": true}

// undeclaredKeys returns one message per parameter or setting a workflow
// document writes that its node's definition does not declare.
func undeclaredKeys(t *testing.T, registry *node.Registry, document string) []string {
	t.Helper()

	var decoded struct {
		Nodes []workflow.Node `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("decode the document: %v", err)
	}

	var problems []string
	for _, instance := range decoded.Nodes {
		definition, found := registry.Resolve(instance.Type, instance.TypeVersion)
		if !found {
			problems = append(problems, fmt.Sprintf("node %q: the catalogue has no %s", instance.ID, instance.Type))

			continue
		}

		declared := map[string]bool{}
		for _, parameter := range definition.Parameters {
			declared[parameter.Key] = true
		}
		shared := map[string]bool{}
		for _, setting := range definition.SharedSettings {
			shared[setting.Key] = true
		}

		for key := range instance.Parameters {
			if declared[key] {
				continue
			}
			if _, allowed := allowedUndeclaredParameters[instance.Type][key]; allowed {
				continue
			}
			problems = append(problems, fmt.Sprintf("node %q (%s) sets the parameter %q, which its definition does not declare", instance.ID, instance.Type, key))
		}
		for key := range instance.Settings {
			if shared[key] || compilerSettings[key] {
				continue
			}
			problems = append(problems, fmt.Sprintf("node %q (%s) sets the setting %q, which is not a shared setting", instance.ID, instance.Type, key))
		}
	}
	sort.Strings(problems)

	return problems
}

func TestEverySkillExampleWritesOnlyDeclaredKeys(t *testing.T) {
	registry := node.NewRegistry()
	if err := shippedcatalogue.Register(registry); err != nil {
		t.Fatalf("register the shipped catalogue: %v", err)
	}

	checked := 0
	for name, examples := range bundleExamples(t) {
		for _, example := range examples {
			if example.Kind != skills.ExampleWorkflow {
				continue
			}
			checked++
			for _, problem := range undeclaredKeys(t, registry, example.JSON) {
				t.Errorf("%s: %s:%d: %s", name, example.Path, example.Line, problem)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no workflow example was checked")
	}
}

func TestTheKeyCheckRefusesATypo(t *testing.T) {
	registry := node.NewRegistry()
	if err := shippedcatalogue.Register(registry); err != nil {
		t.Fatalf("register the shipped catalogue: %v", err)
	}

	const document = `{"nodes": [
  {"id": "call", "name": "Call", "type": "kilasflow.httpRequest", "typeVersion": 1,
   "parameters": {"metod": "GET", "url": "https://example.com", "method": "GET"},
   "settings": {"retryOnFail": true, "retryOnFial": true}},
  {"id": "vec", "name": "Embed", "type": "kilasflow.embeddings", "typeVersion": 1,
   "parameters": {"mode": "cluster", "model": "m"}}
]}`

	problems := strings.Join(undeclaredKeys(t, registry, document), "\n")
	if !strings.Contains(problems, `"metod"`) || !strings.Contains(problems, `"retryOnFial"`) {
		t.Fatalf("the check did not name both typos:\n%s", problems)
	}
	if strings.Contains(problems, `"mode"`) || strings.Contains(problems, `"url"`) || strings.Contains(problems, `"retryOnFail"`) {
		t.Fatalf("the check refused a declared or allow-listed key:\n%s", problems)
	}
}
