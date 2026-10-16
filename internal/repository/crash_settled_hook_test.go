package repository_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// An execution settled as crashed inside ClaimNext is reported to whoever
// registered for it, once, with the record as persisted. The engine never sees
// that row otherwise (it is failed inside the claim transaction and skipped),
// and it is the one terminal transition that would leave no log line
// (FEAT-53pa9a).
func TestExecutionStoreReportsACrashSettledExecutionToItsHook(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-crash-hook"}
	store := repository.NewExecutionStore(db.DB)

	var settled []execution.Record
	store.OnCrashSettled(func(record execution.Record) { settled = append(settled, record) })

	poison := queueOneNodeExecution(t, db, tenant, "wf_crash_hook")
	if _, _, claimed, err := store.ClaimNext(ctx, "dead-worker", time.Now().UTC().Add(-time.Second)); err != nil || !claimed {
		t.Fatalf("ClaimNext(dead) = (%v, %v), want the queued execution claimed", claimed, err)
	}
	for reclaim := 1; reclaim <= repository.MaxExecutionReclaims; reclaim++ {
		if _, _, claimed, err := store.ClaimNext(ctx, "scavenger", time.Now().UTC().Add(-time.Second)); err != nil || !claimed {
			t.Fatalf("reclaim %d = (%v, %v), want the expired lease reclaimed", reclaim, claimed, err)
		}
	}
	if len(settled) != 0 {
		t.Fatalf("hook called %d times before the cap was passed", len(settled))
	}
	if _, _, claimed, err := store.ClaimNext(ctx, "scavenger", time.Now().UTC().Add(time.Minute)); err != nil || claimed {
		t.Fatalf("ClaimNext(past cap) = (%v, %v), want the crashed row settled and nothing claimed", claimed, err)
	}

	if len(settled) != 1 {
		t.Fatalf("hook called %d times, want exactly once", len(settled))
	}
	record := settled[0]
	if record.ID != poison.ID || record.TenantID != tenant.ID || record.WorkflowID != "wf_crash_hook" {
		t.Errorf("hook record = %s/%s/%s, want the poisoned execution %s", record.TenantID, record.WorkflowID, record.ID, poison.ID)
	}
	if record.Status != execution.StatusFailed || record.FinishedAt == nil || record.LeaseOwner != "" {
		t.Errorf("hook record status %q finishedAt %v lease %q, want a settled failed record", record.Status, record.FinishedAt, record.LeaseOwner)
	}
	if !strings.Contains(string(record.Error), "execution.crashed") {
		t.Errorf("hook record error = %s, want code execution.crashed", record.Error)
	}
}
