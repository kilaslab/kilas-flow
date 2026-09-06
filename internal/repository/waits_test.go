package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// The checkpoint must survive suspension byte-identical, including values
// whose keys look sensitive. Anything matching isSensitiveKey or
// looksLikeCredential would come back "[redacted]" if the checkpoint ever
// travelled the redacting payload() path.
var waitCheckpointFixture = json.RawMessage(`{"upstream":{"apiKey":"hunter2-secret","nested":{"password":"x"}},"items":[1,2]}`)

func openWaitsSQLite(t *testing.T) *database.DB {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"),
	}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func queueAndClaimWaitFixture(t *testing.T, db *database.DB, tenantID, workflowID string) (repository.TenantScope, *repository.GORMExecutionStore, execution.Record) {
	t.Helper()
	ctx := context.Background()
	tenant := repository.TenantScope{ID: tenantID}
	saved, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID, Name: "Waiting workflow",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)
	if _, err := store.QueueManualLatest(ctx, tenant, saved.ID, driverCatalog(), json.RawMessage(`{"start":"here"}`)); err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	record, _, claimed, err := store.ClaimNext(ctx, "worker-1", time.Now().UTC().Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("ClaimNext() = (%v, %v), want a claim", claimed, err)
	}
	// The service persists completed runs before suspending, so the fixture
	// does too: the suspended RunCount below is truthful, not illustrative.
	for sequence, nodeID := range []string{"manual", "set"} {
		if _, err := store.CreateNodeRun(ctx, tenant, execution.NodeRun{
			TenantID: tenant.ID, ExecutionID: record.ID, NodeID: nodeID,
			Attempt: 1, Sequence: sequence + 1, Status: execution.StatusSucceeded,
			Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`),
			StartedAt: time.Now().UTC(), LeaseOwner: record.LeaseOwner,
		}); err != nil {
			t.Fatalf("CreateNodeRun(%q) error = %v", nodeID, err)
		}
	}
	return tenant, store, record
}

func suspendWaitFixture(t *testing.T, store *repository.GORMExecutionStore, tenant repository.TenantScope, record execution.Record, expiresAt time.Time) (string, repository.Wait) {
	t.Helper()
	const token = "test-token-for-wait-fixture-01"
	wait, suspended, err := store.SuspendExecution(context.Background(), tenant, repository.SuspendWaitParams{
		ExecutionID: record.ID, LeaseOwner: record.LeaseOwner, WorkflowID: record.WorkflowID,
		NodeID: "approve", Mode: "approval",
		TokenHash: repository.HashWaitToken(token), ResumeToken: token,
		Checkpoint: append([]byte(nil), waitCheckpointFixture...), RunCount: 2, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("SuspendExecution() error = %v", err)
	}
	if suspended.Status != execution.StatusWaiting {
		t.Fatalf("suspended status = %q, want waiting", suspended.Status)
	}
	return token, wait
}

func TestSuspendReleasesLeaseAndCheckpointsVerbatim(t *testing.T) {
	db := openWaitsSQLite(t)
	tenant, store, record := queueAndClaimWaitFixture(t, db, "wait-tenant-a", "wait_wf_a")
	if record.LeaseOwner == "" {
		t.Fatal("claimed record carries no lease to release")
	}

	token, wait := suspendWaitFixture(t, store, tenant, record, time.Now().UTC().Add(time.Hour))
	if wait.TokenHash != repository.HashWaitToken(token) {
		t.Error("wait stores something other than the token hash")
	}
	loaded, err := store.FindWaitByToken(context.Background(), repository.HashWaitToken(token))
	if err != nil {
		t.Fatalf("FindWaitByToken() error = %v", err)
	}
	if loaded.ResumeToken != token {
		t.Error("the live wait keeps no retrievable token, so no approval URL can be composed")
	}
	if loaded.RunCount != 2 {
		t.Errorf("wait run count = %d, want the two persisted runs", loaded.RunCount)
	}

	// A suspended run must not read as a crashed one: the reclaim predicate
	// cannot select it, so nothing is claimed.
	if _, _, claimed, err := store.ClaimNext(context.Background(), "worker-2", time.Now().UTC().Add(time.Minute)); err != nil || claimed {
		t.Errorf("ClaimNext() = (%v, %v), want nothing claimed while the only execution waits", claimed, err)
	}

	// The same loaded row carries the checkpoint: one lookup proves both.
	if string(loaded.Checkpoint) != string(waitCheckpointFixture) {
		t.Errorf("checkpoint = %s, want it byte-identical (redaction would corrupt it)", loaded.Checkpoint)
	}
	if loaded.Mode != "approval" || loaded.NodeID != "approve" {
		t.Errorf("wait = %+v, want the suspending node and mode", loaded)
	}
}

func TestResumeTokenIsSingleUseWithDistinctRefusals(t *testing.T) {
	db := openWaitsSQLite(t)
	tenant, store, record := queueAndClaimWaitFixture(t, db, "wait-tenant-b", "wait_wf_b")
	token, _ := suspendWaitFixture(t, store, tenant, record, time.Now().UTC().Add(time.Hour))
	ctx := context.Background()
	hash := repository.HashWaitToken(token)

	resumed, queued, err := store.ResumeWait(ctx, "", hash, json.RawMessage(`{"approved":true}`), time.Now().UTC())
	if err != nil {
		t.Fatalf("ResumeWait() error = %v", err)
	}
	if resumed.Outcome != repository.WaitOutcomeResumed || resumed.ConsumedAt == nil {
		t.Errorf("resumed wait = %+v, want it consumed as resumed", resumed)
	}
	if queued.Status != execution.StatusQueued {
		t.Errorf("execution status = %q, want queued for re-claim", queued.Status)
	}
	if string(resumed.ResumeOutput) != `{"approved":true}` {
		t.Errorf("resume output = %s, want the decision stored for the continued run", resumed.ResumeOutput)
	}

	// A second call on the same token is refused as already answered, not as
	// unknown: single-use must be observable, not just documented.
	if _, _, err := store.ResumeWait(ctx, "", hash, json.RawMessage(`{}`), time.Now().UTC()); !errors.Is(err, repository.ErrWaitConsumed) {
		t.Errorf("second ResumeWait() = %v, want ErrWaitConsumed", err)
	}

	if _, err := store.FindWaitByToken(ctx, repository.HashWaitToken("no-such-token")); !errors.Is(err, repository.ErrWaitNotFound) {
		t.Errorf("unknown token = %v, want ErrWaitNotFound", err)
	}

	// A token looked up under another tenant answers exactly like an unknown
	// one, so tokens cannot oracle other tenants' executions.
	if _, _, err := store.ResumeWait(ctx, "wait-tenant-other", hash, json.RawMessage(`{}`), time.Now().UTC()); !errors.Is(err, repository.ErrWaitNotFound) {
		t.Errorf("wrong-tenant ResumeWait() = %v, want ErrWaitNotFound", err)
	}
}

func TestResumePastTheDeadlineIsRefusedStably(t *testing.T) {
	db := openWaitsSQLite(t)
	tenant, store, record := queueAndClaimWaitFixture(t, db, "wait-tenant-c", "wait_wf_c")
	deadline := time.Now().UTC().Add(time.Hour)
	token, _ := suspendWaitFixture(t, store, tenant, record, deadline)
	ctx := context.Background()
	hash := repository.HashWaitToken(token)

	past := deadline.Add(time.Minute)
	if _, _, err := store.ResumeWait(ctx, "", hash, json.RawMessage(`{}`), past); !errors.Is(err, repository.ErrWaitExpired) {
		t.Fatalf("late ResumeWait() = %v, want ErrWaitExpired", err)
	}
	// Refusals never consume: asking again gets the same answer, and the
	// execution is still waiting for the sweeper rather than half-resumed.
	if _, _, err := store.ResumeWait(ctx, "", hash, json.RawMessage(`{}`), past); !errors.Is(err, repository.ErrWaitExpired) {
		t.Errorf("repeated late ResumeWait() = %v, want ErrWaitExpired again", err)
	}
	parked, err := store.Get(ctx, tenant, record.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if parked.Status != execution.StatusWaiting {
		t.Errorf("execution status = %q, want it still waiting", parked.Status)
	}
}

func TestSettleExpiredWaitFailsByName(t *testing.T) {
	db := openWaitsSQLite(t)
	tenant, store, record := queueAndClaimWaitFixture(t, db, "wait-tenant-d", "wait_wf_d")
	deadline := time.Now().UTC().Add(time.Hour)
	_, wait := suspendWaitFixture(t, store, tenant, record, deadline)
	ctx := context.Background()

	settled, failed, err := store.SettleExpiredWait(ctx, wait.ID, repository.ExpiredResolution{}, deadline.Add(time.Minute))
	if err != nil {
		t.Fatalf("SettleExpiredWait() error = %v", err)
	}
	if settled.Outcome != repository.WaitOutcomeExpired {
		t.Errorf("outcome = %q, want expired", settled.Outcome)
	}
	if failed.Status != execution.StatusFailed || failed.FinishedAt == nil {
		t.Errorf("execution = %+v, want a finished failure", failed)
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(failed.Error, &failure); err != nil {
		t.Fatalf("decode failure: %v (%s)", err, failed.Error)
	}
	if failure.Code != repository.WaitExpiredCode {
		t.Errorf("failure code = %q, want %q", failure.Code, repository.WaitExpiredCode)
	}

	if _, _, err := store.SettleExpiredWait(ctx, wait.ID, repository.ExpiredResolution{}, deadline.Add(2*time.Minute)); !errors.Is(err, repository.ErrWaitConsumed) {
		t.Errorf("second settle = %v, want ErrWaitConsumed", err)
	}
}

func TestSettleExpiredWaitRequeuesTimers(t *testing.T) {
	db := openWaitsSQLite(t)
	tenant, store, record := queueAndClaimWaitFixture(t, db, "wait-tenant-e", "wait_wf_e")
	deadline := time.Now().UTC().Add(time.Hour)
	_, wait := suspendWaitFixture(t, store, tenant, record, deadline)
	ctx := context.Background()

	expired, err := store.ListExpiredWaits(ctx, deadline.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("ListExpiredWaits() error = %v", err)
	}
	if len(expired) != 1 || expired[0].ID != wait.ID {
		t.Fatalf("expired waits = %+v, want the one suspension", expired)
	}

	settled, queued, err := store.SettleExpiredWait(ctx, wait.ID, repository.ExpiredResolution{
		Requeue: true, ResumeOutput: json.RawMessage(`{"timer":"fired"}`),
	}, deadline.Add(time.Minute))
	if err != nil {
		t.Fatalf("SettleExpiredWait() error = %v", err)
	}
	if settled.Outcome != repository.WaitOutcomeResumed || queued.Status != execution.StatusQueued {
		t.Errorf("settled = %+v status %q, want resumed and queued", settled, queued.Status)
	}
	resume, err := store.LoadResumeState(ctx, tenant, record.ID)
	if err != nil {
		t.Fatalf("LoadResumeState() error = %v", err)
	}
	if string(resume.ResumeOutput) != `{"timer":"fired"}` {
		t.Errorf("resume output = %s, want the timer payload", resume.ResumeOutput)
	}
}

func TestCancelWaitingCompletesItAtOnce(t *testing.T) {
	db := openWaitsSQLite(t)
	tenant, store, record := queueAndClaimWaitFixture(t, db, "wait-tenant-f", "wait_wf_f")
	suspendWaitFixture(t, store, tenant, record, time.Now().UTC().Add(time.Hour))

	cancelled, err := store.Cancel(context.Background(), tenant, record.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if cancelled.Status != execution.StatusCancelled || cancelled.FinishedAt == nil {
		t.Errorf("cancelled = %+v, want a finished cancellation without a worker to observe it", cancelled)
	}
}

func TestSuspendedWaitSurvivesARestart(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "kilasflow.db")
	open := func() *database.DB {
		db, err := database.Open(context.Background(), config.Database{Driver: "sqlite", DSN: path}, quiet)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		if err := database.Migrate(db, quiet); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		return db
	}
	db := open()
	tenant, store, record := queueAndClaimWaitFixture(t, db, "wait-tenant-g", "wait_wf_g")
	token, _ := suspendWaitFixture(t, store, tenant, record, time.Now().UTC().Add(time.Hour))
	executionID := record.ID
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// A new process on the same file: nothing in memory survives, so the
	// resume below proves the wait is durable state, not a goroutine.
	db = open()
	t.Cleanup(func() { _ = db.Close() })
	store = repository.NewExecutionStore(db.DB)
	hash := repository.HashWaitToken(token)
	if _, err := store.FindWaitByToken(context.Background(), hash); err != nil {
		t.Fatalf("FindWaitByToken() after restart = %v", err)
	}
	if _, queued, err := store.ResumeWait(context.Background(), "", hash, json.RawMessage(`{}`), time.Now().UTC()); err != nil {
		t.Fatalf("ResumeWait() after restart = %v", err)
	} else if queued.Status != execution.StatusQueued || queued.ID != executionID {
		t.Errorf("queued = %+v, want the same execution re-queued", queued)
	}
}
