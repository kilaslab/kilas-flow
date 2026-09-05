package execution_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/execution"
)

// TestRedactionRules names, for every key and every shape, whether it is
// redacted and why.
//
// The two halves are equally load-bearing. Under-redacting leaks a credential
// the runtime resolved; over-redacting stands between the wire and the runtime,
// because the runner rehydrates a trigger item straight out of the stored
// record and a workflow then runs on "[redacted]" instead of its data.
func TestRedactionRules(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		payload  string
		redacted []string
		kept     []string
		why      string
	}{
		{
			name:     "authorization header",
			payload:  `{"headers":{"Authorization":"Bearer abc123"}}`,
			redacted: []string{"abc123"},
			why:      "a resolved credential is what redaction exists to catch",
		},
		{
			name:     "vendor-prefixed api key",
			payload:  `{"X-Api-Key":"k-secret","x_api_key":"k-secret-2","apiKey":"k-secret-3"}`,
			redacted: []string{"k-secret", "k-secret-2", "k-secret-3"},
			why:      "key normalisation collapses casing, separators and the x- prefix",
		},
		{
			name:     "password and secret keys",
			payload:  `{"password":"p","clientSecret":"s","privateKey":"k","passphrase":"ph"}`,
			redacted: []string{`"p"`, `"s"`, `"k"`, `"ph"`},
			why:      "these name a credential whatever they contain",
		},
		{
			name:     "header name/value pair",
			payload:  `{"header":{"name":"Authorization","value":"Bearer pair-secret"}}`,
			redacted: []string{"pair-secret"},
			why:      "the credential hides under an innocent key, so the pair is matched",
		},
		{
			name:    "harmless name/value pair",
			payload: `{"header":{"name":"Accept","value":"application/json"}}`,
			kept:    []string{"application/json"},
			why:     "a value is only redacted when the name beside it is sensitive",
		},
		{
			name:    "WAHA session",
			payload: `{"session":"default","payload":{"sessionId":"628123@c.us"}}`,
			kept:    []string{"default", "628123@c.us"},
			why: "these name a WhatsApp session and a conversation, not a credential; " +
				"redacting them sent [redacted] to the WAHA API and merged every chat into one memory bucket",
		},
		{
			name:     "session token is still a credential",
			payload:  `{"sessionToken":"st-secret"}`,
			redacted: []string{"st-secret"},
			why:      "sessionToken is a credential even though session is not",
		},
		{
			name:    "scheme-prefixed prose",
			payload: `{"message":"basic plan pricing?","note":"bearer of this letter","memo":"digest of the meeting"}`,
			kept:    []string{"basic plan pricing?", "bearer of this letter", "digest of the meeting"},
			why:     "content is never inspected: an auth scheme string is also an ordinary English word",
		},
		{
			name:    "one-time codes and pins are user data",
			payload: `{"otp":"123456","pin":"pin the message"}`,
			kept:    []string{"123456", "pin the message"},
			why:     "neither is ever a KilasFlow credential; both arrive as the user's own words",
		},
		{
			name:     "nested inside arrays",
			payload:  `{"items":[{"token":"deep-secret"},{"body":"safe"}]}`,
			redacted: []string{"deep-secret"},
			kept:     []string{"safe"},
			why:      "redaction walks the whole document, not just the top level",
		},
		{
			name:    "shape is preserved",
			payload: `{"a":null,"b":true,"c":1.5,"d":[],"e":{}}`,
			kept:    []string{`"a":null`, `"b":true`, `"c":1.5`, `"d":[]`, `"e":{}`},
			why:     "an inspector must still show the structure of what ran",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := string(execution.Redact(json.RawMessage(testCase.payload)))
			for _, secret := range testCase.redacted {
				if strings.Contains(got, secret) {
					t.Errorf("Redact(%s) kept %q, want it redacted — %s", testCase.payload, secret, testCase.why)
				}
			}
			for _, value := range testCase.kept {
				if !strings.Contains(got, value) {
					t.Errorf("Redact(%s) destroyed %q, want it kept — %s", testCase.payload, value, testCase.why)
				}
			}
		})
	}
}

// TestRedactTriggerHeadersTouchesOnlyTheHeaders pins the trigger boundary. The
// body, query, method and path are the caller's data and the workflow runs on
// them; only the header map can hold a caller's credential.
func TestRedactTriggerHeadersTouchesOnlyTheHeaders(t *testing.T) {
	t.Parallel()

	const payload = `{"method":"POST","path":"waha","query":{"token":"query-value"},` +
		`"headers":{"Authorization":"Bearer header-secret","Accept":"application/json"},` +
		`"body":{"session":"default","token":"body-value","message":"basic plan?"}}`

	got := string(execution.RedactTriggerHeaders(json.RawMessage(payload)))
	if strings.Contains(got, "header-secret") {
		t.Errorf("the header credential survived: %s", got)
	}
	for _, kept := range []string{"query-value", "body-value", "default", "basic plan?", "application/json", "POST", "waha"} {
		if !strings.Contains(got, kept) {
			t.Errorf("RedactTriggerHeaders destroyed %q, which the workflow runs on: %s", kept, got)
		}
	}
}

// TestRedactLeavesUnreadablePayloadsAlone keeps the existing contract: the
// repository reports invalid JSON precisely rather than storing something this
// function rewrote.
func TestRedactLeavesUnreadablePayloadsAlone(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{"", "not json", "{", `"a bare string"`, "42"} {
		got := string(execution.Redact(json.RawMessage(payload)))
		if got != payload {
			t.Errorf("Redact(%q) = %q, want it untouched", payload, got)
		}
	}
}

// TestPreviouslyRedactedRecordsStillReadBack covers records written before this
// change.
//
// Nothing about the storage format moved — only which values reach it — so an
// older record holds "[redacted]" where a session used to be. It must still
// read back cleanly, and redacting it again must be a no-op rather than
// something that compounds.
func TestPreviouslyRedactedRecordsStillReadBack(t *testing.T) {
	t.Parallel()

	const stored = `{"headers":{"Authorization":"[redacted]"},` +
		`"body":{"session":"[redacted]","message":"[redacted]"}}`

	again := string(execution.Redact(json.RawMessage(stored)))
	if !json.Valid([]byte(again)) {
		t.Fatalf("re-reading an older record produced invalid JSON: %s", again)
	}
	// The marker is a plain string under a non-sensitive key now, so it stays
	// exactly as it is rather than being wrapped or re-marked.
	for _, expected := range []string{`"session":"[redacted]"`, `"message":"[redacted]"`} {
		if !strings.Contains(again, expected) {
			t.Errorf("older record changed shape on read: %s, want %s intact", again, expected)
		}
	}
	if strings.Contains(again, "[redacted][redacted]") {
		t.Errorf("redaction compounded on an already-redacted record: %s", again)
	}
}
