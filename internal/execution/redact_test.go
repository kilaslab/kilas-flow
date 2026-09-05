package execution_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/execution"
)

func TestRedactRemovesAuthorizationHeaderMaterial(t *testing.T) {
	t.Parallel()

	secrets := []string{"c3VwZXItc2VjcmV0", "sk-live-4242", "session-cookie-value"}
	payload := json.RawMessage(`{
		"headers": {
			"Authorization": "Basic c3VwZXItc2VjcmV0",
			"Cookie": "sid=session-cookie-value",
			"Content-Type": "application/json"
		},
		"items": [
			{"json": {"apiKey": "sk-live-4242", "customer": "Ada"}}
		]
	}`)

	redacted := execution.Redact(payload)

	for _, secret := range secrets {
		if strings.Contains(string(redacted), secret) {
			t.Fatalf("redacted payload still contains %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(string(redacted), "Ada") {
		t.Fatalf("redaction dropped non-sensitive data: %s", redacted)
	}
	if !strings.Contains(string(redacted), "application/json") {
		t.Fatalf("redaction dropped a safe header: %s", redacted)
	}
}

func TestRedactHandlesHeaderNameValuePairs(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`[{"name":"authorization","value":"Bearer abcd.efgh.ijkl"}]`)

	redacted := string(execution.Redact(payload))

	if strings.Contains(redacted, "abcd.efgh.ijkl") {
		t.Fatalf("bearer token survived redaction: %s", redacted)
	}
	if !strings.Contains(redacted, "authorization") {
		t.Fatalf("redaction dropped the header name: %s", redacted)
	}
}

func TestRedactMatchesKeysRegardlessOfCaseOrSeparator(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"Authorization", "X-Api-Key", "access_token", "clientSecret", "Set-Cookie", "PASSWORD", "private_key", "refreshToken"} {
		payload := json.RawMessage(`{"` + key + `":"leak-me"}`)
		if redacted := string(execution.Redact(payload)); strings.Contains(redacted, "leak-me") {
			t.Fatalf("key %q was not redacted: %s", key, redacted)
		}
	}
}

func TestRedactKeepsStructureAndNonSensitiveValues(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`{"status":"ready","count":3,"nested":{"ok":true,"list":[1,"two",null]}}`)

	redacted := execution.Redact(payload)

	var got map[string]any
	if err := json.Unmarshal(redacted, &got); err != nil {
		t.Fatalf("redacted payload is not valid JSON: %v", err)
	}
	if got["status"] != "ready" || got["count"] != float64(3) {
		t.Fatalf("redaction altered safe scalars: %s", redacted)
	}
	nested, ok := got["nested"].(map[string]any)
	if !ok || nested["ok"] != true || len(nested["list"].([]any)) != 3 {
		t.Fatalf("redaction altered nested structure: %s", redacted)
	}
}

func TestRedactPassesThroughEmptyAndInvalidPayloads(t *testing.T) {
	t.Parallel()

	if got := execution.Redact(nil); got != nil {
		t.Fatalf("expected nil for nil payload, got %s", got)
	}
	// Invalid JSON must survive untouched: the repository rejects it later with
	// a precise error instead of storing a silently rewritten payload.
	invalid := json.RawMessage(`{"broken"`)
	if got := string(execution.Redact(invalid)); got != string(invalid) {
		t.Fatalf("invalid JSON was rewritten: %s", got)
	}
}

func TestRedactIsSafeAtDepth(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`{"a":{"b":{"c":{"d":{"token":"deep-secret"}}}}}`)

	if redacted := string(execution.Redact(payload)); strings.Contains(redacted, "deep-secret") {
		t.Fatalf("deeply nested secret survived: %s", redacted)
	}
}
