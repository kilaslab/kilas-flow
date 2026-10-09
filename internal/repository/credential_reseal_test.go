package repository_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// legacySealed builds a v0 payload the way every credential was stored before
// payloads were versioned: the raw master key, a random nonce, no AAD.
func legacySealed(t *testing.T, key []byte, fields map[string]string) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	plaintext, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil)
}

func keyOf(fill byte) []byte { return bytes.Repeat([]byte{fill}, credentials.KeySize) }

func mustCipher(t *testing.T, primary []byte, secondary ...[]byte) *credentials.Cipher {
	t.Helper()
	keyring, err := credentials.NewCipher(primary, secondary...)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	return keyring
}

func mustBearer(t *testing.T, store *repository.GORMCredentialStore, tenant repository.TenantScope, name, token string) credentials.Record {
	t.Helper()
	created, err := store.Create(context.Background(), tenant, credentials.Record{
		Name: name, Type: "httpBearerAuth", Fields: map[string]string{"token": token},
	})
	if err != nil {
		t.Fatalf("Create(%s) error = %v", name, err)
	}
	return created
}

func storedUpdatedAt(t *testing.T, db *database.DB, credentialID string) time.Time {
	t.Helper()
	var updated time.Time
	if err := db.Table("credentials").Select("updated_at").Where("id = ?", credentialID).Row().Scan(&updated); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return updated
}

// A rotation reseals what the old key wrote — legacy v0 rows and v1 rows
// under the key now held as secondary — and leaves rows already under the
// primary alone. Afterwards the primary key opens everything on its own, so
// the secondary can be removed.
func TestResealUpgradesLegacyAndRotatedRows(t *testing.T) {
	eachDriver(t, assertResealUpgrades)
}

func assertResealUpgrades(t *testing.T, db *database.DB) {
	ctx := context.Background()
	oldKey, newKey := keyOf(0x51), keyOf(0x52)
	tenant := repository.TenantScope{ID: "drv-reseal"}

	underOld := repository.NewCredentialStore(db.DB, mustCipher(t, oldKey))
	rotated := mustBearer(t, underOld, tenant, "rotated", "rotated-token")
	legacy := mustBearer(t, underOld, tenant, "legacy", "legacy-token")
	writeRawCredentialPayload(t, db, legacy.ID, legacySealed(t, oldKey, map[string]string{"token": "legacy-token"}))
	legacyUpdated := storedUpdatedAt(t, db, legacy.ID)

	rotating := repository.NewCredentialStore(db.DB, mustCipher(t, newKey, oldKey))
	current := mustBearer(t, rotating, tenant, "current", "current-token")
	currentPayload := rawCredentialPayload(t, db, current.ID)

	report, err := rotating.Reseal(ctx)
	if err != nil {
		t.Fatalf("Reseal() error = %v", err)
	}
	if db.Dialector.Name() == "sqlite" {
		// The PostgreSQL server is shared with other tests, so exact counts
		// are only asserted on a database this test owns.
		if report.Resealed != 2 || report.Current != 1 || report.Skipped != 0 || len(report.Unopenable) != 0 {
			t.Fatalf("Reseal() = %+v, want 2 resealed, 1 current", report)
		}
	}

	newOnly := repository.NewCredentialStore(db.DB, mustCipher(t, newKey))
	for _, record := range []struct {
		id, token string
	}{{rotated.ID, "rotated-token"}, {legacy.ID, "legacy-token"}, {current.ID, "current-token"}} {
		payload := rawCredentialPayload(t, db, record.id)
		if !bytes.HasPrefix(payload, []byte("kfs\x01")) {
			t.Errorf("%s was left as %x..., want v1", record.id, payload[:8])
		}
		if _, fields, err := newOnly.Resolve(ctx, tenant, record.id); err != nil || fields["token"] != record.token {
			t.Errorf("%s does not open under the new key alone: %#v, %v", record.id, fields, err)
		}
	}
	if !bytes.Equal(rawCredentialPayload(t, db, current.ID), currentPayload) {
		t.Error("a row already under the primary key was rewritten")
	}
	if !storedUpdatedAt(t, db, legacy.ID).Equal(legacyUpdated) {
		t.Error("resealing changed updated_at: a reseal is not an edit")
	}

	again, err := rotating.Reseal(ctx)
	if err != nil {
		t.Fatalf("Reseal() again error = %v", err)
	}
	if db.Dialector.Name() == "sqlite" && (again.Resealed != 0 || again.Current != 3) {
		t.Fatalf("a second Reseal() = %+v, want nothing left to do", again)
	}

	// Lifecycle state is sealed with the same key, so a rotation that left it
	// behind would break every trigger that verifies with a captured secret.
	workflows := repository.NewWorkflowStore(db.DB).WithWebhooks(pathLabelTriggers).WithLifecycleState(mustCipher(t, newKey, oldKey))
	route := activateCapturingWorkflow(t, workflows, tenant, "wf_drv_reseal")
	if err := db.Exec("UPDATE webhook_routes SET lifecycle_state = ? WHERE route = ?",
		legacySealed(t, oldKey, map[string]string{"secret": "whsec"}), route).Error; err != nil {
		t.Fatalf("plant legacy lifecycle state: %v", err)
	}
	stateReport, err := workflows.ResealLifecycleState(ctx)
	if err != nil {
		t.Fatalf("ResealLifecycleState() error = %v", err)
	}
	if db.Dialector.Name() == "sqlite" && (stateReport.Resealed != 1 || len(stateReport.Unopenable) != 0) {
		t.Fatalf("ResealLifecycleState() = %+v, want 1 resealed", stateReport)
	}
	reader := repository.NewWorkflowStore(db.DB).WithLifecycleState(mustCipher(t, newKey))
	if values, err := reader.LifecycleState(ctx, tenant, route); err != nil || values["secret"] != "whsec" {
		t.Fatalf("lifecycle state under the new key alone = %#v, %v", values, err)
	}
}

// The write is a compare-and-swap on the payload it read: a row a request
// changed between the read and the write keeps the request's value, and is
// counted as skipped rather than overwritten with the older secret.
func TestResealSkipsARowChangedMeanwhile(t *testing.T) {
	eachDriver(t, assertResealSkipsChangedRow)
}

func assertResealSkipsChangedRow(t *testing.T, db *database.DB) {
	ctx := context.Background()
	oldKey, newKey := keyOf(0x61), keyOf(0x62)
	tenant := repository.TenantScope{ID: "drv-reseal-race"}
	underOld := repository.NewCredentialStore(db.DB, mustCipher(t, oldKey))
	raced := mustBearer(t, underOld, tenant, "raced", "before")

	rotating := repository.NewCredentialStore(db.DB, mustCipher(t, newKey, oldKey))
	writer := repository.NewCredentialStore(db.DB, mustCipher(t, newKey, oldKey))
	repository.SetResealHook(rotating, func(credentialID string) {
		if credentialID != raced.ID {
			return
		}
		if _, err := writer.Update(ctx, tenant, raced.ID, credentials.Record{
			Name: "raced", Fields: map[string]string{"token": "after"},
		}); err != nil {
			t.Errorf("Update() during the reseal error = %v", err)
		}
	})
	report, err := rotating.Reseal(ctx)
	if err != nil {
		t.Fatalf("Reseal() error = %v", err)
	}
	if report.Skipped < 1 {
		t.Fatalf("Reseal() = %+v, want the changed row skipped", report)
	}
	if _, fields, err := writer.Resolve(ctx, tenant, raced.ID); err != nil || fields["token"] != "after" {
		t.Fatalf("the concurrent update was lost: %#v, %v", fields, err)
	}
}

// A payload no configured key opens is reported and left exactly as it was,
// and does not stop the pass: every other row is still resealed.
func TestResealReportsUnopenableRows(t *testing.T) {
	db, _, _ := newCredentialFixture(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "tenant-a"}
	lost := mustBearer(t, repository.NewCredentialStore(db.DB, mustCipher(t, keyOf(0x71))), tenant, "lost", "lost-token")
	lostPayload := rawCredentialPayload(t, db, lost.ID)
	oldKey, newKey := keyOf(0x72), keyOf(0x73)
	kept := mustBearer(t, repository.NewCredentialStore(db.DB, mustCipher(t, oldKey)), tenant, "kept", "kept-token")

	rotating := repository.NewCredentialStore(db.DB, mustCipher(t, newKey, oldKey))
	report, err := rotating.Reseal(ctx)
	if err != nil {
		t.Fatalf("Reseal() error = %v", err)
	}
	if len(report.Unopenable) != 1 || report.Unopenable[0] != lost.ID || report.Resealed != 1 {
		t.Fatalf("Reseal() = %+v, want %s unopenable and the other row resealed", report, lost.ID)
	}
	if !bytes.Equal(rawCredentialPayload(t, db, lost.ID), lostPayload) {
		t.Fatal("an unopenable payload was rewritten")
	}
	if _, fields, err := repository.NewCredentialStore(db.DB, mustCipher(t, newKey)).Resolve(ctx, tenant, kept.ID); err != nil || fields["token"] != "kept-token" {
		t.Fatalf("the openable row was not resealed: %#v, %v", fields, err)
	}
}
