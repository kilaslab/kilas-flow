package repository_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// Variables are per tenant: a key is unique inside one tenant, two tenants may
// share it, and one tenant can never read, change or delete another's.
func TestVariablesAreTenantScoped(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewVariableStore(db.DB)
		acme := repository.TenantScope{ID: "drv-vars-acme"}
		globex := repository.TenantScope{ID: "drv-vars-globex"}

		base, err := store.CreateVariable(ctx, acme, "API_BASE", "https://acme.example")
		if err != nil {
			t.Fatalf("CreateVariable() error = %v", err)
		}
		if _, err := store.CreateVariable(ctx, acme, "ENV", "production"); err != nil {
			t.Fatalf("CreateVariable(ENV) error = %v", err)
		}
		if _, err := store.CreateVariable(ctx, globex, "API_BASE", "https://globex.example"); err != nil {
			t.Fatalf("another tenant could not reuse the key: %v", err)
		}
		if _, err := store.CreateVariable(ctx, acme, "API_BASE", "again"); !errors.Is(err, repository.ErrAlreadyExists) {
			t.Fatalf("a duplicate key error = %v, want ErrAlreadyExists", err)
		}

		values, err := store.VariablesFor(ctx, acme)
		if err != nil {
			t.Fatalf("VariablesFor() error = %v", err)
		}
		if len(values) != 2 || values["API_BASE"] != "https://acme.example" || values["ENV"] != "production" {
			t.Fatalf("VariablesFor(acme) = %v, want acme's two variables", values)
		}

		listed, err := store.ListVariables(ctx, acme)
		if err != nil {
			t.Fatalf("ListVariables() error = %v", err)
		}
		if len(listed) != 2 || listed[0].Key != "API_BASE" || listed[1].Key != "ENV" {
			t.Fatalf("ListVariables(acme) = %+v, want both, ordered by key", listed)
		}

		if _, err := store.UpdateVariable(ctx, globex, base.ID, "API_BASE", "stolen"); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("a foreign update error = %v, want ErrNotFound", err)
		}
		if err := store.DeleteVariable(ctx, globex, base.ID); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("a foreign delete error = %v, want ErrNotFound", err)
		}

		updated, err := store.UpdateVariable(ctx, acme, base.ID, "API_URL", "https://acme.example/v2")
		if err != nil {
			t.Fatalf("UpdateVariable() error = %v", err)
		}
		if updated.Key != "API_URL" || updated.Value != "https://acme.example/v2" {
			t.Fatalf("UpdateVariable() = %+v", updated)
		}
		if _, err := store.UpdateVariable(ctx, acme, base.ID, "ENV", "x"); !errors.Is(err, repository.ErrAlreadyExists) {
			t.Fatalf("renaming onto a taken key error = %v, want ErrAlreadyExists", err)
		}
		if err := store.DeleteVariable(ctx, acme, base.ID); err != nil {
			t.Fatalf("DeleteVariable() error = %v", err)
		}
		values, _ = store.VariablesFor(ctx, acme)
		if _, found := values["API_URL"]; found || len(values) != 1 {
			t.Fatalf("after delete VariablesFor(acme) = %v, want only ENV", values)
		}
	})
}

// Keys follow n8n's rule, so every one can be read as `$vars.KEY`.
func TestVariableKeysAndValuesAreValidated(t *testing.T) {
	for name, input := range map[string]struct{ key, value string }{
		"empty key":           {"", "x"},
		"leading digit":       {"1ST", "x"},
		"dash":                {"API-BASE", "x"},
		"space":               {"API BASE", "x"},
		"too long key":        {strings.Repeat("K", repository.VariableKeyMaxLength+1), "x"},
		"too long value":      {"BIG", strings.Repeat("v", repository.VariableValueMaxLength+1)},
		"invalid utf-8 value": {"BAD", string([]byte{0xff, 0xfe})},
	} {
		t.Run(name, func(t *testing.T) {
			if err := repository.ValidateVariable(input.key, input.value); !errors.Is(err, repository.ErrVariableInvalid) {
				t.Fatalf("ValidateVariable(%q) error = %v, want ErrVariableInvalid", input.key, err)
			}
		})
	}
	for _, key := range []string{"API_BASE", "_private", "a1", "camelCase"} {
		if err := repository.ValidateVariable(key, ""); err != nil {
			t.Errorf("ValidateVariable(%q) error = %v, want valid", key, err)
		}
	}
}
