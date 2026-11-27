package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// EmbedPermit is the one decision EmbedAuth enforces, exported so the docs
// generator can print the same verdicts instead of restating them. The pin
// that matters is that the exported answer and the enforced answer are the
// same: a status and a sentence here must be exactly what a host page reads
// back over HTTP, and an allowed request must reach the handler.
func TestEmbedPermitIsTheDecisionEmbedAuthEnforces(t *testing.T) {
	t.Parallel()

	for _, route := range []struct {
		name    string
		session embed.Session
		method  string
		target  string
	}{
		{"own workflow read", workflowSession(embed.ScopeRead), http.MethodGet, "/api/v1/workflows/wf_1"},
		{"another workflow", workflowSession(embed.ScopeRead), http.MethodGet, "/api/v1/workflows/wf_2"},
		{"save without the write scope", workflowSession(embed.ScopeRead), http.MethodPut, "/api/v1/workflows/wf_1"},
		{"publish needs its own scope", workflowSession(embed.ScopeWrite), http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish"},
		{"listing workflows", workflowSession(embed.ScopeRead, embed.ScopeWrite), http.MethodGet, "/api/v1/workflows"},
		{"own execution list", workflowSession(embed.ScopeRead), http.MethodGet, "/api/v1/executions?workflowId=wf_1"},
		{"another workflow's execution list", workflowSession(embed.ScopeRead), http.MethodGet, "/api/v1/executions?workflowId=wf_2"},
		{"datastore rows read", datastoreSession(embed.ScopeDatastoreRead), http.MethodGet, "/api/v1/datastores/datastore_1/rows"},
		{"datastore rows write with a read scope", datastoreSession(embed.ScopeDatastoreRead), http.MethodPost, "/api/v1/datastores/datastore_1/rows"},
		{"datastore rows write", datastoreSession(embed.ScopeDatastoreWrite), http.MethodPost, "/api/v1/datastores/datastore_1/rows"},
		{"datastore schema", datastoreSession(embed.ScopeDatastoreWrite), http.MethodDelete, "/api/v1/datastores/datastore_1"},
		{"who am I", workflowSession(embed.ScopeRead), http.MethodGet, "/api/v1/auth/me"},
		{"variables", workflowSession(embed.ScopeRead, embed.ScopeWrite), http.MethodGet, "/api/v1/variables"},
	} {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(route.method, route.target, nil)
			request.Header.Set("X-KilasFlow-Embed", "kfe1.token")
			request.Header.Set("Origin", "https://host.example")

			status, detail := EmbedPermit(route.session, request)

			reached := false
			handler := EmbedAuth(fixedVerifier{session: route.session}, "")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if status == 0 {
				if !reached || recorder.Code != http.StatusOK {
					t.Fatalf("EmbedPermit allowed %s %s but EmbedAuth answered %d (reached handler: %t)",
						route.method, route.target, recorder.Code, reached)
				}
				if detail != "" {
					t.Errorf("an allowed request carries detail %q, want none", detail)
				}
				return
			}
			if reached {
				t.Fatalf("EmbedPermit refused %s %s (%d %q) but EmbedAuth let it through", route.method, route.target, status, detail)
			}
			if recorder.Code != status {
				t.Errorf("EmbedAuth answered %d, EmbedPermit said %d", recorder.Code, status)
			}
			var problem struct {
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem body %q: %v", recorder.Body.String(), err)
			}
			if problem.Detail != detail {
				t.Errorf("EmbedAuth said %q, EmbedPermit said %q", problem.Detail, detail)
			}
		})
	}
}
