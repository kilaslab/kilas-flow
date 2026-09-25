package api_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
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

// An embed session's listing used to be filtered after the store had built the
// page, and still carried the store's cursor — base64 of the last unfiltered
// row's name and id. Paging with limit=1 therefore walked the whole tenant one
// sibling at a time, and every cursor decoded to a credential the session was
// never granted. The rows and the cursors are both read here.
func TestAnEmbedSessionsCredentialCursorNamesNoOtherCredential(t *testing.T) {
	handler, _, workflowID := embedServer(t)

	siblings := []credentialResource{
		storeCredential(t, handler, "Alpha sibling", "httpHeaderAuth", map[string]string{"name": "X-A", "value": "a"}),
		storeCredential(t, handler, "Mike sibling", "httpHeaderAuth", map[string]string{"name": "X-M", "value": "m"}),
		storeCredential(t, handler, "Zulu sibling", "httpHeaderAuth", map[string]string{"name": "X-Z", "value": "z"}),
	}
	granted := storeScopedCredential(t, handler, "Granted", "httpHeaderAuth",
		map[string]string{"name": "X-G", "value": "g"}, "partner.test")
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", granted.ID))), http.StatusOK)
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil, http.StatusOK)

	token := mintEmbedSession(t, handler, workflowID, "workflow:read")

	var listed []credentialResource
	disclosed := ""
	cursor := ""
	for page := 0; page < 10; page++ {
		path := "/api/v1/credentials?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		got := embedRequest(t, handler, token, http.MethodGet, path, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("page %d status = %d, want 200 (body: %s)", page, got.Code, got.Body)
		}
		listed = append(listed, decodeCredentials(t, got.Body.Bytes())...)
		disclosed += got.Body.String()
		cursor = got.Header().Get("X-Next-Cursor")
		if cursor == "" {
			break
		}
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			t.Fatalf("cursor %q is not the store's encoding: %v", cursor, err)
		}
		disclosed += string(decoded)
	}

	if len(listed) != 1 || listed[0].ID != granted.ID {
		t.Fatalf("listed = %#v, want exactly the granted credential", listed)
	}
	for _, sibling := range siblings {
		if strings.Contains(disclosed, sibling.ID) || strings.Contains(disclosed, sibling.Name) {
			t.Errorf("the listing or its cursor disclosed %q (%s)", sibling.Name, sibling.ID)
		}
	}
}
