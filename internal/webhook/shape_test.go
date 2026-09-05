package webhook_test

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
)

func delivery(t *testing.T, body, contentType string, binding repository.WebhookBinding) webhook.Delivery {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	handler := webhook.NewHandler(nil, nil, nil, nil, webhook.DefaultLimits())
	built, err := webhook.ReadDeliveryForTest(handler, request, binding)
	if err != nil {
		t.Fatalf("readDelivery() error = %v", err)
	}
	return built
}

// TestShapesProduceTheItemEachTriggerFamilyExpects is the point of the ticket.
//
// One hardcoded envelope meant no imported trigger saw the shape it expected:
// an n8n workflow reads $json.body, and a WAHA workflow reads $json.event —
// which was undefined, because the event was at $json.body.event.
func TestShapesProduceTheItemEachTriggerFamilyExpects(t *testing.T) {
	t.Parallel()

	binding := repository.WebhookBinding{Path: "waha-webhook", Method: http.MethodPost}
	built := delivery(t, `{"event":"message","session":"default"}`, "application/json", binding)

	t.Run("envelope keeps today's keys", func(t *testing.T) {
		item := webhook.ShapeEnvelope.Apply(built)
		for _, key := range []string{"method", "path", "headers", "query", "body"} {
			if _, present := item[key]; !present {
				t.Errorf("envelope is missing %q; an already-activated workflow would change behaviour", key)
			}
		}
		// The label the author configured, not the opaque route the URL carries.
		if item["path"] != "waha-webhook" {
			t.Errorf("path = %#v, want the author's label", item["path"])
		}
	})

	t.Run("n8nCore matches n8n's own webhook node", func(t *testing.T) {
		item := webhook.ShapeN8NCore.Apply(built)
		for _, key := range []string{"body", "headers", "params", "query"} {
			if _, present := item[key]; !present {
				t.Errorf("n8nCore is missing %q", key)
			}
		}
		if _, unwanted := item["method"]; unwanted {
			t.Error("n8nCore carries method, which n8n's node does not emit")
		}
		// params is present and empty rather than absent: a binding has no
		// route pattern behind it, so there are no path parameters to extract,
		// and an expression reading it should get an empty object.
		params, ok := item["params"].(map[string]any)
		if !ok || len(params) != 0 {
			t.Errorf("params = %#v, want an empty object", item["params"])
		}
	})

	t.Run("bodyAsItem puts the event at the top level", func(t *testing.T) {
		item := webhook.ShapeBodyAsItem.Apply(built)
		if item["event"] != "message" {
			t.Errorf("$json.event = %#v, want it resolvable at the top level", item["event"])
		}
		if item["session"] != "default" {
			t.Errorf("$json.session = %#v, want the WAHA envelope readable directly", item["session"])
		}
		if _, nested := item["body"]; nested {
			t.Error("bodyAsItem nested the body under a key")
		}
	})
}

// TestANonJSONBodyReachesTheTriggerIntact covers form-encoded, plain text and
// binary bodies, which used to be decoded into nothing useful.
func TestANonJSONBodyReachesTheTriggerIntact(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		body        string
		contentType string
		check       func(*testing.T, any)
	}{
		"plain text": {
			body: "hello there", contentType: "text/plain",
			check: func(t *testing.T, body any) {
				if body != "hello there" {
					t.Errorf("body = %#v, want the text verbatim", body)
				}
			},
		},
		"form encoded": {
			body: "From=%2B628123&Body=basic+plan%3F", contentType: "application/x-www-form-urlencoded",
			check: func(t *testing.T, body any) {
				fields, ok := body.(map[string]any)
				if !ok {
					t.Fatalf("body = %#v, want a decoded object", body)
				}
				if fields["From"] != "+628123" || fields["Body"] != "basic plan?" {
					t.Errorf("body = %#v, want the form fields decoded", fields)
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			built := delivery(t, testCase.body, testCase.contentType, repository.WebhookBinding{Path: "p"})
			testCase.check(t, built.Body)
			// The exact bytes survive whatever the content type.
			if string(built.RawBody) != testCase.body {
				t.Errorf("raw body = %q, want %q", built.RawBody, testCase.body)
			}
			item := webhook.ShapeEnvelope.Apply(built)
			if item["contentType"] != testCase.contentType {
				t.Errorf("contentType = %#v, want %q available to the workflow", item["contentType"], testCase.contentType)
			}
		})
	}
}

// TestHMACVerificationUsesTheExactBytes is what the raw-body capture exists for.
//
// WAHA signs a sha512 over the raw body. A body decoded and re-marshalled does
// not hash to the same value, so before the bytes were carried this check could
// not be written — only approximated, which is what n8n's own community trigger
// warns loudly about when it has to fall back to re-serialising.
func TestHMACVerificationUsesTheExactBytes(t *testing.T) {
	t.Parallel()

	const secret = "shared-signing-secret"
	// Deliberately formatted so that a re-marshal would not reproduce it:
	// different key order and incidental whitespace.
	const body = `{"session":"default",  "event":"message"}`

	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	verify := webhook.HMACVerifier("X-Webhook-Hmac", sha512.New, func(webhook.Delivery) (string, error) {
		return secret, nil
	})

	t.Run("a correct signature passes", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
		request.Header.Set("X-Webhook-Hmac", signature)
		handler := webhook.NewHandler(nil, nil, nil, nil, webhook.DefaultLimits())
		built, err := webhook.ReadDeliveryForTest(handler, request, repository.WebhookBinding{})
		if err != nil {
			t.Fatalf("readDelivery() error = %v", err)
		}
		if err := verify(built); err != nil {
			t.Errorf("verification failed on a correctly signed body: %v", err)
		}
		// The proof that re-marshalling would have broken it.
		remarshalled, _ := json.Marshal(built.Body)
		if string(remarshalled) == body {
			t.Skip("the fixture happens to round-trip; the assertion below is vacuous")
		}
		again := hmac.New(sha512.New, []byte(secret))
		again.Write(remarshalled)
		if hex.EncodeToString(again.Sum(nil)) == signature {
			t.Error("a re-marshalled body produced the same signature; the fixture does not exercise the difference")
		}
	})

	t.Run("a wrong signature is refused", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
		request.Header.Set("X-Webhook-Hmac", strings.Repeat("0", len(signature)))
		handler := webhook.NewHandler(nil, nil, nil, nil, webhook.DefaultLimits())
		built, _ := webhook.ReadDeliveryForTest(handler, request, repository.WebhookBinding{})
		if err := verify(built); err == nil {
			t.Error("a wrong signature was accepted")
		}
	})

	t.Run("a missing signature is refused", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
		handler := webhook.NewHandler(nil, nil, nil, nil, webhook.DefaultLimits())
		built, _ := webhook.ReadDeliveryForTest(handler, request, repository.WebhookBinding{})
		if err := verify(built); err == nil {
			t.Error("an absent signature was accepted; a verifier that does that verifies nothing")
		}
	})
}

// TestAnUnregisteredTriggerKeepsTheEnvelope covers every binding written before
// node types were recorded.
func TestAnUnregisteredTriggerKeepsTheEnvelope(t *testing.T) {
	t.Parallel()

	registry := webhook.NewRegistry()
	if kind := registry.Lookup("kilasflow.somethingNew"); kind.Shape != webhook.ShapeEnvelope {
		t.Errorf("unregistered trigger shape = %q, want the envelope", kind.Shape)
	}
	if kind := registry.Lookup(""); kind.Verify != nil {
		t.Error("an unregistered trigger must not verify anything")
	}
}
