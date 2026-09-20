package repository_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// This file is the persistence half of BUG-1tj5wy and BUG-hfhzq6: the claim
// that used to hand a live execution to a second worker for ever, and the trace
// write that used to fail on the second row a node produced inside a loop.

// openClaimDB opens a migrated SQLite database for the claim and trace tests.
func openClaimDB(t *testing.T, name string) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), name),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// queueOneNodeExecution saves a manual-trigger workflow and queues one run of
// it, which is all a claim needs to have something to work with.
func queueOneNodeExecution(t *testing.T, db *database.DB, tenant repository.TenantScope, workflowID string) execution.Record {
	t.Helper()
	ctx := context.Background()
	if _, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID,
		Name:          "Claim fixture",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft(%q) error = %v", workflowID, err)
	}
	queued, err := repository.NewExecutionStore(db.DB).QueueManualLatest(ctx, tenant, workflowID, activationCatalog{
		"kilasflow.manual": {Type: "kilasflow.manual", Version: workflow.V(1), Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}},
	}, "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(%q) error = %v", workflowID, err)
	}
	return queued
}

// An execution whose worker keeps dying is settled as crashed once the reclaim
// cap is reached, instead of being run again for ever.
//
// Reclaiming is the recovery path for a worker that died: the successor clears
// the partial trace and runs the graph. When the worker dies for a reason the
// re-run reproduces — the collision in BUG-hfhzq6 was exactly that — the same
// path repeats the execution, and every side effect in it, once per lease
// period with no terminal state. The cap is what turns that into one crashed
// record, and this asserts both halves: the cap is reachable, and the row past
// it is settled rather than claimed.
func TestExecutionStoreSettlesAnExecutionPastTheReclaimCapAsCrashed(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-reclaim-cap"}
	store := repository.NewExecutionStore(db.DB)

	poison := queueOneNodeExecution(t, db, tenant, "wf_reclaim_cap")
	dead, _, claimed, err := store.ClaimNext(ctx, "dead-worker", time.Now().UTC().Add(-time.Second))
	if err != nil || !claimed {
		t.Fatalf("ClaimNext(dead) = (%v, %v), want the queued execution claimed", claimed, err)
	}
	// Each reclaim hands it to a worker that also never comes back, so the
	// execution stays running with an expired lease until the cap is passed.
	for reclaim := 1; reclaim <= repository.MaxExecutionReclaims; reclaim++ {
		record, _, claimed, err := store.ClaimNext(ctx, "scavenger", time.Now().UTC().Add(-time.Second))
		if err != nil || !claimed {
			t.Fatalf("reclaim %d of %d = (%v, %v), want the expired lease reclaimed", reclaim, repository.MaxExecutionReclaims, claimed, err)
		}
		if record.ID != poison.ID {
			t.Fatalf("reclaim %d claimed %q, want %q", reclaim, record.ID, poison.ID)
		}
		dead = record
	}
	// A partial trace under the last lease the execution had. A reclaim deletes
	// it; settling the row as crashed must not, because it is the only record
	// of what the abandoned attempts did.
	now := time.Now().UTC()
	if _, err := store.CreateNodeRun(ctx, tenant, execution.NodeRun{
		TenantID: tenant.ID, ExecutionID: poison.ID, NodeID: "manual",
		Attempt: 1, RunIndex: 0, Sequence: 1, Status: execution.StatusSucceeded,
		Input: json.RawMessage(`{}`), Output: json.RawMessage(`[[{}]]`),
		StartedAt: now, FinishedAt: &now, LeaseOwner: dead.LeaseOwner,
	}); err != nil {
		t.Fatalf("CreateNodeRun(partial) error = %v", err)
	}

	// One claim past the cap. The poisoned row is the oldest match, so it is
	// settled first and the loop moves on to the work queued behind it — a
	// settled row that still satisfied the predicate would wedge every later
	// claim on it instead.
	fresh := queueOneNodeExecution(t, db, tenant, "wf_reclaim_cap_fresh")
	claimedRecord, _, claimed, err := store.ClaimNext(ctx, "scavenger", time.Now().UTC().Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("ClaimNext(past cap) = (%v, %v), want the queued work behind the poisoned execution claimed", claimed, err)
	}
	if claimedRecord.ID != fresh.ID {
		t.Errorf("claimed %q, want %q: the poisoned execution must not be claimed again", claimedRecord.ID, fresh.ID)
	}

	crashed, err := store.Get(ctx, tenant, poison.ID)
	if err != nil {
		t.Fatalf("Get(poisoned) error = %v", err)
	}
	if got, want := crashed.Status, execution.StatusFailed; got != want {
		t.Errorf("poisoned execution status = %q, want %q: it must reach a terminal state, not be reclaimed for ever", got, want)
	}
	if !strings.Contains(string(crashed.Error), "execution.crashed") {
		t.Errorf("poisoned execution error = %s, want the crash reported with code execution.crashed", crashed.Error)
	}
	if crashed.FinishedAt == nil {
		t.Error("poisoned execution finishedAt is nil, want the crash timestamped")
	}
	if crashed.LeaseOwner != "" {
		t.Errorf("poisoned execution lease owner = %q, want it released", crashed.LeaseOwner)
	}
	if got, want := len(crashed.NodeRuns), 1; got != want {
		t.Errorf("poisoned execution node runs = %d, want %d kept as the evidence of the abandoned attempt", got, want)
	}

	// Terminal means terminal: no later claim may pick it up again.
	if record, _, claimed, err := store.ClaimNext(ctx, "scavenger", time.Now().UTC().Add(time.Minute)); err != nil || claimed {
		t.Fatalf("ClaimNext(after settlement) = (%q, %v, %v), want nothing claimable", record.ID, claimed, err)
	}
}

// A node that runs more than once inside a loop keeps a row per run, even
// though the runner stamps no run index on the rows it writes for a skipped
// branch or a failure.
//
// This is BUG-hfhzq6 at the write itself: every such row arrives as index 0,
// uidx_node_runs_attempt refuses the second one, and the failure to persist is
// what left the execution running long enough to be reclaimed and re-run
// without end. The write also has to be idempotent, because a trace can be
// written in segments — a suspension, an incremental writer — and the same row
// must not become a second one.
func TestExecutionStoreKeepsEveryTraceRowTheRunnerLeftWithoutARunIndex(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-trace-loop"}
	store := repository.NewExecutionStore(db.DB)

	queued := queueOneNodeExecution(t, db, tenant, "wf_trace_loop")
	claimed, _, ok, err := store.ClaimNext(ctx, "worker", time.Now().UTC().Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("ClaimNext() = (%v, %v), want the queued execution claimed", ok, err)
	}
	now := time.Now().UTC()
	// Three iterations of one loop, each recording the same node as skipped:
	// same node, same attempt, index 0 from the runner, a different sequence
	// per iteration.
	rows := make([]execution.NodeRun, 0, 3)
	for iteration, sequence := range []int{4, 8, 12} {
		rows = append(rows, execution.NodeRun{
			TenantID: tenant.ID, ExecutionID: queued.ID, NodeID: "branch",
			Attempt: 1, RunIndex: 0, Sequence: sequence, Status: execution.StatusSkipped,
			Input: json.RawMessage(`{"main":[]}`), Output: json.RawMessage(`[[]]`),
			StartedAt: now, FinishedAt: &now, LeaseOwner: claimed.LeaseOwner,
		})
		_ = iteration
	}
	stored, err := store.CreateNodeRuns(ctx, tenant, rows)
	if err != nil {
		t.Fatalf("CreateNodeRuns(loop trace) error = %v, want every row written: this is the collision that left the execution running for ever", err)
	}
	if len(stored) != len(rows) {
		t.Fatalf("stored rows = %d, want %d", len(stored), len(rows))
	}
	indexes := map[int]bool{}
	for _, row := range stored {
		if indexes[row.RunIndex] {
			t.Errorf("two stored rows share run index %d, want each run of the node kept apart", row.RunIndex)
		}
		indexes[row.RunIndex] = true
	}

	persisted, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	branch := 0
	present := map[int]bool{}
	for _, run := range persisted.NodeRuns {
		if run.NodeID != "branch" {
			continue
		}
		branch++
		present[run.Sequence] = true
	}
	if branch != len(rows) {
		t.Errorf("branch node runs = %d, want %d: every iteration's row must survive", branch, len(rows))
	}
	for _, sequence := range []int{4, 8, 12} {
		if !present[sequence] {
			t.Errorf("sequence %d is missing from the trace", sequence)
		}
	}

	// The same batch again is the same rows, not three more: a trace written in
	// overlapping segments must be idempotent by row identity.
	if _, err := store.CreateNodeRuns(ctx, tenant, rows); err != nil {
		t.Fatalf("CreateNodeRuns(same batch) error = %v, want the repeat to be a no-op", err)
	}
	repeated, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(repeated) error = %v", err)
	}
	if got, want := len(repeated.NodeRuns), len(rows); got != want {
		t.Errorf("node runs after the same batch twice = %d, want %d", got, want)
	}
}

// A renewal only succeeds for the worker that actually holds the execution.
func TestExecutionStoreExtendLeaseOnlyRenewsTheHolder(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-lease-extend"}
	store := repository.NewExecutionStore(db.DB)

	queueOneNodeExecution(t, db, tenant, "wf_lease_extend")
	holder, _, claimed, err := store.ClaimNext(ctx, "holder", time.Now().UTC().Add(-time.Second))
	if err != nil || !claimed {
		t.Fatalf("ClaimNext() = (%v, %v), want the execution claimed", claimed, err)
	}
	renewed := time.Now().UTC().Add(time.Minute)
	held, err := store.ExtendLease(ctx, tenant, holder.ID, holder.LeaseOwner, renewed)
	if err != nil || !held {
		t.Fatalf("ExtendLease(holder) = (%v, %v), want the lease renewed", held, err)
	}
	// The renewal is what keeps a second worker out, which is the whole point:
	// with the lease in the future again the execution is not claimable.
	if record, _, claimed, err := store.ClaimNext(ctx, "other", time.Now().UTC().Add(time.Minute)); err != nil || claimed {
		t.Fatalf("ClaimNext(after renewal) = (%q, %v, %v), want nothing claimable while the lease is live", record.ID, claimed, err)
	}

	// A worker whose lease was taken over cannot keep renewing it, or the fence
	// would mean nothing.
	if held, err := store.ExtendLease(ctx, tenant, holder.ID, "someone-else", renewed); err != nil || held {
		t.Fatalf("ExtendLease(stale owner) = (%v, %v), want it refused", held, err)
	}
}
