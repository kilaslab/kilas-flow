package repository_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// What a Code node printed is written with its trace row and read back with
// it, on every driver, and a node that printed nothing stores NULL rather than
// an empty console it never printed.
func TestConsoleLinesArePersistedWithTheNodeRun(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-console"}
		saved, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "drv_console_wf", Name: "Console",
			Nodes: []workflow.Node{{
				ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
			}},
			Connections: []workflow.Connection{}, Settings: map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		executions := repository.NewExecutionStore(db.DB)
		queued, err := executions.QueueManualLatest(ctx, tenant, saved.ID, driverCatalog(), "", nil)
		if err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		claimed, _, ok, err := executions.ClaimNext(ctx, "drv-console-worker", time.Now().Add(time.Minute))
		if err != nil || !ok {
			t.Fatalf("ClaimNext() = (%v, %v), want a claim", ok, err)
		}
		if claimed.ID != queued.ID {
			t.Fatalf("claimed %q, want %q", claimed.ID, queued.ID)
		}

		console := json.RawMessage(`{"lines":[` +
			`{"level":"log","text":"first","at":"2026-09-23T10:00:00Z"},` +
			`{"level":"warn","text":"  indented\nsecond line","at":"2026-09-23T10:00:01Z"}` +
			`],"truncated":true}`)
		now := time.Now().UTC()
		created, err := executions.CreateNodeRuns(ctx, tenant, []execution.NodeRun{
			{
				TenantID: tenant.ID, ExecutionID: queued.ID, NodeID: "manual", Attempt: 1, Sequence: 1,
				Status: execution.StatusSucceeded, Input: json.RawMessage(`{}`), Output: json.RawMessage(`[[{}]]`),
				StartedAt: now, FinishedAt: &now, LeaseOwner: claimed.LeaseOwner,
			},
			{
				TenantID: tenant.ID, ExecutionID: queued.ID, NodeID: "code", Attempt: 1, Sequence: 2,
				Status: execution.StatusSucceeded, Input: json.RawMessage(`{}`), Output: json.RawMessage(`[[{}]]`),
				Console:   console,
				StartedAt: now, FinishedAt: &now, LeaseOwner: claimed.LeaseOwner,
			},
		})
		if err != nil {
			t.Fatalf("CreateNodeRuns() error = %v", err)
		}
		if len(created) != 2 {
			t.Fatalf("CreateNodeRuns() stored %d rows, want 2", len(created))
		}
		assertSameConsole(t, "created code run", created[1].Console, console)

		// Read back through a fresh query: what matters is what reached the
		// table, not what the write returned.
		stored, err := executions.Get(ctx, tenant, queued.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		runs := map[string]execution.NodeRun{}
		for _, run := range stored.NodeRuns {
			runs[run.NodeID] = run
		}
		assertSameConsole(t, "stored code run", runs["code"].Console, console)
		if len(runs["manual"].Console) != 0 {
			t.Errorf("stored manual run console = %s, want none: it printed nothing", runs["manual"].Console)
		}

		var nulls int64
		if err := db.Raw(`SELECT COUNT(*) FROM execution_node_runs WHERE tenant_id = ? AND execution_id = ? AND console IS NULL`,
			tenant.ID, queued.ID).Scan(&nulls).Error; err != nil {
			t.Fatalf("count NULL consoles: %v", err)
		}
		if nulls != 1 {
			t.Errorf("rows with a NULL console = %d, want 1: only the run that printed nothing", nulls)
		}
	})
}

// assertSameConsole compares two console details by value: the stored copy
// passes through the redacting payload path, which re-encodes it.
func assertSameConsole(t *testing.T, label string, got, want json.RawMessage) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s console = nil, want %s", label, want)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("%s console %s is not JSON: %v", label, got, err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("want %s is not JSON: %v", want, err)
	}
	gotJSON, _ := json.Marshal(gotValue)
	wantJSON, _ := json.Marshal(wantValue)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s console = %s, want %s", label, gotJSON, wantJSON)
	}
}
