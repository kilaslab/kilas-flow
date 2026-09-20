package nodepack_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// A pack trigger with an HMAC declared still verifies nothing until a secret is
// configured, so "has a verifier" and "authenticates its callers" are different
// questions. webhook.require_auth asks the second one.
func TestAPackTriggerCountsAsAuthenticatedOnlyWhileItHoldsAnHMACSecret(t *testing.T) {
	trigger := &nodepack.Trigger{
		HMAC: &nodepack.TriggerHMAC{Header: "X-Webhook-Hmac", Algorithm: "sha512", SecretParameter: "hmacSecret"},
	}
	kind := trigger.TriggerKind()
	if kind.Verify == nil {
		t.Fatal("an HMAC trigger registered no verifier")
	}
	if kind.Verifies == nil {
		t.Fatal("an HMAC trigger did not report whether it verifies a given binding")
	}

	held := webhook.Delivery{Binding: repository.WebhookBinding{Parameters: map[string]any{"hmacSecret": "s3cret"}}}
	if !kind.Verifies(held) {
		t.Error("a trigger holding a secret did not count as authenticating its senders")
	}

	for _, parameters := range []map[string]any{nil, {}, {"hmacSecret": ""}, {"hmacSecret": "   "}} {
		delivery := webhook.Delivery{Binding: repository.WebhookBinding{Parameters: parameters}}
		if kind.Verifies(delivery) {
			t.Errorf("a trigger with parameters %v counted as authenticating its senders", parameters)
		}
	}

	bare := &nodepack.Trigger{}
	bareKind := bare.TriggerKind()
	if bareKind.Verify != nil || bareKind.Verifies != nil {
		t.Error("a trigger with no HMAC reported verification")
	}
}
