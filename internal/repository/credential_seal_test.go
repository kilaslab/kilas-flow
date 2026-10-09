package repository_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

func rawCredentialPayload(t *testing.T, db *database.DB, credentialID string) []byte {
	t.Helper()
	var payload []byte
	if err := db.Table("credentials").Select("payload").Where("id = ?", credentialID).Row().Scan(&payload); err != nil {
		t.Fatalf("read the stored payload: %v", err)
	}
	return payload
}

func writeRawCredentialPayload(t *testing.T, db *database.DB, credentialID string, payload []byte) {
	t.Helper()
	if err := db.Exec("UPDATE credentials SET payload = ? WHERE id = ?", payload, credentialID).Error; err != nil {
		t.Fatalf("write the stored payload: %v", err)
	}
}

// A sealed payload is bound to the row it was written for. Someone who can
// write to the database but does not hold the key used to be able to move a
// payload from one credential to another — into another tenant, or onto a
// credential whose scope sends it somewhere else — and have it open there.
func TestAPayloadSwappedIntoAnotherRowDoesNotOpen(t *testing.T) {
	eachDriver(t, assertPayloadSwapRefused)
}

func assertPayloadSwapRefused(t *testing.T, db *database.DB) {
	cipher := stateCipher(t, 0x41)
	store := repository.NewCredentialStore(db.DB, cipher)
	ctx := context.Background()
	tenantA, tenantB := repository.TenantScope{ID: "drv-seal-a"}, repository.TenantScope{ID: "drv-seal-b"}

	victim, err := store.Create(ctx, tenantA, credentials.Record{
		Name: "victim", Type: "httpBearerAuth", Fields: map[string]string{"token": "victim-token"},
	})
	if err != nil {
		t.Fatalf("Create(victim) error = %v", err)
	}
	attacker, err := store.Create(ctx, tenantB, credentials.Record{
		Name: "attacker", Type: "httpBearerAuth", Fields: map[string]string{"token": "attacker-token"},
	})
	if err != nil {
		t.Fatalf("Create(attacker) error = %v", err)
	}
	sibling, err := store.Create(ctx, tenantA, credentials.Record{
		Name: "sibling", Type: "httpBearerAuth", Fields: map[string]string{"token": "sibling-token"},
	})
	if err != nil {
		t.Fatalf("Create(sibling) error = %v", err)
	}
	header, err := store.Create(ctx, tenantA, credentials.Record{
		Name: "header", Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Key", "value": "header-value"},
	})
	if err != nil {
		t.Fatalf("Create(header) error = %v", err)
	}

	payload := rawCredentialPayload(t, db, victim.ID)
	if !bytes.HasPrefix(payload, []byte("kfs\x01")) {
		t.Fatalf("a new credential was stored as %x..., want the v1 header", payload[:8])
	}

	for _, target := range []struct {
		name   string
		tenant repository.TenantScope
		id     string
	}{
		{"another tenant's credential", tenantB, attacker.ID},
		{"another credential of the same tenant", tenantA, sibling.ID},
		{"a credential of another type", tenantA, header.ID},
	} {
		original := rawCredentialPayload(t, db, target.id)
		writeRawCredentialPayload(t, db, target.id, payload)
		if _, fields, err := store.Resolve(ctx, target.tenant, target.id); err == nil {
			t.Errorf("the victim's payload opened as %s: %#v", target.name, fields)
		}
		if _, err := store.Update(ctx, target.tenant, target.id, credentials.Record{Name: "renamed"}); err == nil {
			t.Errorf("an update of %s re-sealed the victim's payload", target.name)
		}
		writeRawCredentialPayload(t, db, target.id, original)
	}

	if _, fields, err := store.Resolve(ctx, tenantA, victim.ID); err != nil || fields["token"] != "victim-token" {
		t.Fatalf("the victim's own row no longer resolves: %#v, %v", fields, err)
	}
}

// Lifecycle state is the secret a service signs its deliveries with, so it is
// bound to the route it was captured for the same way.
func TestLifecycleStateIsBoundToItsRoute(t *testing.T) {
	eachDriver(t, assertLifecycleStateBoundToRoute)
}

func assertLifecycleStateBoundToRoute(t *testing.T, db *database.DB) {
	store := repository.NewWorkflowStore(db.DB).WithWebhooks(pathLabelTriggers).WithLifecycleState(stateCipher(t, 0x42))
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "drv-seal-route"}
	first := activateCapturingWorkflow(t, store, tenant, "wf_drv_seal_one")
	second := activateCapturingWorkflow(t, store, tenant, "wf_drv_seal_two")

	if err := store.SaveLifecycleState(ctx, tenant, first, map[string]string{"secret": "first-secret"}); err != nil {
		t.Fatalf("SaveLifecycleState(first) error = %v", err)
	}
	if err := store.SaveLifecycleState(ctx, tenant, second, map[string]string{"secret": "second-secret"}); err != nil {
		t.Fatalf("SaveLifecycleState(second) error = %v", err)
	}
	var sealed []byte
	if err := db.Table("webhook_routes").Select("lifecycle_state").Where("route = ?", first).Row().Scan(&sealed); err != nil {
		t.Fatalf("read the stored state: %v", err)
	}
	if !bytes.HasPrefix(sealed, []byte("kfs\x01")) {
		t.Fatalf("lifecycle state was stored as %x..., want the v1 header", sealed[:8])
	}
	if err := db.Exec("UPDATE webhook_routes SET lifecycle_state = ? WHERE route = ?", sealed, second).Error; err != nil {
		t.Fatalf("swap the state: %v", err)
	}
	if values, err := store.LifecycleState(ctx, tenant, second); err == nil {
		t.Fatalf("the first route's state opened on the second route: %#v", values)
	}
	if values, err := store.LifecycleState(ctx, tenant, first); err != nil || values["secret"] != "first-secret" {
		t.Fatalf("the first route's own state = %#v, %v", values, err)
	}
}
