package main

// Secrets-manager boot proof: a configured-but-unreachable manager refuses
// startup with ErrManagerUnreachable (never degrading to the no-key path),
// a healthy manager returns the key, and an unconfigured install keeps the
// environment path unchanged.
//
// Never allow_private_networks in these tests: loopback grants travel as
// allowed_private_endpoints entries only.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
)

func TestMasterKeyKeepsTheEnvironmentPathWhenUnconfigured(t *testing.T) {
	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", "")
	cfg := config.Default()
	cfg.Security.EncryptionKeyEnv = "KILASFLOW_TEST_MAIN_MASTER_KEY"
	if _, err := masterKey(context.Background(), cfg); !errors.Is(err, credentials.ErrNoKey) {
		t.Fatalf("masterKey() error = %v, want ErrNoKey", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand error = %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	t.Setenv("KILASFLOW_TEST_MAIN_MASTER_KEY", encoded)
	key, err := masterKey(context.Background(), cfg)
	if err != nil {
		t.Fatalf("masterKey() error = %v", err)
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
	if _, err := masterKey(context.Background(), cfg); !errors.Is(err, credentials.ErrManagerUnreachable) {
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
	key, err := masterKey(context.Background(), cfg)
	if err != nil {
		t.Fatalf("masterKey() error = %v", err)
	}
	if base64.StdEncoding.EncodeToString(key) != encoded {
		t.Error("masterKey() did not return the manager's key")
	}
}
