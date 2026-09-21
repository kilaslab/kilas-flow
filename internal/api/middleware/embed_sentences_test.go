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
		{http.MethodGet, "/api/v1/credential-types", "This embed session cannot use this endpoint."},
		{http.MethodPost, "/api/v1/credential-types/slack/test", "This embed session cannot use this endpoint."},
		{http.MethodPost, "/api/v1/credentials/cred_1/test", "This embed session cannot manage credentials."},
		{http.MethodGet, "/api/v1/workflows/wf_2", "This embed session is scoped to a different workflow."},
		{http.MethodPost, "/api/v1/workflows/wf_1/activate", "This embed session cannot change activation."},
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
