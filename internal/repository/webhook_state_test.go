package repository_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// stateCipher is a lifecycle-state key made of one repeated byte, so two tests
// can hold two different keys.
func stateCipher(t *testing.T, fill byte) *credentials.Cipher {
	t.Helper()
	cipher, err := credentials.NewCipher(bytes.Repeat([]byte{fill}, credentials.KeySize))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	return cipher
}

// activateCapturingWorkflow activates one workflow with one webhook trigger for
// a tenant, and returns the route it answers on.
func activateCapturingWorkflow(t *testing.T, store *repository.GORMWorkflowStore, tenant repository.TenantScope, workflowID string) string {
	t.Helper()
	ctx := context.Background()
	document := pathLabelDocument(workflowID, "Captures", pathLabelTrigger{nodeID: "hook", method: "POST", path: "events"})
	if _, err := store.SaveDraft(ctx, tenant, document); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := store.Activate(ctx, tenant, workflowID, pathLabelCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	bindings, err := store.WebhookRoutes(ctx, tenant, workflowID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("WebhookRoutes() = %#v, %v, want the one trigger's route", bindings, err)
	}
	return bindings[0].Route
}

// What a trigger's registration answered with — a subscription id, the secret
// the service will sign with — is kept on the route, sealed like a credential.
// The route is what outlives activation: bindings are deleted on deactivation
// and re-inserted on activation, and the subscription is still there when the
// workflow comes back, so its id must be too. A delivery carries the values
// opened, because the HMAC secret among them is what verifies it.
func TestCapturedLifecycleStateIsSealedOnTheRouteAndOutlivesItsBindings(t *testing.T) {
	eachDriver(t, assertLifecycleStateOutlivesBindings)
}

func assertLifecycleStateOutlivesBindings(t *testing.T, db *database.DB) {
	store := repository.NewWorkflowStore(db.DB).WithWebhooks(pathLabelTriggers).WithLifecycleState(stateCipher(t, 1))
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "drv-state"}
	route := activateCapturingWorkflow(t, store, tenant, "wf_drv_state")

	kept := map[string]string{"id": "sub-42", "secret": "whsec_1"}
	if err := store.SaveLifecycleState(ctx, tenant, route, kept); err != nil {
		t.Fatalf("SaveLifecycleState() error = %v", err)
	}
	var sealed []byte
	if err := db.Table("webhook_routes").Select("lifecycle_state").Where("route = ?", route).Row().Scan(&sealed); err != nil {
		t.Fatalf("read the stored state error = %v", err)
	}
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("sub-42")) || bytes.Contains(sealed, []byte("whsec_1")) {
		t.Fatalf("stored state = %q, want the values sealed", sealed)
	}

	if _, err := store.Deactivate(ctx, tenant, "wf_drv_state"); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if got, err := store.LifecycleState(ctx, tenant, route); err != nil || !reflect.DeepEqual(got, kept) {
		t.Fatalf("LifecycleState() after deactivation = %#v, %v, want %#v", got, err, kept)
	}
	if _, err := store.Activate(ctx, tenant, "wf_drv_state", pathLabelCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	resolved, err := store.Resolve(ctx, "POST", route)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Captured, kept) {
		t.Fatalf("Resolve().Captured = %#v, want %#v", resolved.Captured, kept)
	}

	// The route is another tenant's to nobody: the state is read and written
	// by tenant and route together.
	other := repository.TenantScope{ID: "drv-state-other"}
	if got, err := store.LifecycleState(ctx, other, route); err != nil || len(got) != 0 {
		t.Fatalf("another tenant's LifecycleState() = %#v, %v, want nothing", got, err)
	}
	if err := store.SaveLifecycleState(ctx, other, route, map[string]string{"secret": "forged"}); err == nil {
		t.Fatal("another tenant saved state on this tenant's route")
	}

	if err := store.ClearLifecycleState(ctx, tenant, route); err != nil {
		t.Fatalf("ClearLifecycleState() error = %v", err)
	}
	resolved, err = store.Resolve(ctx, "POST", route)
	if err != nil {
		t.Fatalf("Resolve() after clearing error = %v", err)
	}
	if len(resolved.Captured) != 0 {
		t.Fatalf("Resolve().Captured after clearing = %#v, want nothing", resolved.Captured)
	}
}

// A captured value may be a secret, so it is never written unsealed: without
// the key there is nowhere to keep it. And a delivery to a route whose state
// cannot be opened is not routed at all. Routed with its values missing, a
// trigger verified by a captured secret would read no secret, and a trigger
// with no secret accepts every delivery unsigned.
func TestCapturedLifecycleStateIsNeverKeptOrReadWithoutItsKey(t *testing.T) {
	eachDriver(t, assertLifecycleStateNeedsItsKey)
}

func assertLifecycleStateNeedsItsKey(t *testing.T, db *database.DB) {
	store := repository.NewWorkflowStore(db.DB).WithWebhooks(pathLabelTriggers)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "drv-state-key"}
	route := activateCapturingWorkflow(t, store, tenant, "wf_drv_state_key")

	if err := store.SaveLifecycleState(ctx, tenant, route, map[string]string{"secret": "whsec_1"}); err == nil {
		t.Fatal("state was kept with no key to seal it")
	}
	if resolved, err := store.Resolve(ctx, "POST", route); err != nil || len(resolved.Captured) != 0 {
		t.Fatalf("Resolve() of a route with no state = %#v, %v, want it routed with nothing captured", resolved.Captured, err)
	}

	store.WithLifecycleState(stateCipher(t, 1))
	if err := store.SaveLifecycleState(ctx, tenant, route, map[string]string{"secret": "whsec_1"}); err != nil {
		t.Fatalf("SaveLifecycleState() error = %v", err)
	}
	for name, reader := range map[string]*repository.GORMWorkflowStore{
		"no key":        repository.NewWorkflowStore(db.DB),
		"the wrong key": repository.NewWorkflowStore(db.DB).WithLifecycleState(stateCipher(t, 2)),
	} {
		if _, err := reader.Resolve(ctx, "POST", route); err == nil {
			t.Errorf("with %s, Resolve() routed a delivery whose state it could not open", name)
		}
		if _, err := reader.LifecycleState(ctx, tenant, route); err == nil {
			t.Errorf("with %s, LifecycleState() read state it could not open", name)
		}
	}
}
