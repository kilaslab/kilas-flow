package main

// Secrets-manager boot proof: a configured-but-unreachable manager refuses
// startup with ErrManagerUnreachable (never degrading to the no-key path),
// a healthy manager returns the key, and an unconfigured install keeps the
// environment path unchanged.
//
// Never allow_private_networks in these tests: loopback grants travel as
// allowed_private_endpoints entries only.

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
)

func TestMasterKeyKeepsTheEnvironmentPathWhenUnconfigured(t *testing.T) {
	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", "")
	cfg := config.Default()
	cfg.Security.EncryptionKeyEnv = "KILASFLOW_TEST_MAIN_MASTER_KEY"
	if _, _, err := masterKey(context.Background(), cfg); !errors.Is(err, credentials.ErrNoKey) {
		t.Fatalf("masterKey() error = %v, want ErrNoKey", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand error = %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", encoded)
	key, format, err := masterKey(context.Background(), cfg)
	if err != nil {
		t.Fatalf("masterKey() error = %v", err)
	}
	if format != credentials.KeyFormatBase64 {
		t.Fatalf("masterKey() format = %v, want base64", format)
	}
	if len(key) != 32 {
		t.Fatalf("key bytes = %d, want 32", len(key))
	}
}

func TestMasterKeyRefusesAnUnreachableManager(t *testing.T) {
	t.Setenv("KILASFLOW_TEST_MAIN_VAULT_TOKEN", "test-token")
	cfg := config.Default()
	cfg.Secrets = config.Secrets{
		ManagerAddr:     "http://127.0.0.1:1",
		ManagerTokenEnv: "KILASFLOW_TEST_MAIN_VAULT_TOKEN",
		MasterKey:       "prod/master-key",
	}
	cfg.Outbound.AllowedPrivateEndpoints = []string{"127.0.0.1:1"}
	if _, _, err := masterKey(context.Background(), cfg); !errors.Is(err, credentials.ErrManagerUnreachable) {
		t.Fatalf("masterKey() error = %v, want ErrManagerUnreachable", err)
	}
}

func TestMasterKeyReadsFromAHealthyManager(t *testing.T) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand error = %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/sys/health":
			writer.WriteHeader(http.StatusOK)
		case "/v1/secret/data/prod/master-key":
			if request.Header.Get("X-Vault-Token") != "test-token" {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = writer.Write([]byte(`{"data":{"data":{"value":` + `"` + encoded + `"` + `}}}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
	t.Setenv("KILASFLOW_TEST_MAIN_VAULT_TOKEN", "test-token")
	cfg := config.Default()
	cfg.Secrets = config.Secrets{
		ManagerAddr:     server.URL,
		ManagerTokenEnv: "KILASFLOW_TEST_MAIN_VAULT_TOKEN",
		MasterKey:       "prod/master-key",
	}
	cfg.Outbound.AllowedPrivateEndpoints = []string{serverURL.Host}
	key, _, err := masterKey(context.Background(), cfg)
	if err != nil {
		t.Fatalf("masterKey() error = %v", err)
	}
	if base64.StdEncoding.EncodeToString(key) != encoded {
		t.Error("masterKey() did not return the manager's key")
	}
}

// The OAuth state and PKCE verifier used to be HMAC'd with the credential
// master key itself, so the same 32 bytes sealed every secret and signed
// every Connect popup. The signing key is now its own HKDF output.
func TestOAuthSigningKeyIsDerivedNotTheMasterKey(t *testing.T) {
	master := make([]byte, credentials.KeySize)
	if _, err := rand.Read(master); err != nil {
		t.Fatalf("rand error = %v", err)
	}
	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", base64.StdEncoding.EncodeToString(master))
	cfg := config.Default()
	cfg.Security.EncryptionKeyEnv = "KILASFLOW_TEST_MAIN_MASTER_KEY"

	cipher, oauthKey, err := loadCredentialKeys(context.Background(), cfg, quietLogger())
	if err != nil {
		t.Fatalf("loadCredentialKeys() error = %v", err)
	}
	if cipher == nil {
		t.Fatal("loadCredentialKeys() returned no cipher")
	}
	if bytes.Equal(oauthKey, master) {
		t.Fatal("the OAuth signing key is the credential master key")
	}
	want, err := hkdf.Key(sha256.New, master, nil, "kilasflow/oauth-state/v1", credentials.KeySize)
	if err != nil {
		t.Fatalf("hkdf.Key() error = %v", err)
	}
	if !bytes.Equal(oauthKey, want) {
		t.Fatal("the OAuth signing key is not HKDF-SHA256(master, \"kilasflow/oauth-state/v1\")")
	}

	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", "")
	if _, _, err := loadCredentialKeys(context.Background(), cfg, quietLogger()); !errors.Is(err, credentials.ErrNoKey) {
		t.Fatalf("loadCredentialKeys() with no key error = %v, want ErrNoKey", err)
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A 32-character passphrase is accepted as raw key bytes — refusing it would
// lock every such installation out of its own credentials — but it is not a
// generated key, and the operator is told so once per key at boot.
func TestBootWarnsOnceAboutARawStringKey(t *testing.T) {
	const raw = "correct-horse-battery-staple-123"
	if len(raw) != credentials.KeySize {
		t.Fatalf("fixture is %d characters, want %d", len(raw), credentials.KeySize)
	}
	cfg := config.Default()
	cfg.Security.EncryptionKeyEnv = "KILASFLOW_TEST_MAIN_MASTER_KEY"

	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", raw)
	var out bytes.Buffer
	if _, _, err := loadCredentialKeys(context.Background(), cfg, slog.New(slog.NewTextHandler(&out, nil))); err != nil {
		t.Fatalf("loadCredentialKeys() error = %v", err)
	}
	logged := out.String()
	if strings.Count(logged, "raw key bytes") != 1 || !strings.Contains(logged, "variable=KILASFLOW_TEST_MAIN_MASTER_KEY") ||
		!strings.Contains(logged, "openssl rand -base64 32") || !strings.Contains(logged, "level=WARN") {
		t.Fatalf("boot log = %q, want one warning naming the variable and how to generate a key", logged)
	}
	if strings.Contains(logged, raw) {
		t.Fatal("the warning printed the key")
	}

	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(raw)))
	out.Reset()
	if _, _, err := loadCredentialKeys(context.Background(), cfg, slog.New(slog.NewTextHandler(&out, nil))); err != nil {
		t.Fatalf("loadCredentialKeys() error = %v", err)
	}
	if strings.Contains(out.String(), "raw key bytes") {
		t.Fatalf("a base64 key was warned about: %q", out.String())
	}

	// The embed and auth signing keys go through the same decoder, so they
	// get the same warning.
	t.Setenv("KILASFLOW_TEST_MAIN_SIGNING_KEY", raw)
	out.Reset()
	key, err := signingKeyFromEnvironment("KILASFLOW_TEST_MAIN_SIGNING_KEY", slog.New(slog.NewTextHandler(&out, nil)))
	if err != nil || string(key) != raw {
		t.Fatalf("signingKeyFromEnvironment() = %q, %v", key, err)
	}
	if !strings.Contains(out.String(), "variable=KILASFLOW_TEST_MAIN_SIGNING_KEY") || !strings.Contains(out.String(), "raw key bytes") {
		t.Fatalf("signing key log = %q, want the raw-key warning", out.String())
	}
}
