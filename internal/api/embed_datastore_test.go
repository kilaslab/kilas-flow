package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/embed"
)

// newDatastoreEmbedAPI builds one server with the row store and an embed
// issuer, so a datastore embed test exercises the same handlers, middleware,
// and tenant resolution the dashboard uses rather than a parallel stack.
func newDatastoreEmbedAPI(t *testing.T, tenant string, issuer *embed.Issuer) http.Handler {
	t.Helper()
	db, engine := sharedDatastoreDB(t)
	return newTestServer(t, api.Deps{
		DB: db, Datastores: engine, Tenants: fixedTenant{id: tenant}, EmbedIssuer: issuer,
	})
}

func datastoreToken(t *testing.T, issuer *embed.Issuer, datastoreID string, scopes ...embed.Scope) string {
	t.Helper()
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "tenant-a", DatastoreID: datastoreID,
		Scopes: scopes, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return token
}

func TestDatastoreSessionReadsItsOwnRows(t *testing.T) {
	issuer := embedIssuer(t)
	handler := newDatastoreEmbedAPI(t, "tenant-a", issuer)
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"})
	insertRow(t, handler, created.ID, map[string]any{"email": "ada@example.com"})

	token := datastoreToken(t, issuer, created.ID, embed.ScopeDatastoreRead)

	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/datastores/"+created.ID, nil); got.Code != http.StatusOK {
		t.Fatalf("definition status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	recorder := embedRequest(t, handler, token, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows?limit=100", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("rows status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	var rows struct {
		Items []rowResource `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	if len(rows.Items) != 1 || rows.Items[0]["email"] != "ada@example.com" {
		t.Fatalf("rows = %+v, want the one inserted row", rows.Items)
	}
}

func TestDatastoreSessionReadsNoOtherDatastore(t *testing.T) {
	issuer := embedIssuer(t)
	handler := newDatastoreEmbedAPI(t, "tenant-a", issuer)
	mine := createDatastoreWithColumns(t, handler, "Mine", [2]string{"email", "string"})
	theirs := createDatastoreWithColumns(t, handler, "Theirs", [2]string{"email", "string"})
	insertRow(t, handler, theirs.ID, map[string]any{"email": "grace@example.com"})

	token := datastoreToken(t, issuer, mine.ID, embed.ScopeDatastoreRead)

	// 404 rather than 403: the session must read another datastore as
	// unknown, never learning that it exists.
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/datastores/" + theirs.ID},
		{http.MethodGet, "/api/v1/datastores/" + theirs.ID + "/rows?limit=100"},
		{http.MethodGet, "/api/v1/datastores/" + theirs.ID + "/rows/export"},
	} {
		if got := embedRequest(t, handler, token, route[0], route[1], nil); got.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404 (body: %s)", route[0], route[1], got.Code, got.Body)
		}
	}
	// And the tenant-wide surface stays out of reach either way.
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/datastores"},
		{http.MethodPost, "/api/v1/datastores"},
	} {
		if got := embedRequest(t, handler, token, route[0], route[1], map[string]any{"name": "x"}); got.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403 (body: %s)", route[0], route[1], got.Code, got.Body)
		}
	}
}

func TestDatastoreWriteScopeIsEnforcedPerRoute(t *testing.T) {
	issuer := embedIssuer(t)
	handler := newDatastoreEmbedAPI(t, "tenant-a", issuer)
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"})

	read := datastoreToken(t, issuer, created.ID, embed.ScopeDatastoreRead)
	if got := embedRequest(t, handler, read, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"email": "ada@example.com"}}); got.Code != http.StatusForbidden {
		t.Errorf("read-scope insert status = %d, want 403 (body: %s)", got.Code, got.Body)
	}

	write := datastoreToken(t, issuer, created.ID, embed.ScopeDatastoreWrite)
	if got := embedRequest(t, handler, write, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"email": "ada@example.com"}}); got.Code != http.StatusCreated {
		t.Fatalf("write-scope insert status = %d, want 201 (body: %s)", got.Code, got.Body)
	}
	// Write implies read through the whole stack, not only in Allows.
	if got := embedRequest(t, handler, write, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows?limit=100", nil); got.Code != http.StatusOK {
		t.Errorf("write-scope read status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	// Schema work stays refused on either scope.
	if got := embedRequest(t, handler, write, http.MethodDelete, "/api/v1/datastores/"+created.ID, nil); got.Code != http.StatusForbidden {
		t.Errorf("write-scope drop status = %d, want 403 (body: %s)", got.Code, got.Body)
	}
}

func TestCrossTenantDatastoreIsRefusedThroughEmbed(t *testing.T) {
	issuer := embedIssuer(t)
	db, engine := sharedDatastoreDB(t)
	serverA := newTestServer(t, api.Deps{DB: db, Datastores: engine, Tenants: fixedTenant{id: "tenant-a"}, EmbedIssuer: issuer})
	serverB := newTestServer(t, api.Deps{DB: db, Datastores: engine, Tenants: fixedTenant{id: "tenant-b"}, EmbedIssuer: issuer})

	foreign := createDatastoreWithColumns(t, serverB, "Foreign", [2]string{"email", "string"})
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "tenant-a", DatastoreID: foreign.ID,
		Scopes: []embed.Scope{embed.ScopeDatastoreRead, embed.ScopeDatastoreWrite}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// The session names the victim's id exactly, yet the engine clauses the
	// catalogue read on the request's tenant: unknown id, 404, no leak.
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/datastores/" + foreign.ID},
		{http.MethodGet, "/api/v1/datastores/" + foreign.ID + "/rows?limit=100"},
	} {
		if got := embedRequest(t, serverA, token, route[0], route[1], nil); got.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404 (body: %s)", route[0], route[1], got.Code, got.Body)
		}
	}
}

func TestWorkflowAndDatastoreSessionsStayApart(t *testing.T) {
	issuer := embedIssuer(t)
	handler := newDatastoreEmbedAPI(t, "tenant-a", issuer)
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"})

	_, workflowToken, err := issuer.Issue(embed.Request{
		TenantID: "tenant-a", WorkflowID: "wf_embedded",
		Scopes: []embed.Scope{embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// A fully-scoped workflow session still reaches no datastore route.
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/datastores/" + created.ID},
		{http.MethodGet, "/api/v1/datastores/" + created.ID + "/rows?limit=100"},
		{http.MethodPost, "/api/v1/datastores/" + created.ID + "/rows"},
	} {
		if got := embedRequest(t, handler, workflowToken, route[0], route[1], map[string]any{"values": map[string]any{}}); got.Code != http.StatusForbidden {
			t.Errorf("workflow %s %s status = %d, want 403 (body: %s)", route[0], route[1], got.Code, got.Body)
		}
	}

	datastore := datastoreToken(t, issuer, created.ID, embed.ScopeDatastoreRead)
	if got := embedRequest(t, handler, datastore, http.MethodGet, "/api/v1/workflows/wf_embedded", nil); got.Code != http.StatusForbidden {
		t.Errorf("datastore session on workflow status = %d, want 403 (body: %s)", got.Code, got.Body)
	}
}

func TestMintDatastoreSessionThroughTheAPI(t *testing.T) {
	issuer := embedIssuer(t)
	handler := newDatastoreEmbedAPI(t, "tenant-a", issuer)
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"})

	minted := requestJSON[struct {
		Token       string   `json:"token"`
		EmbedURL    string   `json:"embedUrl"`
		DatastoreID string   `json:"datastoreId"`
		Scopes      []string `json:"scopes"`
		Origin      string   `json:"origin"`
	}](t, handler, http.MethodPost, "/api/v1/embed-sessions", map[string]any{
		"datastoreId": created.ID, "scopes": []string{"datastore:read"}, "origin": hostOrigin,
	}, http.StatusCreated)
	if minted.Token == "" || minted.DatastoreID != created.ID || minted.Origin != hostOrigin {
		t.Fatalf("minted = %#v, want the token bound to the datastore", minted)
	}
	// A datastore session names no workflow, so it carries no editor URL
	// rather than a fabricated /embed/ the token is refused on.
	if minted.EmbedURL != "" {
		t.Fatalf("embedUrl = %q, want empty on a datastore session", minted.EmbedURL)
	}
	session, err := issuer.Verify(minted.Token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if session.DatastoreID != created.ID || session.WorkflowID != "" {
		t.Fatalf("verified = %#v, want the datastore subject", session)
	}

	// Both subjects, an unknown datastore, and a cross-family scope are each
	// refused rather than minted into a token that reaches nowhere.
	for _, refusal := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"both subjects", map[string]any{
			"workflowId": "wf-1", "datastoreId": created.ID,
			"scopes": []string{"datastore:read"}, "origin": hostOrigin,
		}, http.StatusUnprocessableEntity},
		{"unknown datastore", map[string]any{
			"datastoreId": "datastore_missing", "scopes": []string{"datastore:read"}, "origin": hostOrigin,
		}, http.StatusNotFound},
		{"cross-family scope", map[string]any{
			"datastoreId": created.ID, "scopes": []string{"workflow:read"}, "origin": hostOrigin,
		}, http.StatusUnprocessableEntity},
		{"no subject", map[string]any{
			"scopes": []string{"datastore:read"}, "origin": hostOrigin,
		}, http.StatusUnprocessableEntity},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			if got := doJSON(t, handler, http.MethodPost, "/api/v1/embed-sessions", refusal.body); got.Code != refusal.want {
				t.Errorf("status = %d, want %d (body: %s)", got.Code, refusal.want, got.Body)
			}
		})
	}
}
