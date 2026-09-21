package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// The drv- tenant prefix is what eachDriver's PostgreSQL cleanup deletes from
// webhook_deliveries, so a run on the shared server leaves nothing behind.

// deliveryRow reads one delivery straight off the table, bypassing the store
// under test.
func deliveryRow(t *testing.T, db *database.DB, route, deliveryID string) (tenantID, executionID string) {
	t.Helper()
	err := db.Raw(`SELECT tenant_id, execution_id FROM webhook_deliveries WHERE route = ? AND delivery_id = ?`, route, deliveryID).
		Row().Scan(&tenantID, &executionID)
	if err != nil {
		t.Fatalf("read the delivery %s/%s: %v", route, deliveryID, err)
	}
	return tenantID, executionID
}

// A claim is stamped with the tenant that owns the route it arrived on, which
// is the only thing a tenant purge can later delete it by.
func TestClaimDeliveryRecordsTheTenantThatOwnsTheRoute(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewWorkflowStore(db.DB)

		owner, claimed, err := store.ClaimDelivery(ctx, "drv-claim-a", "drv-claim-route", "delivery-1", "", time.Minute)
		if err != nil || !claimed || owner != "" {
			t.Fatalf("ClaimDelivery() = (%q, %v, %v), want the first claim to win with no execution yet", owner, claimed, err)
		}

		tenantID, _ := deliveryRow(t, db, "drv-claim-route", "delivery-1")
		if tenantID != "drv-claim-a" {
			t.Errorf("the claim was stamped with tenant %q, want %q", tenantID, "drv-claim-a")
		}
	})
}

// A retry is answered with the execution the first delivery queued.
func TestClaimDeliveryAnswersARetryFromTheSameTenantWithTheOriginalExecution(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewWorkflowStore(db.DB)

		if _, claimed, err := store.ClaimDelivery(ctx, "drv-retry-a", "drv-retry-route", "delivery-1", "exec-1", time.Minute); err != nil || !claimed {
			t.Fatalf("first ClaimDelivery() = (%v, %v), want it to win", claimed, err)
		}
		owner, claimed, err := store.ClaimDelivery(ctx, "drv-retry-a", "drv-retry-route", "delivery-1", "", time.Minute)
		if err != nil || claimed || owner != "exec-1" {
			t.Errorf("second ClaimDelivery() = (%q, %v, %v), want the original execution and a lost claim", owner, claimed, err)
		}
	})
}

// A delivery is never attributed to nobody. An empty tenant is exactly the row a
// purge could never reach, and the schema keeps an empty-string DEFAULT on both
// dialects, so this refusal is the guard.
func TestClaimDeliveryRefusesAnEmptyTenant(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewWorkflowStore(db.DB)

		if _, _, err := store.ClaimDelivery(ctx, "", "drv-empty-route", "delivery-1", "", time.Minute); err == nil {
			t.Error("ClaimDelivery(empty tenant) = nil, want an error")
		}
		var rows int64
		if err := db.Raw(`SELECT COUNT(*) FROM webhook_deliveries WHERE route = ?`, "drv-empty-route").Scan(&rows).Error; err != nil {
			t.Fatalf("count deliveries: %v", err)
		}
		if rows != 0 {
			t.Errorf("a refused claim left %d rows behind", rows)
		}

		// With no delivery id there is nothing to dedupe on, so the claim is a
		// no-op that never looks at the tenant. The webhook handler already
		// skips the call in that case; this pins the order the check runs in.
		owner, claimed, err := store.ClaimDelivery(ctx, "", "drv-empty-route", "", "", time.Minute)
		if err != nil || !claimed || owner != "" {
			t.Errorf("ClaimDelivery(no delivery id) = (%q, %v, %v), want the no-op that always wins", owner, claimed, err)
		}
	})
}

// Two tenants that ever share a route label collide on the unique index. The
// loser must fail open — an error, so the handler runs the delivery anyway —
// and never be answered with the winner's execution.
func TestClaimDeliveryNeverReturnsAnotherTenantsExecution(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewWorkflowStore(db.DB)

		if _, claimed, err := store.ClaimDelivery(ctx, "drv-cross-a", "drv-cross-route", "delivery-1", "exec-a", time.Minute); err != nil || !claimed {
			t.Fatalf("tenant A's ClaimDelivery() = (%v, %v), want it to win", claimed, err)
		}

		owner, claimed, err := store.ClaimDelivery(ctx, "drv-cross-b", "drv-cross-route", "delivery-1", "", time.Minute)
		if err == nil {
			t.Fatalf("tenant B's ClaimDelivery() = (%q, %v, nil), want an error: the row it conflicts with is not its own", owner, claimed)
		}
		if owner == "exec-a" {
			t.Errorf("tenant B was handed tenant A's execution %q", owner)
		}

		tenantID, executionID := deliveryRow(t, db, "drv-cross-route", "delivery-1")
		if tenantID != "drv-cross-a" || executionID != "exec-a" {
			t.Errorf("the surviving claim = (%q, %q), want tenant A's, untouched", tenantID, executionID)
		}
	})
}

// Attaching the execution to a claim is scoped by tenant as well as by route and
// delivery id, so another tenant's call cannot rewrite the claim.
func TestRecordDeliveryExecutionIsTenantScoped(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewWorkflowStore(db.DB)

		if _, claimed, err := store.ClaimDelivery(ctx, "drv-record-a", "drv-record-route", "delivery-1", "", time.Minute); err != nil || !claimed {
			t.Fatalf("ClaimDelivery() = (%v, %v), want it to win", claimed, err)
		}

		if err := store.RecordDeliveryExecution(ctx, "drv-record-b", "drv-record-route", "delivery-1", "exec-b"); err != nil {
			t.Fatalf("RecordDeliveryExecution(other tenant) error = %v", err)
		}
		if _, executionID := deliveryRow(t, db, "drv-record-route", "delivery-1"); executionID != "" {
			t.Errorf("another tenant rewrote the claim's execution to %q, want it unchanged", executionID)
		}

		if err := store.RecordDeliveryExecution(ctx, "drv-record-a", "drv-record-route", "delivery-1", "exec-a"); err != nil {
			t.Fatalf("RecordDeliveryExecution(owner) error = %v", err)
		}
		if _, executionID := deliveryRow(t, db, "drv-record-route", "delivery-1"); executionID != "exec-a" {
			t.Errorf("the owner's execution = %q, want exec-a", executionID)
		}

		if err := store.RecordDeliveryExecution(ctx, "", "drv-record-route", "delivery-1", "exec-x"); err == nil {
			t.Error("RecordDeliveryExecution(empty tenant) = nil, want an error")
		}
	})
}
