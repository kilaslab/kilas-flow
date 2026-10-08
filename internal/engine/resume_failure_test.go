package engine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// resumeFailureRewrite edits one field of a stored wait checkpoint, the way a
// checkpoint from another release or a corrupted row would read.
func resumeFailureRewrite(t *testing.T, db *database.DB, wait repository.Wait, field string, value any) {
	t.Helper()
	var checkpoint map[string]any
	if err := json.Unmarshal(wait.Checkpoint, &checkpoint); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	if _, present := checkpoint[field]; !present {
		t.Fatalf("the checkpoint has no %q field: %s", field, wait.Checkpoint)
	}
	checkpoint[field] = value
	rewritten, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatalf("encode checkpoint: %v", err)
	}
	if err := db.DB.Exec(`UPDATE "execution_waits" SET "checkpoint" = ? WHERE "id" = ?`, rewritten, wait.ID).Error; err != nil {
		t.Fatalf("rewrite checkpoint: %v", err)
	}
}

// resumeFailureMessage reads the message out of a stored structured error.
func resumeFailureMessage(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode stored error: %v (%s)", err, raw)
	}
	return payload.Message
}

// TestEveryRefusedResumeRecordsAFailedRunOnTheWait covers the resume failures
// that returned no result at all: the service's own (a checkpoint it cannot
// decode, a resume output the wait cannot take) and the runner's early ones
// (a checkpoint for another trigger, a suspended node the graph no longer
// has). Each failed the execution and recorded nothing, so the replay showed
// every node before the wait green with nothing marking where it stopped.
//
// Each now fails on the suspended node, as the refusals that already used
// failedResume do: one failed run carrying the refusal, and nothing after it.
func TestEveryRefusedResumeRecordsAFailedRunOnTheWait(t *testing.T) {
	approve := func(t *testing.T, worker *engine.Service, tenant repository.TenantScope, wait repository.Wait) {
		t.Helper()
		if _, _, err := worker.ResumeApproval(t.Context(), tenant.ID, wait.ResumeToken, engine.ApprovalDecision{
			Approved: true, DecidedBy: "tester", RespondedAt: time.Now().UTC(),
		}, false); err != nil {
			t.Fatalf("ResumeApproval() error = %v", err)
		}
	}
	for _, test := range []struct {
		name string
		// refuse makes the resume fail, then resumes the wait.
		refuse    func(t *testing.T, db *database.DB, worker *engine.Service, tenant repository.TenantScope, wait repository.Wait)
		node      string
		wantError string
	}{
		{
			name: "a checkpoint the service cannot decode",
			refuse: func(t *testing.T, db *database.DB, worker *engine.Service, tenant repository.TenantScope, wait repository.Wait) {
				resumeFailureRewrite(t, db, wait, "version", 999)
				approve(t, worker, tenant, wait)
			},
			node: "hold", wantError: "wait checkpoint version 999 is not supported",
		},
		{
			name: "a resume output with more streams than the wait returns",
			refuse: func(t *testing.T, _ *database.DB, worker *engine.Service, tenant repository.TenantScope, wait repository.Wait) {
				if _, _, err := worker.ResumeCall(t.Context(), tenant.ID, wait.ResumeToken,
					json.RawMessage(`[[{"json":{"a":1}}],[{"json":{"b":2}}]]`), false); err != nil {
					t.Fatalf("ResumeCall() error = %v", err)
				}
			},
			node: "hold", wantError: `resume output has 2 streams, node "hold" returns 1`,
		},
		{
			name: "a checkpoint for a different trigger",
			refuse: func(t *testing.T, db *database.DB, worker *engine.Service, tenant repository.TenantScope, wait repository.Wait) {
				resumeFailureRewrite(t, db, wait, "triggerNodeId", "elsewhere")
				approve(t, worker, tenant, wait)
			},
			node: "hold", wantError: "wait checkpoint is for a different trigger",
		},
		{
			name: "a suspended node the graph does not have",
			refuse: func(t *testing.T, db *database.DB, worker *engine.Service, tenant repository.TenantScope, wait repository.Wait) {
				resumeFailureRewrite(t, db, wait, "suspendNode", "gone")
				approve(t, worker, tenant, wait)
			},
			node: "gone", wantError: `suspended node "gone" is not in the compiled graph`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
			hold := &waitSuspender{calls: new(int), requests: &[]engine.Request{}, mode: engine.WaitModeApproval}
			var passCalls int
			waitRegisterFakes(t, executors, hold, &passCalls, &[]workflow.NodeInput{})
			saved, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, waitTestWorkflow())
			if err != nil {
				t.Fatalf("SaveDraft() error = %v", err)
			}
			queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("QueueManualLatest() error = %v", err)
			}
			worker := waitTestService(t, store, catalog, executors, "worker-0")
			if worked, err := worker.RunOnce(ctx); err != nil || !worked {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
			}
			wait, err := store.FindActiveWait(ctx, tenant, queued.ID)
			if err != nil {
				t.Fatalf("FindActiveWait() error = %v", err)
			}
			test.refuse(t, db, worker, tenant, wait)
			worker = waitTestService(t, store, catalog, executors, "worker-1")
			if worked, err := worker.RunOnce(ctx); err != nil || !worked {
				t.Fatalf("RunOnce(refused resume) = (%v, %v), want (true, nil)", worked, err)
			}

			failed, err := store.Get(ctx, tenant, queued.ID)
			if err != nil {
				t.Fatalf("Get(failed) error = %v", err)
			}
			if failed.Status != execution.StatusFailed {
				t.Fatalf("status = %q, want failed on the refused resume (error %s)", failed.Status, failed.Error)
			}
			if message := resumeFailureMessage(t, failed.Error); !strings.Contains(message, test.wantError) {
				t.Errorf("execution error = %q, want the refusal %q", message, test.wantError)
			}
			var failedRuns []execution.NodeRun
			for _, run := range failed.NodeRuns {
				if run.Status == execution.StatusFailed {
					failedRuns = append(failedRuns, run)
				}
			}
			if len(failedRuns) != 1 {
				t.Fatalf("the trace holds %d failed runs, want the wait's alone (runs %s)", len(failedRuns), describeRuns(failed))
			}
			run := failedRuns[0]
			if run.NodeID != test.node || run.Attempt != 1 {
				t.Errorf("failed run = node %q, attempt %d; want %q, first attempt", run.NodeID, run.Attempt, test.node)
			}
			if message := resumeFailureMessage(t, run.Error); !strings.Contains(message, test.wantError) {
				t.Errorf("failed run error = %q, want the refusal %q", message, test.wantError)
			}
			// Nothing ran after the refusal: the failed wait is the end of the
			// trace, and the nodes before it are the ones that really ran.
			if last := failed.NodeRuns[len(failed.NodeRuns)-1]; last.Sequence != run.Sequence {
				t.Errorf("the trace ends on node %q, want the failed wait (runs %s)", last.NodeID, describeRuns(failed))
			}
			for _, earlier := range failed.NodeRuns {
				if earlier.NodeID == "done" {
					t.Errorf("done ran after a refused resume (runs %s)", describeRuns(failed))
				}
			}
		})
	}
}
