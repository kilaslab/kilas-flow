package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
)

// decodeCredentials reads a listing body, which is still a bare JSON array.
func decodeCredentials(t *testing.T, body []byte) []credentialResource {
	t.Helper()
	var listed []credentialResource
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode listing = %v (body: %s)", err, body)
	}
	return listed
}

// The listing endpoints used to return every row in the tenant. The cursor is
// opaque and travels in a header, so the body keeps the shape the dashboard
// already reads — which is what makes this a fix rather than an API break.
func TestCredentialsArePagedWithACursor(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})

	for _, name := range []string{"Alpha", "Bravo", "Charlie"} {
		storeCredential(t, handler, name, "httpHeaderAuth", map[string]string{
			"name": "X-Api-Key", "value": "secret-" + name,
		})
	}

	first := get(t, handler, "/api/v1/credentials?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", first.Code, first.Body)
	}
	page := decodeCredentials(t, first.Body.Bytes())
	if len(page) != 2 {
		t.Fatalf("first page = %d credentials, want 2", len(page))
	}
	cursor := first.Header().Get("X-Next-Cursor")
	if cursor == "" {
		t.Fatal("the first page carried no X-Next-Cursor, so the rest of the listing is unreachable")
	}

	// The second page resumes after the last row the first one returned, and
	// the two pages together are the whole listing with nothing repeated.
	second := get(t, handler, "/api/v1/credentials?limit=2&cursor="+cursor)
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200 (body: %s)", second.Code, second.Body)
	}
	rest := decodeCredentials(t, second.Body.Bytes())
	if len(rest) != 1 {
		t.Fatalf("second page = %d credentials, want the remaining 1", len(rest))
	}
	if second.Header().Get("X-Next-Cursor") != "" {
		t.Error("the last page still advertised a next page")
	}
	seen := map[string]bool{}
	for _, credential := range append(page, rest...) {
		if seen[credential.Name] {
			t.Errorf("credential %q appeared on both pages", credential.Name)
		}
		seen[credential.Name] = true
	}
	if len(seen) != 3 {
		t.Errorf("pages covered %d credentials, want all 3", len(seen))
	}

	// A cursor this API never issued is a bad request: paging from a position
	// that means nothing is not a server fault.
	broken := get(t, handler, "/api/v1/credentials?cursor=not-a-cursor")
	if broken.Code != http.StatusBadRequest {
		t.Errorf("status = %d for a malformed cursor, want 400 (body: %s)", broken.Code, broken.Body)
	}
}
