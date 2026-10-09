package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

func rotationConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Security.EncryptionKeyEnv = "KILASFLOW_TEST_RESEAL_KEY"
	cfg.Security.SecondaryEncryptionKeyEnv = "KILASFLOW_TEST_RESEAL_KEY_SECONDARY"
	return cfg
}

func encodedKey(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, credentials.KeySize))
}

// During a rotation the old key is configured as the secondary: what it
// sealed still opens, and nothing new is sealed with it.
func TestASecondaryKeyOpensWhatTheOldKeySealed(t *testing.T) {
	cfg := rotationConfig(t)
	aad := credentials.CredentialAAD("default", "cred_1", "httpBasicAuth")
	old, err := credentials.NewCipher(bytes.Repeat([]byte{0x01}, credentials.KeySize))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	sealed, err := old.Seal(map[string]string{"password": "p"}, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	t.Setenv("KILASFLOW_TEST_RESEAL_KEY", encodedKey(0x02))
	t.Setenv("KILASFLOW_TEST_RESEAL_KEY_SECONDARY", "")
	cipher, _, err := loadCredentialKeys(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("loadCredentialKeys() error = %v", err)
	}
	if _, err := cipher.Open(sealed, aad); err == nil {
		t.Fatal("an old seal opened with no secondary key configured")
	}

	t.Setenv("KILASFLOW_TEST_RESEAL_KEY_SECONDARY", encodedKey(0x01))
	cipher, oauthKey, err := loadCredentialKeys(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("loadCredentialKeys() error = %v", err)
	}
	if opened, err := cipher.Open(sealed, aad); err != nil || opened["password"] != "p" {
		t.Fatalf("the secondary key did not open the old seal: %#v, %v", opened, err)
	}
	fresh, _ := cipher.Seal(map[string]string{"password": "q"}, aad)
	if _, err := old.Open(fresh, aad); err == nil {
		t.Fatal("a new seal was made with the secondary key")
	}
	// The OAuth key follows the primary only.
	primaryOnly, _ := credentials.DeriveKey(bytes.Repeat([]byte{0x02}, credentials.KeySize), credentials.OAuthStateKeyInfo)
	if !bytes.Equal(oauthKey, primaryOnly) {
		t.Fatal("the OAuth signing key is not derived from the primary key")
	}

	t.Setenv("KILASFLOW_TEST_RESEAL_KEY_SECONDARY", "not a key")
	if _, _, err := loadCredentialKeys(context.Background(), cfg, quietLogger()); err == nil ||
		!strings.Contains(err.Error(), "KILASFLOW_TEST_RESEAL_KEY_SECONDARY") {
		t.Fatalf("a malformed secondary key error = %v, want a refusal naming the variable", err)
	}

	t.Setenv("KILASFLOW_TEST_RESEAL_KEY_SECONDARY", "correct-horse-battery-staple-456")
	var out bytes.Buffer
	if _, _, err := loadCredentialKeys(context.Background(), cfg, slog.New(slog.NewTextHandler(&out, nil))); err != nil {
		t.Fatalf("loadCredentialKeys() error = %v", err)
	}
	if !strings.Contains(out.String(), "variable=KILASFLOW_TEST_RESEAL_KEY_SECONDARY") {
		t.Fatalf("a raw secondary key was not warned about: %q", out.String())
	}
}

func openMigrated(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"),
	}, quietLogger())
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quietLogger()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// -reseal-credentials reports what it did and fails when a payload cannot be
// opened, because that means a key that sealed it is not configured and
// removing the secondary now would lose it for good.
func TestResealCredentialsReportsAndFailsOnAnUnopenableRow(t *testing.T) {
	cfg := rotationConfig(t)
	db := openMigrated(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "default"}

	oldCipher, _ := credentials.NewCipher(bytes.Repeat([]byte{0x11}, credentials.KeySize))
	if _, err := repository.NewCredentialStore(db.DB, oldCipher).Create(ctx, tenant, credentials.Record{
		Name: "old", Type: "httpBearerAuth", Fields: map[string]string{"token": "t"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Setenv("KILASFLOW_TEST_RESEAL_KEY", encodedKey(0x12))
	t.Setenv("KILASFLOW_TEST_RESEAL_KEY_SECONDARY", encodedKey(0x11))
	var out bytes.Buffer
	if err := resealCredentials(ctx, cfg, db, slog.New(slog.NewTextHandler(&out, nil))); err != nil {
		t.Fatalf("resealCredentials() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "resealed=1") {
		t.Fatalf("reseal log = %q, want one row resealed", out.String())
	}
	out.Reset()
	if err := resealCredentials(ctx, cfg, db, slog.New(slog.NewTextHandler(&out, nil))); err != nil {
		t.Fatalf("a second resealCredentials() error = %v", err)
	}
	if !strings.Contains(out.String(), "resealed=0") || !strings.Contains(out.String(), "current=1") {
		t.Fatalf("second reseal log = %q, want nothing left to do", out.String())
	}

	lostCipher, _ := credentials.NewCipher(bytes.Repeat([]byte{0x13}, credentials.KeySize))
	lost, err := repository.NewCredentialStore(db.DB, lostCipher).Create(ctx, tenant, credentials.Record{
		Name: "lost", Type: "httpBearerAuth", Fields: map[string]string{"token": "t"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	out.Reset()
	err = resealCredentials(ctx, cfg, db, slog.New(slog.NewTextHandler(&out, nil)))
	if err == nil || !strings.Contains(err.Error(), "could not be opened") {
		t.Fatalf("resealCredentials() with an unopenable row error = %v", err)
	}
	if !strings.Contains(out.String(), lost.ID) {
		t.Fatalf("reseal log = %q, want the unopenable credential named", out.String())
	}

	t.Setenv("KILASFLOW_TEST_RESEAL_KEY", "")
	if err := resealCredentials(ctx, cfg, db, quietLogger()); err == nil {
		t.Fatal("resealCredentials() with no key succeeded")
	}
}
