package credentials_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// A binding names the environment variable its manager token lives in. Left
// open, a binding could name any variable the server holds — the database DSN,
// the credential master key — and have it sent to an address the binding also
// chooses, as an X-Vault-Token header. Only a variable under one fixed prefix
// can be named.
func TestBindingRefusesATokenVariableOutsideThePrefix(t *testing.T) {
	t.Parallel()

	binding := credentials.Binding{TenantID: "tenant-a", Name: "prod-vault", Provider: "vault", Address: "https://vault.example"}
	for _, variable := range []string{
		"KILASFLOW_ENCRYPTION_KEY",
		"KILASFLOW_DATABASE_DSN",
		"VAULT_TOKEN",
		"HOME",
		credentials.BindingTokenEnvPrefix,
		credentials.BindingTokenEnvPrefix + "prod",
		credentials.BindingTokenEnvPrefix + "PROD-VAULT",
		credentials.BindingTokenEnvPrefix + "PROD VAULT",
		"X" + credentials.BindingTokenEnvPrefix + "PROD",
	} {
		candidate := binding
		candidate.TokenEnv = variable
		err := candidate.Validate()
		if err == nil {
			t.Errorf("Validate() accepted TokenEnv %q", variable)
			continue
		}
		if !strings.Contains(err.Error(), credentials.BindingTokenEnvPrefix) {
			t.Errorf("Validate(%q) error = %v, want it to name the prefix", variable, err)
		}
	}
	for _, variable := range []string{
		credentials.BindingTokenEnvPrefix + "PROD",
		credentials.BindingTokenEnvPrefix + "PROD_VAULT_2",
	} {
		candidate := binding
		candidate.TokenEnv = variable
		if err := candidate.Validate(); err != nil {
			t.Errorf("Validate() refused %q: %v", variable, err)
		}
	}
	if credentials.BindingTokenEnvPrefix != "KILASFLOW_EXTERNAL_SECRET_TOKEN_" {
		t.Fatalf("BindingTokenEnvPrefix = %q: it is documented, and operators set variables under it", credentials.BindingTokenEnvPrefix)
	}
}

// A row written around Validate — by hand, by an older build, by a future
// binding API that forgets — is refused again where the variable is read, so
// the variable's value never reaches a provider.
func TestManagerTokenRefusesAStoredVariableOutsideThePrefix(t *testing.T) {
	t.Setenv("KILASFLOW_TEST_GUARD_MASTER_KEY", "the-master-key-value")
	store := &fakeStore{bindings: map[string]credentials.Binding{
		"tenant-a\x00prod-vault": {
			TenantID: "tenant-a", Name: "prod-vault", Provider: credentials.VaultProviderName,
			Address: "https://vault.example", TokenEnv: "KILASFLOW_TEST_GUARD_MASTER_KEY",
		},
	}}
	resolver := credentials.NewResolver(store, credentials.ResolverConfig{})
	_, err := resolver.ResolveField(context.Background(), "tenant-a", "ext://prod-vault/prod/api-token")
	if err == nil {
		t.Fatal("a binding naming a variable outside the prefix resolved")
	}
	if !strings.Contains(err.Error(), credentials.BindingTokenEnvPrefix) {
		t.Fatalf("ResolveField() error = %v, want it to name the prefix", err)
	}
	if strings.Contains(err.Error(), "the-master-key-value") {
		t.Fatal("the refused variable's value appeared in the error")
	}
}
