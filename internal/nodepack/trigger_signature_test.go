package nodepack_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// signedManifest wraps one hmac block in an otherwise well-formed trigger
// pack, optionally with a pack credentialType.
func signedManifest(credentialType, signature string) []byte {
	return []byte(`{
		"type": "pack.signedTrigger", "version": 1,
		"displayName": "Signed Trigger", "category": "Triggers",
		` + credentialField(credentialType) + `
		"requestDefaults": {},
		"trigger": {
			"events": ["message"], "catchAll": "other", "eventPath": "event", "shape": "bodyAsItem",
			"webhook": {"name": "default", "staticPath": "signed", "method": "POST"},
			"hmac": ` + signature + `
		}
	}`)
}

func credentialField(credentialType string) string {
	if credentialType == "" {
		return ""
	}
	return `"credentialType": "` + credentialType + `",`
}

// A trigger's hmac block can spell the schemes real services sign with, and
// the pack validator refuses the ones it cannot honour before a workflow ever
// carries them.
func TestATriggerHMACBlockCanSpellCommonSchemes(t *testing.T) {
	t.Parallel()

	for name, manifest := range map[string][]byte{
		"the waha form":              signedManifest("", `{"header": "X-Webhook-Hmac", "algorithm": "sha512", "secretParameter": "secret"}`),
		"github form":                signedManifest("", `{"header": "X-Hub-Signature-256", "algorithm": "sha256", "encoding": "hex", "prefix": "sha256=", "secretParameter": "secret"}`),
		"a signed timestamp":         signedManifest("", `{"header": "X-Signature", "algorithm": "sha256", "encoding": "hex", "signingString": "{{ .timestamp }}.{{ .body }}", "timestampHeader": "X-Timestamp", "toleranceSeconds": 300, "secretParameter": "secret"}`),
		"a standard-webhooks preset": signedManifest("", `{"preset": "standardWebhooks", "secretParameter": "secret"}`),
		"a credential-held secret":   signedManifest("acmeApi", `{"header": "X-Signature", "algorithm": "sha256", "secretCredential": {"type": "acmeApi", "field": "signingSecret"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if issues := nodepack.ValidateBytes(manifest, "pack.json"); len(issues) != 0 {
				t.Fatalf("a well-formed hmac block was refused: %v", issues)
			}
		})
	}

	for name, testCase := range map[string]struct {
		credentialType string
		signature      string
		want           string
	}{
		"an unknown algorithm":                        {signature: `{"header": "X-S", "algorithm": "md5", "secretParameter": "secret"}`, want: "use sha256 or sha512"},
		"an unknown encoding":                         {signature: `{"header": "X-S", "algorithm": "sha256", "encoding": "base32", "secretParameter": "secret"}`, want: "hex or base64"},
		"no header":                                   {signature: `{"algorithm": "sha256", "secretParameter": "secret"}`, want: "the header"},
		"a typo in the template":                      {signature: `{"header": "X-S", "algorithm": "sha256", "signingString": "{{ .boday }}", "secretParameter": "secret"}`, want: "may name only"},
		"an expression in the template":               {signature: `{"header": "X-S", "algorithm": "sha256", "signingString": "{{ $json.id }}", "secretParameter": "secret"}`, want: "placeholders"},
		"a timestamp from nowhere":                    {signature: `{"header": "X-S", "algorithm": "sha256", "signingString": "{{ .timestamp }}.{{ .body }}", "secretParameter": "secret"}`, want: "timestampHeader"},
		"a tolerance with no header":                  {signature: `{"header": "X-S", "algorithm": "sha256", "toleranceSeconds": 60, "secretParameter": "secret"}`, want: "no timestampHeader"},
		"a negative tolerance":                        {signature: `{"header": "X-S", "algorithm": "sha256", "timestampHeader": "X-T", "toleranceSeconds": -5, "secretParameter": "secret"}`, want: "negative"},
		"an unknown preset":                           {signature: `{"preset": "stripeAllAtOnce", "secretParameter": "secret"}`, want: "only preset"},
		"a preset beside a scheme field":              {signature: `{"preset": "standardWebhooks", "algorithm": "sha256", "secretParameter": "secret"}`, want: "the whole scheme"},
		"a preset beside a header":                    {signature: `{"preset": "standardWebhooks", "header": "X-S", "secretParameter": "secret"}`, want: "the whole scheme"},
		"two secret sources":                          {signature: `{"header": "X-S", "algorithm": "sha256", "secretParameter": "secret", "secretCredential": {"type": "acmeApi", "field": "f"}}`, want: "one secret"},
		"an incomplete credential":                    {signature: `{"header": "X-S", "algorithm": "sha256", "secretCredential": {"type": "acmeApi"}}`, want: "needs both"},
		"a credential type the pack does not declare": {credentialType: "acmeApi", signature: `{"header": "X-S", "algorithm": "sha256", "secretCredential": {"type": "otherApi", "field": "f"}}`, want: "credentialType is"},
		"a credential with no pack credentialType":    {signature: `{"header": "X-S", "algorithm": "sha256", "secretCredential": {"type": "acmeApi", "field": "f"}}`, want: "declares no credentialType"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			issues := nodepack.ValidateBytes(signedManifest(testCase.credentialType, testCase.signature), "pack.json")
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

// TestTheStandardWebhooksPresetVerifiesRealDeliveries runs the preset
// end-to-end through a trigger's kind: a delivery signed the way the spec
// says passes, a stale one is refused as a replay, and a rotation pair passes
// while the sender and this endpoint swap secrets.
func TestTheStandardWebhooksPresetVerifiesRealDeliveries(t *testing.T) {
	t.Parallel()

	const secret = "whsec_current"
	const body = `{"type":"message.created","data":{"id":"m-1"}}`

	trigger := &nodepack.Trigger{
		HMAC: &nodepack.TriggerHMAC{Preset: nodepack.StandardWebhooksPreset, SecretParameter: "secret"},
	}
	kind := trigger.TriggerKind()

	delivery := func(t *testing.T, timestamp, signature string, parameters map[string]any) webhook.Delivery {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", nil)
		if timestamp != "" {
			request.Header.Set("webhook-timestamp", timestamp)
		}
		if signature != "" {
			request.Header.Set("webhook-signature", signature)
		}
		return webhook.Delivery{Request: request, RawBody: []byte(body), Binding: repository.WebhookBinding{Parameters: parameters}}
	}
	sign := func(secret, timestamp string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp + "." + body))
		return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	withSecret := map[string]any{"secret": secret}
	now := strconv.FormatInt(time.Now().Unix(), 10)

	if !kind.Verifies(delivery(t, "", "", withSecret)) {
		t.Fatal("a trigger holding a secret did not count as authenticating its senders")
	}
	if err := kind.Verify(delivery(t, now, sign(secret, now), withSecret)); err != nil {
		t.Fatalf("a spec-shaped delivery was refused: %v", err)
	}

	stale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	if err := kind.Verify(delivery(t, stale, sign(secret, stale), withSecret)); err == nil {
		t.Fatal("a delivery ten minutes old was accepted; the preset's tolerance is five")
	}

	rotation := sign("whsec_retired", now) + "," + sign(secret, now)
	if err := kind.Verify(delivery(t, now, rotation, withSecret)); err != nil {
		t.Fatalf("a rotation pair carrying the current secret was refused: %v", err)
	}

	// An unsigned delivery from a node holding the secret is a refusal, never
	// a pass; one from a node without any secret configured is the state every
	// imported workflow arrives in, and is not verified at all.
	if err := kind.Verify(delivery(t, now, "", withSecret)); err == nil {
		t.Fatal("an unsigned delivery was accepted")
	}
	if kind.Verifies(delivery(t, "", "", nil)) {
		t.Fatal("a trigger with no secret configured counted as authenticating its senders")
	}
	if err := kind.Verify(delivery(t, "", "", nil)); err != nil {
		t.Fatalf("a node with no secret configured refused a delivery: %v", err)
	}
}

// TestTheSecretCanComeFromACredentialField covers the storage rule a
// `secretParameter` breaks: the workflow document never carries the secret,
// the node's credential does, and the delivery reads it through the resolver
// the HTTP boundary provides — never decrypting for a delivery that has no
// credential bound.
func TestTheSecretCanComeFromACredentialField(t *testing.T) {
	t.Parallel()

	const body = `{"event":"message"}`
	trigger := &nodepack.Trigger{
		HMAC: &nodepack.TriggerHMAC{
			Header: "X-Signature", Algorithm: "sha256",
			SecretCredential: &nodepack.TriggerSecretCredential{Type: "acmeApi", Field: "signingSecret"},
		},
	}
	kind := trigger.TriggerKind()

	sign := func(secret string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		// Uppercase hex, to prove the hex comparison is case-insensitive on
		// this path too.
		return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
	}
	delivery := func(t *testing.T, signature string, credentialID string, resolver func(string) (map[string]string, error)) webhook.Delivery {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", nil)
		if signature != "" {
			request.Header.Set("X-Signature", signature)
		}
		binding := repository.WebhookBinding{Parameters: map[string]any{}}
		if credentialID != "" {
			binding.Parameters["$credentials"] = map[string]any{"acmeApi": credentialID}
		}
		return webhook.Delivery{Request: request, RawBody: []byte(body), Binding: binding, Credential: resolver}
	}
	bound := func(fields map[string]string, fail error) func(string) (map[string]string, error) {
		return func(string) (map[string]string, error) { return fields, fail }
	}

	const id = "credential_1"
	if err := kind.Verify(delivery(t, sign("sealed-secret"), id, bound(map[string]string{"signingSecret": "sealed-secret"}, nil))); err != nil {
		t.Fatalf("a delivery signed with the credential's field was refused: %v", err)
	}
	if err := kind.Verify(delivery(t, sign("a-parameter-is-not-the-secret"), id, bound(map[string]string{"signingSecret": "sealed-secret"}, nil))); err == nil {
		t.Fatal("a delivery signed with the wrong secret was accepted")
	}
	// A credential bound but unreadable — wrong type, sealed with another key
	// — is a configured verification that failed, not verification turned off.
	if err := kind.Verify(delivery(t, sign("x"), id, bound(nil, errCredentialUnreadable))); err == nil {
		t.Fatal("a delivery was accepted while its credential could not be read")
	}
	if err := kind.Verify(delivery(t, sign("x"), id, bound(map[string]string{}, nil))); err == nil {
		t.Fatal("a delivery was accepted from a credential with no signing field")
	}
	// No credential bound: the same state as an empty secretParameter.
	if kind.Verifies(delivery(t, "", "", bound(map[string]string{"signingSecret": "sealed-secret"}, nil))) {
		t.Fatal("a node with no credential bound counted as authenticating its senders")
	}
	if err := kind.Verify(delivery(t, "", "", bound(map[string]string{"signingSecret": "sealed-secret"}, nil))); err != nil {
		t.Fatalf("a node with no credential bound refused a delivery: %v", err)
	}
}

var errCredentialUnreadable = &credentialError{}

type credentialError struct{}

func (*credentialError) Error() string { return "sealed with another key" }
