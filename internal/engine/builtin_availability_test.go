package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Built-in availability rides the same gate as a scoped pack node: the tenant's
// catalogue view. These tests prove the three places the engine compiles for a
// tenant (activation, the manual run that follows it, the worker's claim) refuse
// a hidden built-in the way they refuse a scoped-away pack node, and that a
// narrowing after the run was queued still stops it at claim.

const hiddenBuiltin = "kilasflow.set"

// mutableLists is a tenant-list source a test can change between phases.
type mutableLists struct {
	mu       sync.Mutex
	byTenant map[string]node.TenantAvailability
}

func (lists *mutableLists) set(tenant string, own node.TenantAvailability) {
	lists.mu.Lock()
	defer lists.mu.Unlock()
	lists.byTenant[tenant] = own
}

func (lists *mutableLists) TenantAvailability(tenantID string) (node.TenantAvailability, error) {
	lists.mu.Lock()
	defer lists.mu.Unlock()
	return lists.byTenant[tenantID], nil
}

func TestADeniedBuiltinIsRefusedAtActivationAndRunAndRunsForAnotherTenant(t *testing.T) {
	setup := newComposition(t)
	if err := setup.catalog.ApplyAvailability(node.AvailabilityRules{
		Deny: []node.AvailabilityRule{{Type: hiddenBuiltin, Tenant: "tenant-b"}},
	}); err != nil {
		t.Fatalf("ApplyAvailability() error = %v", err)
	}
	ctx := context.Background()

	open := repository.TenantScope{ID: "tenant-a"}
	openView := workflow.CatalogFor(setup.catalog, open.ID)
	stored, err := setup.workflows.SaveDraft(ctx, open, scopedWorkflow("Open", hiddenBuiltin))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := setup.workflows.Activate(ctx, open, stored.ID, openView); err != nil {
		t.Fatalf("Activate() error = %v, want the built-in to compile for a tenant it is not denied to", err)
	}
	queued, err := setup.executions.QueueManualLatest(ctx, open, stored.ID, openView, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if record := runOnce(t, setup, open, queued.ID); record.Status != execution.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded (error %s)", record.Status, record.Error)
	}

	denied := repository.TenantScope{ID: "tenant-b"}
	deniedView := workflow.CatalogFor(setup.catalog, denied.ID)
	copied, err := setup.workflows.SaveDraft(ctx, denied, scopedWorkflow("Denied", hiddenBuiltin))
	if err != nil {
		t.Fatalf("SaveDraft(denied) error = %v, want a draft to accept a hidden type", err)
	}
	if _, err := setup.workflows.Activate(ctx, denied, copied.ID, deniedView); issueCode(t, err, "set") != workflow.ErrorNodeNotAvailable {
		t.Fatalf("Activate() error = %v, want node.not_available", err)
	}
	if _, err := setup.executions.QueueManualLatest(ctx, denied, copied.ID, deniedView, "", json.RawMessage(`{}`)); issueCode(t, err, "set") != workflow.ErrorNodeNotAvailable {
		t.Fatalf("QueueManualLatest() error = %v, want node.not_available", err)
	}
}

// The worker is the authoritative gate for a built-in too: work that reached the
// queue past a compile fails at claim with the not-available text and runs
// nothing.
func TestTheWorkerRefusesADeniedBuiltinQueuedPastTheAPI(t *testing.T) {
	setup := newComposition(t)
	ctx := context.Background()
	denied := repository.TenantScope{ID: "tenant-b"}

	stored, err := setup.workflows.SaveDraft(ctx, denied, scopedWorkflow("Bypassed", hiddenBuiltin))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	// Activated and queued before the operator denied the type: the raw
	// catalogue compiled it.
	if _, err := setup.workflows.Activate(ctx, denied, stored.ID, setup.catalog); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	queued, err := setup.executions.QueueManualLatest(ctx, denied, stored.ID, setup.catalog, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if err := setup.catalog.ApplyAvailability(node.AvailabilityRules{
		Deny: []node.AvailabilityRule{{Type: hiddenBuiltin}},
	}); err != nil {
		t.Fatalf("ApplyAvailability() error = %v", err)
	}

	record := runOnce(t, setup, denied, queued.ID)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %q, want failed", record.Status)
	}
	if !strings.Contains(string(record.Error), "not available to this workspace") {
		t.Errorf("error = %s, want it to say the node is not available", record.Error)
	}
	if len(record.NodeRuns) != 0 {
		t.Fatalf("node runs = %d, want nothing executed", len(record.NodeRuns))
	}
}

// A tenant's own deny list, set after its run was queued, stops that run at claim:
// the worker reads the lists through the same source the API does.
func TestATenantsOwnDenyListStopsAQueuedRunAtClaim(t *testing.T) {
	setup := newComposition(t)
	lists := &mutableLists{byTenant: map[string]node.TenantAvailability{}}
	setup.catalog.SetTenantAvailabilitySource(lists)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-b"}

	stored, err := setup.workflows.SaveDraft(ctx, tenant, scopedWorkflow("Queued", hiddenBuiltin))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	view := workflow.CatalogFor(setup.catalog, tenant.ID)
	if _, err := setup.workflows.Activate(ctx, tenant, stored.ID, view); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	queued, err := setup.executions.QueueManualLatest(ctx, tenant, stored.ID, view, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}

	lists.set(tenant.ID, node.TenantAvailability{Deny: []string{hiddenBuiltin}})

	record := runOnce(t, setup, tenant, queued.ID)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %q, want failed", record.Status)
	}
	if !strings.Contains(string(record.Error), "not available to this workspace") {
		t.Errorf("error = %s, want it to say the node is not available", record.Error)
	}
	if len(record.NodeRuns) != 0 {
		t.Fatalf("node runs = %d, want nothing executed", len(record.NodeRuns))
	}
}

// An allow list keeps the engine's own triggers visible even when it omits
// them, so a called workflow still starts from its sub-workflow trigger and an
// error workflow from its error trigger, while any other omitted built-in is hidden.
func TestAnAllowListNeverHidesTheEnginesOwnTriggers(t *testing.T) {
	setup := newComposition(t)
	if err := setup.catalog.ApplyAvailability(node.AvailabilityRules{
		Allow: []node.AvailabilityRule{{Type: "kilasflow.manual"}},
	}); err != nil {
		t.Fatalf("ApplyAvailability() error = %v", err)
	}
	view := workflow.CatalogFor(setup.catalog, "tenant-a")
	for _, required := range node.EngineRequiredTypes() {
		if _, found := view.Lookup(required, workflow.V(1)); !found {
			t.Errorf("an allow list hid the engine-required %q", required)
		}
	}
	if _, found := view.Lookup(hiddenBuiltin, workflow.V(1)); found {
		t.Errorf("an allow list that omits %q did not hide it", hiddenBuiltin)
	}
}
