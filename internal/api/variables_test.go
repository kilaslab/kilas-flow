package api_test

import (
	"net/http"
	"testing"
)

// Variables are a tenant's own: created, listed, renamed and deleted under its
// key, invisible to and untouchable by another tenant, with a duplicate key a
// 409 and a key `$vars` cannot read a 422.
func TestVariablesAPI(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	acme, globex := keys["acme"], keys["globex"]

	created := server.callJSON(t, http.MethodPost, "/api/v1/variables", acme,
		map[string]any{"key": "API_BASE", "value": "https://acme.example"}, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" || created["key"] != "API_BASE" || created["value"] != "https://acme.example" {
		t.Fatalf("created = %v", created)
	}

	server.callJSON(t, http.MethodPost, "/api/v1/variables", acme,
		map[string]any{"key": "API_BASE", "value": "again"}, http.StatusConflict)
	server.callJSON(t, http.MethodPost, "/api/v1/variables", acme,
		map[string]any{"key": "API-BASE", "value": "x"}, http.StatusUnprocessableEntity)

	if listed := server.callArray(t, "/api/v1/variables", acme); len(listed) != 1 {
		t.Fatalf("acme lists %v, want its one variable", listed)
	}
	if listed := server.callArray(t, "/api/v1/variables", globex); len(listed) != 0 {
		t.Fatalf("globex lists %v, want none of acme's", listed)
	}

	server.callJSON(t, http.MethodPut, "/api/v1/variables/"+id, globex,
		map[string]any{"key": "API_BASE", "value": "stolen"}, http.StatusNotFound)
	if recorder := server.call(t, http.MethodDelete, "/api/v1/variables/"+id, globex, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("globex delete answered %d, want 404", recorder.Code)
	}

	updated := server.callJSON(t, http.MethodPut, "/api/v1/variables/"+id, acme,
		map[string]any{"key": "API_URL", "value": "https://acme.example/v2"}, http.StatusOK)
	if updated["key"] != "API_URL" || updated["value"] != "https://acme.example/v2" {
		t.Fatalf("updated = %v", updated)
	}
	if recorder := server.call(t, http.MethodDelete, "/api/v1/variables/"+id, acme, nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete answered %d, want 204", recorder.Code)
	}
	if listed := server.callArray(t, "/api/v1/variables", acme); len(listed) != 0 {
		t.Fatalf("after delete acme lists %v, want none", listed)
	}
}
