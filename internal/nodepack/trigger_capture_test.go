package nodepack_test

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// capturingManifest is a trigger pack whose lifecycle and HMAC are the
// fragments given.
func capturingManifest(lifecycle, signature string) []byte {
	return []byte(`{
		"type": "pack.subscriptionTrigger", "version": 1,
		"displayName": "Subscription Trigger", "category": "Triggers",
		"trigger": {
			"events": ["message"], "catchAll": "other", "eventPath": "event", "shape": "bodyAsItem",
			"webhook": {"name": "default", "staticPath": "subscription", "method": "POST"},
			"hmac": ` + signature + `,
			"lifecycle": ` + lifecycle + `
		}
	}`)
}

// subscriptionLifecycleJSON registers a subscription, keeps its id and its
// signing secret, and checks and removes it by that id.
const subscriptionLifecycleJSON = `{
	"id": "subscription.webhook",
	"check": {"method": "GET", "url": "{{ .baseUrl }}/subscriptions/{{ .Captured.id }}"},
	"set": {"method": "POST", "url": "{{ .baseUrl }}/subscriptions", "capture": {"id": "data.id", "secret": "data.secret"}},
	"remove": {"method": "DELETE", "url": "{{ .baseUrl }}/subscriptions/{{ .Captured.id }}"}
}`

const capturedSecretJSON = `{"header": "X-Signature", "algorithm": "sha512", "secretCapture": "secret"}`

// A manifest may capture from the answer to `set` and nowhere else, and only
// what a template can name from a path that names something. An HMAC secret
// may be one of those values — but only one the lifecycle captures, and not
// beside a parameter, since a delivery is checked against one secret.
func TestAManifestCapturesOnlyFromSetAndOnlyWhatItCanName(t *testing.T) {
	t.Parallel()

	if issues := nodepack.ValidateBytes(capturingManifest(subscriptionLifecycleJSON, capturedSecretJSON), "pack.json"); len(issues) != 0 {
		t.Fatalf("a manifest capturing from set was refused: %v", issues)
	}

	for name, testCase := range map[string]struct {
		lifecycle string
		signature string
		want      string
	}{
		"capture on check": {
			lifecycle: strings.Replace(subscriptionLifecycleJSON, `{{ .Captured.id }}"}`, `{{ .Captured.id }}", "capture": {"id": "data.id"}}`, 1),
			signature: "null", want: "lifecycle check declares capture",
		},
		"capture on remove": {
			lifecycle: `{"id": "x", "remove": {"method": "DELETE", "url": "{{ .baseUrl }}/x", "capture": {"id": "data.id"}}}`,
			signature: "null", want: "lifecycle remove declares capture",
		},
		"an empty path": {
			lifecycle: `{"id": "x", "set": {"method": "POST", "url": "{{ .baseUrl }}/x", "capture": {"id": ""}}}`,
			signature: "null", want: "names no value",
		},
		"a path with an empty segment": {
			lifecycle: `{"id": "x", "set": {"method": "POST", "url": "{{ .baseUrl }}/x", "capture": {"id": "data..id"}}}`,
			signature: "null", want: "names no value",
		},
		"an empty key": {
			lifecycle: `{"id": "x", "set": {"method": "POST", "url": "{{ .baseUrl }}/x", "capture": {"": "data.id"}}}`,
			signature: "null", want: "letters, digits and underscores",
		},
		"a key no template can name": {
			lifecycle: `{"id": "x", "set": {"method": "POST", "url": "{{ .baseUrl }}/x", "capture": {"sub-id": "data.id"}}}`,
			signature: "null", want: "letters, digits and underscores",
		},
		"a secret nothing captures": {
			lifecycle: subscriptionLifecycleJSON,
			signature: `{"header": "X-Signature", "algorithm": "sha512", "secretCapture": "token"}`,
			want:      "does not capture",
		},
		"a secret from a capture and a parameter both": {
			lifecycle: subscriptionLifecycleJSON,
			signature: `{"header": "X-Signature", "algorithm": "sha512", "secretParameter": "hmacSecret", "secretCapture": "secret"}`,
			want:      "one secret",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			issues := nodepack.ValidateBytes(capturingManifest(testCase.lifecycle, testCase.signature), "pack.json")
			found := false
			for _, issue := range issues {
				found = found || strings.Contains(issue.Message, testCase.want)
			}
			if !found {
				t.Fatalf("issues = %v, want one mentioning %q", issues, testCase.want)
			}
		})
	}
}

// A service that generates the secret it signs with says what it is once, in
// the answer to the registration. The trigger then verifies every delivery with
// the value that answer was captured for, exactly as it would with a secret the
// node was configured with — and with that value only: a parameter that
// happens to share the key's name is not the secret.
func TestAnHMACSecretCanBeAValueTheRegistrationCaptured(t *testing.T) {
	t.Parallel()

	trigger := &nodepack.Trigger{
		HMAC: &nodepack.TriggerHMAC{Header: "X-Signature", Algorithm: "sha512", SecretCapture: "secret"},
	}
	kind := trigger.TriggerKind()
	body := []byte(`{"event":"message"}`)
	sign := func(secret string) string {
		mac := hmac.New(sha512.New, []byte(secret))
		mac.Write(body)
		return hex.EncodeToString(mac.Sum(nil))
	}
	delivery := func(captured map[string]string, signature string) webhook.Delivery {
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", nil)
		if signature != "" {
			request.Header.Set("X-Signature", signature)
		}
		return webhook.Delivery{
			Request: request, RawBody: body,
			Binding: repository.WebhookBinding{
				Parameters: map[string]any{"secret": "a-parameter-is-not-the-secret"},
				Captured:   captured,
			},
		}
	}
	captured := map[string]string{"id": "sub-1", "secret": "whsec_1"}

	if !kind.Verifies(delivery(captured, "")) {
		t.Fatal("a trigger holding a captured secret did not count as authenticating its senders")
	}
	if err := kind.Verify(delivery(captured, sign("whsec_1"))); err != nil {
		t.Fatalf("a delivery signed with the captured secret was refused: %v", err)
	}
	for name, signature := range map[string]string{
		"unsigned":                  "",
		"signed with the parameter": sign("a-parameter-is-not-the-secret"),
		"signed with another key":   sign("whsec_2"),
	} {
		if err := kind.Verify(delivery(captured, signature)); err == nil {
			t.Errorf("a delivery %s was accepted", name)
		}
	}

	// Nothing captured yet — the registration is off, or has not run — is the
	// same as a node with no secret configured: not verified, and not counted
	// as authenticating.
	if kind.Verifies(delivery(nil, "")) {
		t.Error("a trigger with nothing captured counted as authenticating its senders")
	}
	if err := kind.Verify(delivery(nil, "")); err != nil {
		t.Errorf("a trigger with nothing captured refused a delivery: %v", err)
	}
}
