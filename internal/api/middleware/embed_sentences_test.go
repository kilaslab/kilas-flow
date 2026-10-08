package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// The embed arm's sentences are what a host page reads back, so sharing the
// table with the scoped-key arm must not have changed them. Two of them did
// change, both in the direction the design asks for — a named reason instead of
// the default arm's "cannot use this endpoint" — and they are pinned here so
// the next refactor cannot quietly move them again.
func TestEmbedSentencesAreUnchangedByTheSharedTable(t *testing.T) {
	t.Parallel()
	session := workflowSession(embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun)
	for _, route := range []struct{ method, path, want string }{
		{http.MethodGet, "/api/v1/credentials/cred_1", "This embed session cannot use this endpoint."},
		{http.MethodPost, "/api/v1/credential-types/slack/test", "This embed session cannot use this endpoint."},
		{http.MethodPost, "/api/v1/credentials/cred_1/test", "This embed session cannot manage credentials."},
		{http.MethodPost, "/api/v1/credentials/cred_1/oauth/start", "This embed session cannot manage credentials."},
		{http.MethodGet, "/api/v1/workflows/wf_2", "This embed session is scoped to a different workflow."},
		{http.MethodPost, "/api/v1/workflows/wf_1/activate", "This embed session cannot change activation."},
		{http.MethodPost, "/api/v1/workflows/wf_1/deactivate", "This embed session cannot change activation."},
		// Publishing was a write until it had a scope of its own; the sentence
		// names that scope, so a host reads what it would have to grant.
		{http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish", "This embed session cannot publish a revision without the workflow:publish scope."},
		{http.MethodGet, "/api/v1/datastores", "This embed session cannot manage datastores."},
		{http.MethodGet, "/api/v1/schedules", "This embed session cannot manage schedules."},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		status, detail := permits(sessionSubject{session: session}, request)
		if status != http.StatusForbidden || detail != route.want {
			t.Errorf("%s %s = %d %q, want 403 %q", route.method, route.path, status, detail, route.want)
		}
	}
}

// The editor renders a node's credential field from the type catalogue on every
// load, and an embed session was refused the whole prefix by kind, so every
// embedded editor logged a 403 for a read its own scope covers. The catalogue
// is schemas, never values; the validator under the same prefix spends a live
// call and stays refused.
func TestAnEmbedSessionReadsTheCredentialTypeCatalogue(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/v1/credential-types", "/api/v1/credential-types/slack"} {
		reader := httptest.NewRequest(http.MethodGet, path, nil)
		if status, detail := permits(sessionSubject{session: workflowSession(embed.ScopeRead)}, reader); status != 0 {
			t.Errorf("GET %s with %s = %d %q, want it permitted", path, embed.ScopeRead, status, detail)
		}
		outsider := httptest.NewRequest(http.MethodGet, path, nil)
		if status, _ := permits(sessionSubject{session: workflowSession(embed.ScopeDatastoreRead)}, outsider); status != http.StatusForbidden {
			t.Errorf("GET %s without %s = %d, want 403", path, embed.ScopeRead, status)
		}
	}
}
