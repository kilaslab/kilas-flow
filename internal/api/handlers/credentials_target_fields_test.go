package handlers

import (
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// TestAStoredSecretIsOnlyMergedWhileTheTypesOwnTargetIsUnchanged: a pack type
// names its address fields itself, so the fields that pin a stored secret to
// its target include the type's default-scope field and every readable field
// its test URL reads, beside the fixed host/port/baseUrl/url.
func TestAStoredSecretIsOnlyMergedWhileTheTypesOwnTargetIsUnchanged(t *testing.T) {
	t.Parallel()

	got := storedTargetFields(credentials.Type{
		ID: "pack.acme.api", DefaultDomainsFrom: "apiHost",
		Secrets: []string{"apiKey"},
		Test:    &credentials.TestRequest{URL: "https://{{ apiHost }}/{{ tenant }}/me?key={{ apiKey }}&u={{ url }}"},
	})
	want := []string{"host", "port", "baseUrl", "url", "apiHost", "tenant"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("storedTargetFields() = %v, want %v", got, want)
	}
	if got := storedTargetFields(credentials.Type{}); !reflect.DeepEqual(got, targetFields) {
		t.Fatalf("storedTargetFields(unknown type) = %v, want the fixed list %v", got, targetFields)
	}
}
