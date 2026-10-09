package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// goneCredentials answers every lookup the way the credential store answers
// for a credential that was deleted.
type goneCredentials struct{}

func (goneCredentials) Resolve(context.Context, repository.TenantScope, string) (credentials.Record, map[string]string, error) {
	return credentials.Record{}, nil, fmt.Errorf("%w: credential", repository.ErrNotFound)
}

// A run whose node points at a deleted credential used to fail with
// `node "Call API": resolve credential: repository record not found:
// credential`, which names neither the credential nor the fix.
func TestAMissingCredentialIsNamedInTheRunError(t *testing.T) {
	request := engine.Request{Credentials: engine.NewTenantCredentials(goneCredentials{}, repository.TenantScope{ID: "acme"})}
	node := workflow.IRNode{
		ID: "h", Name: "Call API", Type: "kilasflow.httpRequest",
		Credentials: map[string]string{"httpHeaderAuth": "cred_deleted_1"},
	}
	_, _, err := request.ResolveAttachedCredential(context.Background(), node, "httpHeaderAuth")
	if err == nil {
		t.Fatal("a deleted credential resolved")
	}
	message := err.Error()
	for _, want := range []string{`"Call API"`, `"cred_deleted_1"`, "no longer exists", "attach another credential"} {
		if !strings.Contains(message, want) {
			t.Errorf("run error = %q, want it to contain %s", message, want)
		}
	}
	if strings.Contains(message, "repository record not found") {
		t.Errorf("run error = %q still carries the storage wording", message)
	}
	if !errors.Is(err, repository.ErrNotFound) {
		t.Error("the run error no longer wraps ErrNotFound, so a caller cannot tell a missing credential from a failure")
	}
}
