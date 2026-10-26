package webhook_test

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

	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// standardWebhooksHeaders signs a delivery the way standardwebhooks.com
// specifies: sha256 over "{unix-seconds}.{body}", base64, behind a `v1,`
// prefix — and, during a secret rotation, twice, comma-separated.
func standardWebhooksSignature(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + body))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// TestAHMACSchemeCanSpellCommonServices pins the schemes real services sign
// with, each through the one verifier that carries them:
//
//   - GitHub-style: sha256, hex, behind a `sha256=` prefix, over the raw body.
//   - Standard Webhooks: sha256, base64, behind a `v1,` prefix, over
//     `timestamp.body`, with a unix-seconds timestamp judged for staleness.
func TestAHMACSchemeCanSpellCommonServices(t *testing.T) {
	t.Parallel()

	const secret = "whsec_shared"
	body := `{"type":"message.created","data":{"id":"m-1"}}`

	t.Run("a prefixed hex sha256 over the body", func(t *testing.T) {
		t.Parallel()

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		scheme := webhook.HMACScheme{Header: "X-Hub-Signature-256", Algorithm: "sha256", Encoding: "hex", Prefix: "sha256="}
		verify := webhook.HMACSchemeVerifier(scheme, fixedSecret(secret))

		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
		request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		if err := verify(webhook.Delivery{Request: request, RawBody: []byte(body)}); err != nil {
			t.Fatalf("a GitHub-style signature was refused: %v", err)
		}

		// Uppercase hex is the same digest; the hex comparison is
		// case-insensitive, the way it always was for WAHA's.
		request.Header.Set("X-Hub-Signature-256", "sha256="+strings.ToUpper(hex.EncodeToString(mac.Sum(nil))))
		if err := verify(webhook.Delivery{Request: request, RawBody: []byte(body)}); err != nil {
			t.Fatalf("an uppercase hex signature was refused: %v", err)
		}
	})

	scheme := webhook.HMACScheme{
		Header: "webhook-signature", Algorithm: "sha256", Encoding: "base64", Prefix: "v1,",
		SigningString: "{{ .timestamp }}.{{ .body }}", TimestampHeader: "webhook-timestamp", Tolerance: 5 * time.Minute,
	}
	verify := webhook.HMACSchemeVerifier(scheme, fixedSecret(secret))
	delivery := func(t *testing.T, timestamp, signature string) webhook.Delivery {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
		if timestamp != "" {
			request.Header.Set("webhook-timestamp", timestamp)
		}
		if signature != "" {
			request.Header.Set("webhook-signature", signature)
		}
		return webhook.Delivery{Request: request, RawBody: []byte(body)}
	}

	t.Run("a fresh standard-webhooks delivery", func(t *testing.T) {
		t.Parallel()

		now := strconv.FormatInt(time.Now().Unix(), 10)
		if err := verify(delivery(t, now, standardWebhooksSignature(secret, now, body))); err != nil {
			t.Fatalf("a correct standard-webhooks signature was refused: %v", err)
		}
	})

	t.Run("a replay outside the tolerance", func(t *testing.T) {
		t.Parallel()

		stale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
		err := verify(delivery(t, stale, standardWebhooksSignature(secret, stale, body)))
		if err == nil || !strings.Contains(err.Error(), "tolerance") {
			t.Fatalf("a stale delivery was not refused as a replay: %v", err)
		}
	})

	t.Run("a timestamp that cannot be read", func(t *testing.T) {
		t.Parallel()

		err := verify(delivery(t, "last tuesday", standardWebhooksSignature(secret, "last tuesday", body)))
		if err == nil || !strings.Contains(err.Error(), "timestamp") {
			t.Fatalf("an unreadable timestamp was not refused: %v", err)
		}
	})

	t.Run("a missing timestamp", func(t *testing.T) {
		t.Parallel()

		err := verify(delivery(t, "", "v1,AAAA"))
		if err == nil || !strings.Contains(err.Error(), "webhook-timestamp") {
			t.Fatalf("a delivery without the timestamp header was not refused: %v", err)
		}
	})

	t.Run("rotation: two signatures, either accepted", func(t *testing.T) {
		t.Parallel()

		now := strconv.FormatInt(time.Now().Unix(), 10)
		oldSecret := standardWebhooksSignature("whsec_retired", now, body)
		current := standardWebhooksSignature(secret, now, body)
		// The order is the sender's; either half matching is the pass.
		for _, header := range []string{oldSecret + "," + current, current + "," + oldSecret} {
			if err := verify(delivery(t, now, header)); err != nil {
				t.Fatalf("a rotating sender's signature pair was refused: %v", err)
			}
		}
		if err := verify(delivery(t, now, oldSecret+","+standardWebhooksSignature("whsec_other", now, body))); err == nil {
			t.Fatal("a pair neither half of which this endpoint knows was accepted")
		}
	})

	t.Run("a wrong secret, a wrong digest, a truncated prefix", func(t *testing.T) {
		t.Parallel()

		now := strconv.FormatInt(time.Now().Unix(), 10)
		correct := standardWebhooksSignature(secret, now, body)
		for name, header := range map[string]string{
			"wrong secret": standardWebhooksSignature("whsec_somebody-else", now, body),
			"wrong digest": "v1," + base64.StdEncoding.EncodeToString([]byte("not the digest")),
			"no prefix":    strings.TrimPrefix(correct, "v1,"),
		} {
			if err := verify(delivery(t, now, header)); err == nil {
				t.Errorf("%s was accepted", name)
			}
		}
	})

	t.Run("a signing string over a header value", func(t *testing.T) {
		t.Parallel()

		// LINE signs its channel secret over the raw body; the schemes that
		// fold another header in are why the template can name one.
		withHeader := webhook.HMACScheme{
			Header: "X-Signature", Algorithm: "sha256", Encoding: "hex",
			SigningString: "{{ .header.X-Delivery-Id }}:{{ .body }}",
		}
		headerVerify := webhook.HMACSchemeVerifier(withHeader, fixedSecret(secret))
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
		request.Header.Set("X-Delivery-Id", "delivery-7")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("delivery-7:" + body))
		request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
		if err := headerVerify(webhook.Delivery{Request: request, RawBody: []byte(body)}); err != nil {
			t.Fatalf("a header-templated signing string was refused: %v", err)
		}
		// A delivery without the header signs the empty string in its place —
		// what its sender signed — and this one signed something else.
		request.Header.Del("X-Delivery-Id")
		if err := headerVerify(webhook.Delivery{Request: request, RawBody: []byte(body)}); err == nil {
			t.Fatal("a delivery signed over a header it did not carry was accepted")
		}
	})
}

// TestCheckSigningString pins what an author hears at pack-validation time,
// so a typo in a template is an authoring error rather than a refused
// delivery.
func TestCheckSigningString(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		template        string
		timestampHeader string
		want            string
	}{
		"the raw body":             {template: "{{ .body }}"},
		"a timestamp and the body": {template: "{{ .timestamp }}.{{ .body }}", timestampHeader: "webhook-timestamp"},
		"a header":                 {template: "{{ .header.X-Id }}:{{ .body }}"},
		"literals stay":            {template: "v0:{{ .timestamp }}:{{ .body }}", timestampHeader: "t"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := webhook.CheckSigningString(testCase.template, testCase.timestampHeader); err != nil {
				t.Fatalf("a well-formed signing string was refused: %v", err)
			}
		})
	}

	for name, testCase := range map[string]struct {
		template        string
		timestampHeader string
		want            string
	}{
		"a typo":                   {template: "{{ .boday }}", want: "may name only"},
		"an unnamed header":        {template: "{{ .header. }}", want: "no name"},
		"a timestamp from nowhere": {template: "{{ .timestamp }}", want: "timestampHeader"},
		"an expression":            {template: "{{ $json.id }}", want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := webhook.CheckSigningString(testCase.template, testCase.timestampHeader)
			if err == nil {
				t.Fatalf("signing string %q was accepted", testCase.template)
			}
			if testCase.want != "" && !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// A scheme with no preset machinery is what a pack's own fields build; the
// empty SigningString is the raw body, exactly as WAHA's verifier read it.
func TestABareSchemeSignsTheBodyAlone(t *testing.T) {
	t.Parallel()

	const secret = "shared"
	body := `{"session":"default","event":"message"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	verify := webhook.HMACSchemeVerifier(webhook.HMACScheme{Header: "X-Signature", Algorithm: "sha256"}, fixedSecret(secret))

	request := httptest.NewRequest(http.MethodPost, "/webhook/abc", strings.NewReader(body))
	request.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	binding := repository.WebhookBinding{}
	if err := verify(webhook.Delivery{Request: request, RawBody: []byte(body), Binding: binding}); err != nil {
		t.Fatalf("a bare sha256 over the body was refused: %v", err)
	}
}

func fixedSecret(secret string) func(webhook.Delivery) (string, error) {
	return func(webhook.Delivery) (string, error) { return secret, nil }
}
