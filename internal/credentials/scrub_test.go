package credentials_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// The values scrubbed out of a node's error are the type's declared secrets,
// in the forms a request writes them: a header name or a base URL is ordinary
// diagnostic text and stays, while the query-escaped form of a secret is
// exactly what a URL-borne secret looks like inside an error.
func TestSecretValuesAreTheDeclaredSecretsInEveryFormARequestWrites(t *testing.T) {
	t.Parallel()

	values := credentials.SecretValues("httpQueryAuth", map[string]string{"name": "api_key", "value": "a+b/c d"})
	for _, want := range []string{"a+b/c d", "a%2Bb%2Fc+d", "a+b%2Fc%20d"} {
		if !slices.Contains(values, want) {
			t.Errorf("SecretValues() = %q, want %q among them", values, want)
		}
	}
	if slices.Contains(values, "api_key") {
		t.Errorf("SecretValues() = %q, want the parameter name left alone", values)
	}

	// A custom-auth template is one secret field holding JSON, and the values
	// it sends are the strings inside it.
	custom := credentials.SecretValues("httpCustomAuth", map[string]string{
		"json": `{"headers":{"X-Custom-Token":"custom-secret-1"},"qs":{"sig":"custom-secret-2"}}`,
	})
	for _, want := range []string{"custom-secret-1", "custom-secret-2"} {
		if !slices.Contains(custom, want) {
			t.Errorf("SecretValues(custom) = %q, want %q among them", custom, want)
		}
	}

	// A type this server does not know has no declaration to trust, so every
	// field counts; a value too short to be worth replacing does not.
	unknown := credentials.SecretValues("pack.unknownType", map[string]string{"token": "unknown-secret", "pin": "12"})
	if !slices.Contains(unknown, "unknown-secret") || slices.Contains(unknown, "12") {
		t.Errorf("SecretValues(unknown) = %q, want every field but the too-short one", unknown)
	}

	// Longest first, so a secret containing another is replaced whole.
	for index := 1; index < len(values); index++ {
		if len(values[index]) > len(values[index-1]) {
			t.Fatalf("SecretValues() = %q, want longest first", values)
		}
	}
}

func TestScrubTextReplacesEachKnownValue(t *testing.T) {
	t.Parallel()

	got := credentials.ScrubText(`Get "https://api.test/x?key=tok-live-123": refused`, []string{"", "tok-live-123"})
	if strings.Contains(got, "tok-live-123") || !strings.Contains(got, "[redacted]") || !strings.Contains(got, "refused") {
		t.Errorf("ScrubText() = %q, want the value replaced and the rest kept", got)
	}
}
