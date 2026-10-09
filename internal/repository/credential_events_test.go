package repository_test

import (
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// sameActor compares the three fields an audit row keeps.
func sameActor(left, right repository.Actor) bool {
	return left.Kind == right.Kind && left.Label == right.Label && left.KeyID == right.KeyID
}

// A credential event names the credential as it was — the row may be gone by
// the time anyone reads it — and the actor it was given, and it is read back
// only under the tenant that wrote it.
func TestCredentialEventsRecordTheActor(t *testing.T) {
	eachDriver(t, assertCredentialEventsRecordTheActor)
}

func assertCredentialEventsRecordTheActor(t *testing.T, db *database.DB) {
	ctx := context.Background()
	store := repository.NewCredentialStore(db.DB, stateCipher(t, 0x55))
	tenant, other := repository.TenantScope{ID: "drv-events"}, repository.TenantScope{ID: "drv-events-other"}
	created := mustBearer(t, store, tenant, "Billing API", "token")

	key := repository.Actor{Kind: repository.ActorKindKey, Label: "deploy bot", KeyID: "key_1"}
	user := repository.Actor{Kind: repository.ActorKindUser, Label: "owner@acme.example"}
	for _, event := range []repository.CredentialEvent{
		{CredentialID: created.ID, CredentialName: created.Name, CredentialType: created.Type,
			Action: repository.CredentialEventCreated, Actor: key},
		{CredentialID: created.ID, CredentialName: created.Name, CredentialType: created.Type,
			Action: repository.CredentialEventTested, Actor: user, Detail: "ok"},
		{CredentialID: created.ID, CredentialName: created.Name, CredentialType: created.Type,
			Action: repository.CredentialEventDeleted},
	} {
		if err := store.RecordCredentialEvent(ctx, tenant, event); err != nil {
			t.Fatalf("RecordCredentialEvent(%s) error = %v", event.Action, err)
		}
	}

	events, err := store.CredentialEvents(ctx, tenant, created.ID)
	if err != nil {
		t.Fatalf("CredentialEvents() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("CredentialEvents() = %d rows, want 3", len(events))
	}
	// Newest first, the way an audit trail is read.
	deleted, tested, made := events[0], events[1], events[2]
	if made.Action != repository.CredentialEventCreated || !sameActor(made.Actor, key) ||
		made.CredentialName != "Billing API" || made.CredentialType != "httpBearerAuth" || made.CreatedAt.IsZero() {
		t.Errorf("created event = %+v", made)
	}
	if tested.Action != repository.CredentialEventTested || !sameActor(tested.Actor, user) || tested.Detail != "ok" {
		t.Errorf("tested event = %+v", tested)
	}
	if deleted.Action != repository.CredentialEventDeleted || !sameActor(deleted.Actor, repository.Actor{}) {
		t.Errorf("an event with no actor = %+v, want it recorded with none", deleted)
	}

	if leaked, err := store.CredentialEvents(ctx, other, created.ID); err != nil || len(leaked) != 0 {
		t.Fatalf("another tenant read %d events, %v; want none", len(leaked), err)
	}
	if err := store.RecordCredentialEvent(ctx, repository.TenantScope{}, repository.CredentialEvent{
		CredentialID: created.ID, Action: repository.CredentialEventCreated,
	}); err == nil {
		t.Fatal("an event was recorded with no tenant")
	}
	if err := store.RecordCredentialEvent(ctx, tenant, repository.CredentialEvent{CredentialID: created.ID}); err == nil {
		t.Fatal("an event was recorded with no action")
	}

	// An event is recorded even where credential storage is not configured:
	// the trail does not need the key.
	if err := repository.NewCredentialStore(db.DB, nil).RecordCredentialEvent(ctx, tenant, repository.CredentialEvent{
		CredentialID: created.ID, Action: repository.CredentialEventUpdated, Actor: key,
	}); err != nil {
		t.Fatalf("RecordCredentialEvent() without a cipher error = %v", err)
	}
}

// The OAuth callback looks up one event — the connect-started row with its
// state's nonce hash — rather than reading a credential's whole trail.
func TestFindCredentialEventMatchesActionAndDetailUnderTheTenant(t *testing.T) {
	eachDriver(t, assertFindCredentialEvent)
}

func assertFindCredentialEvent(t *testing.T, db *database.DB) {
	ctx := context.Background()
	store := repository.NewCredentialStore(db.DB, stateCipher(t, 0x56))
	tenant, other := repository.TenantScope{ID: "drv-find-event"}, repository.TenantScope{ID: "drv-find-event-other"}
	created := mustBearer(t, store, tenant, "Drive", "token")
	keyA := repository.Actor{Kind: repository.ActorKindKey, Label: "a", KeyID: "key_a"}
	userB := repository.Actor{Kind: repository.ActorKindUser, Label: "b@acme.example"}
	for _, event := range []repository.CredentialEvent{
		{CredentialID: created.ID, Action: repository.CredentialEventConnectStarted, Actor: keyA, Detail: "nonce-a"},
		{CredentialID: created.ID, Action: repository.CredentialEventConnectStarted, Actor: userB, Detail: "nonce-b"},
		{CredentialID: created.ID, Action: repository.CredentialEventTested, Actor: userB, Detail: "nonce-a"},
	} {
		if err := store.RecordCredentialEvent(ctx, tenant, event); err != nil {
			t.Fatalf("RecordCredentialEvent() error = %v", err)
		}
	}

	found, ok, err := store.FindCredentialEvent(ctx, tenant, created.ID, repository.CredentialEventConnectStarted, "nonce-a")
	if err != nil || !ok || !sameActor(found.Actor, keyA) {
		t.Fatalf("FindCredentialEvent(nonce-a) = %+v, %t, %v, want key A's start", found, ok, err)
	}
	found, ok, err = store.FindCredentialEvent(ctx, tenant, created.ID, repository.CredentialEventConnectStarted, "nonce-b")
	if err != nil || !ok || !sameActor(found.Actor, userB) {
		t.Fatalf("FindCredentialEvent(nonce-b) = %+v, %t, %v, want user B's start", found, ok, err)
	}
	if _, ok, err := store.FindCredentialEvent(ctx, tenant, created.ID, repository.CredentialEventConnectStarted, "nonce-c"); err != nil || ok {
		t.Fatalf("FindCredentialEvent(unknown) = %t, %v, want no match", ok, err)
	}
	if _, ok, err := store.FindCredentialEvent(ctx, other, created.ID, repository.CredentialEventConnectStarted, "nonce-a"); err != nil || ok {
		t.Fatalf("FindCredentialEvent() under another tenant = %t, %v, want no match", ok, err)
	}
}
