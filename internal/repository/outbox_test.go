package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// subscribeAll routes every event of the named tenant to one endpoint and
// declines every other tenant.
func subscribeAll(tenantID, endpoint string) repository.OutboxRouter {
	return func(tenant, _ string, _ []byte) (string, bool) {
		return endpoint, tenant == tenantID
	}
}

// outboxRows lists every outbox row of one tenant, oldest first.
func outboxRows(t *testing.T, db *database.DB, tenantID string) []repository.OutboundEvent {
	t.Helper()
	listed, err := repository.NewOutboxStore(db.DB).List(context.Background(), repository.TenantScope{ID: tenantID}, repository.OutboundFilter{Limit: repository.MaxOutboundPageSize})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for left, right := 0, len(listed)-1; left < right; left, right = left+1, right-1 {
		listed[left], listed[right] = listed[right], listed[left]
	}
	return listed
}

// settle claims the tenant's queued execution and finishes it with status.
func settle(t *testing.T, store *repository.GORMExecutionStore, tenant repository.TenantScope, status execution.Status, errorPayload string) execution.Record {
	t.Helper()
	ctx := context.Background()
	claimed, _, found, err := store.ClaimNext(ctx, "worker-outbox", time.Now().Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("ClaimNext() = (%v, %v), want the queued execution", found, err)
	}
	finished := time.Now().UTC()
	claimed.Status = status
	claimed.Output = json.RawMessage(`null`)
	claimed.Error = json.RawMessage(errorPayload)
	claimed.FinishedAt = &finished
	updated, err := store.UpdateRuntime(ctx, tenant, claimed)
	if err != nil {
		t.Fatalf("UpdateRuntime(%s) error = %v", status, err)
	}
	return updated
}

// A settled execution writes its outcome event in the transaction that
// settles it: a failed run an execution.failed row carrying the error, a
// succeeded run an execution.completed row, each with a stable id the body
// repeats.
func TestOutboxWritesTheExecutionOutcomeWithTheTerminalStatus(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		tenant := repository.TenantScope{ID: "drv-outbox-exec"}
		store := repository.NewExecutionStore(db.DB).WithOutbox(subscribeAll(tenant.ID, "https://host.example/hooks"))

		queueOneNodeExecution(t, db, tenant, "wf_outbox_fail")
		failed := settle(t, store, tenant, execution.StatusFailed, `{"code":"node.failed","message":"boom"}`)
		queueClaimFixture(t, db, tenant.ID, "wf_outbox_ok", 1)
		succeeded := settle(t, store, tenant, execution.StatusSucceeded, `null`)

		rows := outboxRows(t, db, tenant.ID)
		if len(rows) != 2 {
			t.Fatalf("outbox rows = %d, want 2: %#v", len(rows), rows)
		}
		byType := map[string]repository.OutboundEvent{}
		for _, row := range rows {
			byType[row.EventType] = row
		}
		failedRow, ok := byType[repository.OutboundExecutionFailed]
		if !ok || failedRow.SubjectID != failed.ID {
			t.Fatalf("execution.failed row = %#v, want one for %s", failedRow, failed.ID)
		}
		if failedRow.Status != repository.OutboundPending || failedRow.Endpoint != "https://host.example/hooks" || failedRow.NextAttemptAt == nil || failedRow.Attempts != 0 {
			t.Fatalf("execution.failed row is not a due pending delivery: %#v", failedRow)
		}
		var body struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			TenantID string `json:"tenantId"`
			Data     struct {
				ExecutionID string          `json:"executionId"`
				WorkflowID  string          `json:"workflowId"`
				Status      string          `json:"status"`
				Error       json.RawMessage `json:"error"`
			} `json:"data"`
		}
		if err := json.Unmarshal(failedRow.Payload, &body); err != nil {
			t.Fatalf("payload is not JSON: %v (%s)", err, failedRow.Payload)
		}
		if body.ID != failedRow.ID || body.Type != repository.OutboundExecutionFailed || body.TenantID != tenant.ID ||
			body.Data.ExecutionID != failed.ID || body.Data.WorkflowID != "wf_outbox_fail" || body.Data.Status != "failed" {
			t.Fatalf("payload = %s, want the failed execution under event id %s", failedRow.Payload, failedRow.ID)
		}
		if string(body.Data.Error) == "" || string(body.Data.Error) == "null" {
			t.Fatalf("execution.failed payload carries no error: %s", failedRow.Payload)
		}
		completed, ok := byType[repository.OutboundExecutionCompleted]
		if !ok || completed.SubjectID != succeeded.ID {
			t.Fatalf("execution.completed row = %#v, want one for %s", completed, succeeded.ID)
		}
	})
}

// A tenant the router declines writes nothing, and a cancelled execution has
// no outcome event at all.
func TestOutboxWritesNothingForAnUnsubscribedTenantOrACancellation(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-outbox-quiet"}
	store := repository.NewExecutionStore(db.DB).WithOutbox(subscribeAll("someone-else", "https://host.example/hooks"))
	queueOneNodeExecution(t, db, tenant, "wf_outbox_quiet")
	settle(t, store, tenant, execution.StatusFailed, `{"code":"node.failed","message":"boom"}`)
	if rows := outboxRows(t, db, tenant.ID); len(rows) != 0 {
		t.Fatalf("an unsubscribed tenant wrote %d outbox rows", len(rows))
	}

	subscribed := repository.TenantScope{ID: "tenant-outbox-cancel"}
	store.WithOutbox(subscribeAll(subscribed.ID, "https://host.example/hooks"))
	queueOneNodeExecution(t, db, subscribed, "wf_outbox_cancel")
	claimed, _, found, err := store.ClaimNext(ctx, "worker-cancel", time.Now().Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("ClaimNext() = (%v, %v)", found, err)
	}
	if _, err := store.Cancel(ctx, subscribed, claimed.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	finished := time.Now().UTC()
	claimed.Status = execution.StatusSucceeded
	claimed.Output = json.RawMessage(`null`)
	claimed.Error = json.RawMessage(`null`)
	claimed.FinishedAt = &finished
	if _, err := store.UpdateRuntime(ctx, subscribed, claimed); err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}
	if rows := outboxRows(t, db, subscribed.ID); len(rows) != 0 {
		t.Fatalf("a cancelled execution wrote outbox rows: %#v", rows)
	}
}

// An execution settled as crashed by the claim writes execution.failed in the
// claim transaction, the one terminal transition the engine never sees.
func TestOutboxWritesExecutionFailedForACrashSettledExecution(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-outbox-crash"}
	store := repository.NewExecutionStore(db.DB).WithOutbox(subscribeAll(tenant.ID, "https://host.example/hooks"))
	poison := queueOneNodeExecution(t, db, tenant, "wf_outbox_crash")
	for range repository.MaxExecutionReclaims + 1 {
		if _, _, claimed, err := store.ClaimNext(ctx, "dying-worker", time.Now().UTC().Add(-time.Second)); err != nil || !claimed {
			t.Fatalf("ClaimNext() = (%v, %v)", claimed, err)
		}
	}
	if _, _, claimed, err := store.ClaimNext(ctx, "scavenger", time.Now().UTC().Add(time.Minute)); err != nil || claimed {
		t.Fatalf("ClaimNext() past the cap = (%v, %v), want the row settled, not claimed", claimed, err)
	}
	rows := outboxRows(t, db, tenant.ID)
	if len(rows) != 1 || rows[0].EventType != repository.OutboundExecutionFailed || rows[0].SubjectID != poison.ID {
		t.Fatalf("outbox rows = %#v, want one execution.failed for %s", rows, poison.ID)
	}
}

// Publishing and unpublishing write workflow.activated and
// workflow.deactivated in their own transactions, and a publish that is
// refused writes nothing: the event and the change commit or roll back
// together.
func TestOutboxWritesWorkflowActivationAndDeactivation(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-outbox-wf"}
		workflows := repository.NewWorkflowStore(db.DB).WithOutbox(subscribeAll(tenant.ID, "https://host.example/hooks"))
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_outbox_publish", Name: "Publish me",
			Nodes:       []workflow.Node{{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}},
			Connections: []workflow.Connection{}, Settings: map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		// An empty catalogue refuses the document, so the publish rolls back.
		if _, err := workflows.Activate(ctx, tenant, saved.ID, activationCatalog{}); err == nil {
			t.Fatal("Activate() with an empty catalogue succeeded, want it refused")
		}
		if rows := outboxRows(t, db, tenant.ID); len(rows) != 0 {
			t.Fatalf("a refused publish wrote outbox rows: %#v", rows)
		}
		active, err := workflows.Activate(ctx, tenant, saved.ID, driverCatalog())
		if err != nil {
			t.Fatalf("Activate() error = %v", err)
		}
		if _, err := workflows.Deactivate(ctx, tenant, saved.ID); err != nil {
			t.Fatalf("Deactivate() error = %v", err)
		}
		// Deactivating an inactive workflow changes nothing and says nothing.
		if _, err := workflows.Deactivate(ctx, tenant, saved.ID); err != nil {
			t.Fatalf("second Deactivate() error = %v", err)
		}
		rows := outboxRows(t, db, tenant.ID)
		if len(rows) != 2 {
			t.Fatalf("outbox rows = %#v, want activated then deactivated", rows)
		}
		types := map[string]repository.OutboundEvent{}
		for _, row := range rows {
			types[row.EventType] = row
		}
		activated, ok := types[repository.OutboundWorkflowActivated]
		if !ok || activated.SubjectID != saved.ID {
			t.Fatalf("no workflow.activated row for %s: %#v", saved.ID, rows)
		}
		var body struct {
			Data struct {
				WorkflowID string `json:"workflowId"`
				VersionID  string `json:"versionId"`
				Name       string `json:"name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(activated.Payload, &body); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if body.Data.WorkflowID != saved.ID || body.Data.VersionID == "" || active.ActiveVersion == nil || body.Data.VersionID != active.ActiveVersion.ID || body.Data.Name != "Publish me" {
			t.Fatalf("workflow.activated payload = %s", activated.Payload)
		}
		if _, ok := types[repository.OutboundWorkflowDeactivated]; !ok {
			t.Fatalf("no workflow.deactivated row: %#v", rows)
		}
	})
}

// enqueueDirect writes one due outbox row through a publish, which is the
// cheapest state change that writes one.
func enqueueDirect(t *testing.T, db *database.DB, tenant repository.TenantScope, workflowID string) repository.OutboundEvent {
	t.Helper()
	ctx := context.Background()
	workflows := repository.NewWorkflowStore(db.DB).WithOutbox(func(tenantID, eventType string, _ []byte) (string, bool) {
		return "https://host.example/hooks", tenantID == tenant.ID && eventType == repository.OutboundWorkflowActivated
	})
	if _, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: workflowID, Name: "Outbox fixture",
		Nodes:       []workflow.Node{{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := workflows.Activate(ctx, tenant, workflowID, driverCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	for _, row := range outboxRows(t, db, tenant.ID) {
		if row.SubjectID == workflowID {
			return row
		}
	}
	t.Fatalf("activating %s wrote no outbox row", workflowID)
	return repository.OutboundEvent{}
}

// A claimed row is fenced to its dispatcher until the lease expires; the
// outcome is written only by the lease holder; a retry is not due before its
// time; and a delivered row is never claimed again.
func TestOutboxClaimLeasesARowToOneDispatcher(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-outbox-claim"}
		row := enqueueDirect(t, db, tenant, "wf_outbox_claim")
		outbox := repository.NewOutboxStore(db.DB)
		now := time.Now().UTC().Add(time.Second)

		first, found, err := outbox.ClaimDue(ctx, now, "dispatcher-a", time.Minute)
		if err != nil || !found || first.ID != row.ID || first.LeaseOwner != "dispatcher-a" {
			t.Fatalf("ClaimDue(a) = (%#v, %v, %v), want %s leased to a", first, found, err, row.ID)
		}
		if _, found, err := outbox.ClaimDue(ctx, now, "dispatcher-b", time.Minute); err != nil || found {
			t.Fatalf("ClaimDue(b) under a's lease = (%v, %v), want nothing", found, err)
		}
		if err := outbox.RecordAttempt(ctx, repository.OutboundAttempt{ID: row.ID, WorkerID: "dispatcher-b", Delivered: true, StatusCode: 200, At: now}); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("RecordAttempt by a non-holder = %v, want ErrNotFound", err)
		}
		retryAt := now.Add(10 * time.Minute)
		if err := outbox.RecordAttempt(ctx, repository.OutboundAttempt{ID: row.ID, WorkerID: "dispatcher-a", StatusCode: 503, Error: "the endpoint answered 503", NextAttemptAt: retryAt, At: now}); err != nil {
			t.Fatalf("RecordAttempt(retry) error = %v", err)
		}
		if _, found, err := outbox.ClaimDue(ctx, now.Add(time.Minute), "dispatcher-b", time.Minute); err != nil || found {
			t.Fatalf("ClaimDue() before the retry is due = (%v, %v), want nothing", found, err)
		}
		logged := outboxRows(t, db, tenant.ID)[0]
		if logged.Attempts != 1 || logged.LastStatusCode != 503 || logged.LastError == "" || logged.NextAttemptAt == nil || logged.Status != repository.OutboundPending || logged.LeaseOwner != "" {
			t.Fatalf("delivery log after a 503 = %#v", logged)
		}

		second, found, err := outbox.ClaimDue(ctx, retryAt.Add(time.Second), "dispatcher-b", time.Minute)
		if err != nil || !found || second.ID != row.ID {
			t.Fatalf("ClaimDue() once due = (%v, %v), want the row again", found, err)
		}
		if err := outbox.RecordAttempt(ctx, repository.OutboundAttempt{ID: row.ID, WorkerID: "dispatcher-b", Delivered: true, StatusCode: 204, At: retryAt}); err != nil {
			t.Fatalf("RecordAttempt(delivered) error = %v", err)
		}
		if _, found, err := outbox.ClaimDue(ctx, retryAt.Add(time.Hour), "dispatcher-a", time.Minute); err != nil || found {
			t.Fatalf("ClaimDue() after delivery = (%v, %v), want nothing", found, err)
		}
		logged = outboxRows(t, db, tenant.ID)[0]
		if logged.Status != repository.OutboundDelivered || logged.Attempts != 2 || logged.LastStatusCode != 204 || logged.DeliveredAt == nil || logged.NextAttemptAt != nil {
			t.Fatalf("delivery log after delivery = %#v", logged)
		}
	})
}

// A dispatcher that dies holding a lease leaves the row to the next one once
// the lease expires, with the unfinished attempt not counted.
func TestOutboxExpiredLeaseIsClaimableAgain(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-outbox-expired"}
	row := enqueueDirect(t, db, tenant, "wf_outbox_expired")
	outbox := repository.NewOutboxStore(db.DB)
	now := time.Now().UTC().Add(time.Second)
	if _, found, err := outbox.ClaimDue(ctx, now, "dead-dispatcher", time.Minute); err != nil || !found {
		t.Fatalf("ClaimDue(dead) = (%v, %v)", found, err)
	}
	taken, found, err := outbox.ClaimDue(ctx, now.Add(2*time.Minute), "survivor", time.Minute)
	if err != nil || !found || taken.ID != row.ID || taken.Attempts != 0 {
		t.Fatalf("ClaimDue() after the lease expired = (%#v, %v, %v), want the row with no attempt counted", taken, found, err)
	}
}

// The delivery log is per tenant, and retention removes every row older than
// the cutoff whatever its state.
func TestOutboxLogIsTenantScopedAndPruned(t *testing.T) {
	ctx := context.Background()
	db := openClaimDB(t, "kilasflow.db")
	mine := repository.TenantScope{ID: "tenant-outbox-mine"}
	theirs := repository.TenantScope{ID: "tenant-outbox-theirs"}
	enqueueDirect(t, db, mine, "wf_outbox_mine")
	enqueueDirect(t, db, theirs, "wf_outbox_theirs")
	if rows := outboxRows(t, db, mine.ID); len(rows) != 1 || rows[0].TenantID != mine.ID {
		t.Fatalf("my delivery log = %#v, want my one row", rows)
	}
	outbox := repository.NewOutboxStore(db.DB)
	if _, err := outbox.List(ctx, repository.TenantScope{}, repository.OutboundFilter{}); err == nil {
		t.Fatal("List() without a tenant succeeded")
	}
	if delivered, err := outbox.List(ctx, mine, repository.OutboundFilter{Status: repository.OutboundDelivered}); err != nil || len(delivered) != 0 {
		t.Fatalf("List(delivered) = (%d, %v), want none", len(delivered), err)
	}
	if pruned, err := outbox.PruneOutbound(ctx, time.Now().Add(-time.Hour)); err != nil || pruned != 0 {
		t.Fatalf("PruneOutbound(an hour ago) = (%d, %v), want nothing pruned", pruned, err)
	}
	if pruned, err := outbox.PruneOutbound(ctx, time.Now().Add(time.Hour)); err != nil || pruned != 2 {
		t.Fatalf("PruneOutbound(an hour ahead) = (%d, %v), want both rows", pruned, err)
	}
}
