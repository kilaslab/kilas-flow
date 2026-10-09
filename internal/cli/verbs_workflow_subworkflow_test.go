package cli

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// bootSubworkflowServer serves the real API over real stores with the
// sub-workflow extractor the binary wires, and saves an inactive child and a
// parent that calls it. The refusal under test is produced by the repository
// and mapped by the handler, so a stub that returned a canned 422 would prove
// nothing about either.
func bootSubworkflowServer(t *testing.T) (srv *httptest.Server, parentID string) {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "cli-subworkflow.db"),
	}, log)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	store := repository.NewWorkflowStore(db.DB).WithSubworkflows(nodes.SubworkflowCalls)

	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	manual := workflow.Node{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}
	child, err := store.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Billing child",
		Nodes: []workflow.Node{manual}, Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft(child) error = %v", err)
	}
	parent, err := store.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Parent",
		Nodes: []workflow.Node{manual, {
			ID: "call", Name: "Run billing", Type: nodes.ExecuteWorkflowNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{"workflowId": map[string]any{"__rl": true, "mode": "id", "value": child.ID}},
		}},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "call", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft(parent) error = %v", err)
	}

	handler := api.NewServer(api.Deps{
		Config:       config.Default(),
		Logger:       log,
		DB:           db,
		Version:      "0.0.0-test",
		NodeRegistry: registry,
		Workflows:    store,
		Executions:   repository.NewExecutionStore(db.DB),
	}).Handler()
	srv = httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv, parent.ID
}

// assertSubworkflowRefusal checks the envelope a refused activation produces:
// the error message is the whole sentence, so a caller that prints only the
// message still learns the node and the child, and the structured issue is
// carried beside it.
func assertSubworkflowRefusal(t *testing.T, text string) {
	t.Helper()

	doc := envelope(t, text)
	failure, _ := doc["error"].(map[string]any)
	if failure == nil {
		t.Fatalf("no error in the envelope: %s", text)
	}
	if failure["status"] != float64(422) {
		t.Errorf("error.status = %v, want 422 (envelope: %s)", failure["status"], text)
	}
	message, _ := failure["message"].(string)
	for _, want := range []string{"Run billing", "Billing child", "not active", "activate that workflow first"} {
		if !strings.Contains(message, want) {
			t.Errorf("error.message = %q, want it to contain %q", message, want)
		}
	}
	if strings.Contains(message, "workflow operation failed") || failure["status"] == float64(500) {
		t.Errorf("error = %v, want the refusal and not the generic 500", failure)
	}
	if !strings.Contains(text, `"config.invalid"`) || !strings.Contains(text, `"nodeId":"call"`) {
		t.Errorf("the envelope does not carry the config.invalid issue on node call: %s", text)
	}
}

func TestWorkflowActivateSaysWhichNodeNeedsWhichInactiveChild(t *testing.T) {
	srv, parentID := bootSubworkflowServer(t)

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "activate", parentID, "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	// 422 is the CLI's bad_request, a usage-class exit; the generic failure
	// (1) is what the 500 it replaces produced.
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	assertSubworkflowRefusal(t, stdout)
}

func TestMCPWorkflowActivateReturnsTheSameRefusal(t *testing.T) {
	srv, parentID := bootSubworkflowServer(t)

	session := startMCPServe(t, srv.URL)
	session.initialize()

	answer := session.call("workflow_activate", map[string]any{"workflow_id": parentID, "confirm": true})
	if !answer.isError {
		t.Fatalf("isError = false, want the refusal flagged as an error (text=%q)", answer.text)
	}
	assertSubworkflowRefusal(t, answer.text)
}
