package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// The three levels a host grants an embedded editor, and nothing in between:
// write saves drafts, publish pins a revision, activate turns the workflow on
// and off. Each level is its own scope, so a host that mints the editor's
// everyday scopes has not, without saying so, handed its customers the switch
// production traffic runs on.
func TestAnEmbedSessionPublishesAndActivatesOnlyWithItsOwnScope(t *testing.T) {
	t.Parallel()

	editor := workflowSession(embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun)
	publisher := workflowSession(embed.ScopePublish)
	activator := workflowSession(embed.ScopeActivate)
	everything := workflowSession(embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun, embed.ScopePublish, embed.ScopeActivate)

	for _, route := range []struct {
		name    string
		method  string
		path    string
		session embed.Session
		status  int
		detail  string
	}{
		// Publishing a revision needs workflow:publish; write alone is refused
		// with a sentence that names the scope the host would have to grant.
		{"write cannot publish", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish", editor, http.StatusForbidden,
			"This embed session cannot publish a revision without the workflow:publish scope."},
		{"activate alone cannot publish", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish", activator, http.StatusForbidden,
			"This embed session cannot publish a revision without the workflow:publish scope."},
		{"publish publishes", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish", publisher, 0, ""},
		{"publish still reads the workflow", http.MethodGet, "/api/v1/workflows/wf_1", publisher, 0, ""},
		{"publish reads the publish history", http.MethodGet, "/api/v1/workflows/wf_1/publish-events", publisher, 0, ""},
		{"publish cannot save a draft", http.MethodPut, "/api/v1/workflows/wf_1", publisher, http.StatusForbidden, "This embed session is read-only."},
		{"publish cannot restore", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/restore", publisher, http.StatusForbidden, "This embed session is read-only."},
		{"publish cannot activate", http.MethodPost, "/api/v1/workflows/wf_1/activate", publisher, http.StatusForbidden, "This embed session cannot change activation."},
		{"publish cannot deactivate", http.MethodPost, "/api/v1/workflows/wf_1/deactivate", publisher, http.StatusForbidden, "This embed session cannot change activation."},
		{"publish is still bound to its workflow", http.MethodPost, "/api/v1/workflows/wf_2/versions/ver_1/publish", publisher, http.StatusForbidden,
			"This embed session is scoped to a different workflow."},

		// Activation needs workflow:activate; without it the refusal is the one
		// every session has always read.
		{"write cannot activate", http.MethodPost, "/api/v1/workflows/wf_1/activate", editor, http.StatusForbidden, "This embed session cannot change activation."},
		{"write cannot deactivate", http.MethodPost, "/api/v1/workflows/wf_1/deactivate", editor, http.StatusForbidden, "This embed session cannot change activation."},
		{"activate activates", http.MethodPost, "/api/v1/workflows/wf_1/activate", activator, 0, ""},
		{"activate deactivates", http.MethodPost, "/api/v1/workflows/wf_1/deactivate", activator, 0, ""},
		{"activate still reads the workflow", http.MethodGet, "/api/v1/workflows/wf_1", activator, 0, ""},
		{"activate cannot save a draft", http.MethodPut, "/api/v1/workflows/wf_1", activator, http.StatusForbidden, "This embed session is read-only."},
		{"activate is still bound to its workflow", http.MethodPost, "/api/v1/workflows/wf_2/activate", activator, http.StatusForbidden,
			"This embed session is scoped to a different workflow."},

		// Every scope at once is still one workflow, and still no delete.
		{"every scope publishes", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish", everything, 0, ""},
		{"every scope activates", http.MethodPost, "/api/v1/workflows/wf_1/activate", everything, 0, ""},
		{"every scope still cannot delete", http.MethodDelete, "/api/v1/workflows/wf_1", everything, http.StatusForbidden, "This embed session cannot delete a workflow."},
		{"every scope still cannot import", http.MethodPost, "/api/v1/workflows/import", everything, http.StatusForbidden, "This embed session cannot import workflows."},
	} {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(route.method, route.path, nil)
			status, detail := permits(sessionSubject{session: route.session}, request)
			if status != route.status || detail != route.detail {
				t.Errorf("%s %s = %d %q, want %d %q", route.method, route.path, status, detail, route.status, route.detail)
			}
		})
	}
}

// A scoped key keeps the answer it had: neither embed-only scope opens
// publishing or activation to an agent token, even were one to reach the gate.
func TestAScopedKeyIsRefusedPublishAndActivationWhateverItHolds(t *testing.T) {
	t.Parallel()

	holdsEverything := agentKey("", embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun, embed.ScopePublish, embed.ScopeActivate)
	checkScopeRoutes(t, []scopeRoute{
		{"cannot publish", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish", holdsEverything, http.StatusForbidden, "This agent token cannot change activation."},
		{"cannot activate", http.MethodPost, "/api/v1/workflows/wf_1/activate", holdsEverything, http.StatusForbidden, "This agent token cannot change activation."},
		{"cannot deactivate", http.MethodPost, "/api/v1/workflows/wf_1/deactivate", holdsEverything, http.StatusForbidden, "This agent token cannot change activation."},
	})
}
