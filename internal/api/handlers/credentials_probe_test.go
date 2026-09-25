package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// Live, a SQLite probe blocked in its driver and never returned, so the
// in-flight claim was never released and every later test of that credential
// answered 409 for the life of the process. The handler must answer by its
// deadline and release the claim whatever the probe does.
func TestACredentialTestThatNeverReturnsStillReleasesItsClaim(t *testing.T) {
	t.Parallel()

	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })
	handler := NewCredentials(nil, nil).WithTestTimeout(100 * time.Millisecond)
	handler.probeFunc = func(context.Context, repository.TenantScope, credentials.Record, map[string]string, []string) *testCredentialOutput {
		<-unblock
		return &testCredentialOutput{Body: TestCredentialResource{OK: true}}
	}

	input := &testPayloadInput{Type: "sqlite"}
	input.Body.Fields = map[string]string{"path": "orders.db"}

	for attempt := 1; attempt <= 2; attempt++ {
		started := time.Now()
		output, err := handler.TestPayload(context.Background(), input)
		if err != nil {
			var status huma.StatusError
			if errors.As(err, &status) && status.GetStatus() == 409 {
				t.Fatalf("attempt %d answered 409: the first test's claim was never released", attempt)
			}
			t.Fatalf("attempt %d: TestPayload() error = %v", attempt, err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("attempt %d answered after %s against a 100ms deadline", attempt, elapsed)
		}
		if output.Body.OK {
			t.Fatalf("attempt %d reported a probe that never finished as ok", attempt)
		}
		if output.Body.Detail == "" {
			t.Errorf("attempt %d gave no detail for a probe that ran out of time", attempt)
		}
	}
}

// A release called twice, or after a newer test claimed the same credential,
// must not remove the newer claim.
func TestAStaleClaimReleaseCannotFreeANewerClaim(t *testing.T) {
	t.Parallel()

	handler := NewCredentials(nil, nil)
	tenant := repository.TenantScope{ID: "acme"}

	release, err := handler.claim(tenant, "cred-1")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	release()
	if _, err := handler.claim(tenant, "cred-1"); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	release() // stale: must leave the newer claim in place
	if _, err := handler.claim(tenant, "cred-1"); err == nil {
		t.Fatal("a stale release freed the newer claim, so two tests of one credential ran at once")
	}
}
