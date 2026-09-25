package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/embed"
)

// agentKey is a scoped key with the given scopes, bound to one workflow when
// bound is not empty.
func agentKey(bound string, scopes ...embed.Scope) keySubject {
	return keySubject{principal: auth.Principal{
		TenantID: "tenant-a", KeyID: "key_agent", Kind: auth.KindAPIKey,
		Label: "agent", Scopes: scopes, WorkflowID: bound,
	}}
}

// scopeRoute is one request and the answer the gate owes it: the status a
// refusal carries, or zero when the request is inside the key's authority.
type scopeRoute struct {
	name   string
	method string
	path   string
	key    keySubject
	status int
	// reason is a fragment the refusal must name, so a caller reads which
	// authority refused rather than a generic message.
	reason string
}

func checkScopeRoutes(t *testing.T, routes []scopeRoute) {
	t.Helper()
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(route.method, route.path, nil)
			status, detail := permits(route.key, request)
			if status != route.status {
				t.Fatalf("%s %s status = %d (%q), want %d", route.method, route.path, status, detail, route.status)
			}
			if route.status != 0 && !strings.Contains(detail, route.reason) {
				t.Errorf("%s %s refusal = %q, want it to name %q", route.method, route.path, detail, route.reason)
			}
		})
	}
}

// The refusals of design §3.2, one arm at the EmbedAuth position, each with a
// named reason. Every one of these is refused *before* the handler runs, so a
// route added later is refused by the default arm rather than by an author
// remembering to add a check.
func TestAScopedKeyIsRefusedTheOperationsOfSectionThreeTwo(t *testing.T) {
	t.Parallel()

	full := agentKey("", embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun,
		embed.ScopeDatastoreRead, embed.ScopeDatastoreWrite)

	checkScopeRoutes(t, []scopeRoute{
		// Activation publishes a public endpoint, and publishing a revision is
		// activation by another name.
		{"cannot activate", http.MethodPost, "/api/v1/workflows/wf_1/activate", full, http.StatusForbidden, "cannot change activation"},
		{"cannot deactivate", http.MethodPost, "/api/v1/workflows/wf_1/deactivate", full, http.StatusForbidden, "cannot change activation"},
		{"cannot publish a revision", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/publish", full, http.StatusForbidden, "cannot change activation"},
		// Deleting and importing are outside a key's list even with every
		// scope: both are how a workflow stops existing or starts existing.
		{"cannot delete", http.MethodDelete, "/api/v1/workflows/wf_1", full, http.StatusForbidden, "cannot delete a workflow"},
		{"cannot import", http.MethodPost, "/api/v1/workflows/import", full, http.StatusForbidden, "cannot import workflows"},
		// The operator surface is not an agent's.
		{"cannot list tenants", http.MethodGet, "/api/v1/tenants", full, http.StatusForbidden, "cannot use this endpoint"},
		{"cannot read a tenant", http.MethodGet, "/api/v1/tenants/tenant-a", full, http.StatusForbidden, "cannot use this endpoint"},
		{"cannot create a tenant", http.MethodPost, "/api/v1/tenants", full, http.StatusForbidden, "cannot use this endpoint"},
		// Key management: a scoped key can never mint or revoke a key, which
		// is how it would escalate past its own list.
		{"cannot mint a key", http.MethodPost, "/api/v1/api-keys", full, http.StatusForbidden, "cannot use this endpoint"},
		{"cannot revoke a key", http.MethodDelete, "/api/v1/api-keys/key_1", full, http.StatusForbidden, "cannot use this endpoint"},
		// A browser session is a different authority than acting.
		{"cannot mint an embed session", http.MethodPost, "/api/v1/embed-sessions", full, http.StatusForbidden, "cannot use this endpoint"},
		// Credential mutation, including the test that spends a stored secret.
		{"cannot create a credential", http.MethodPost, "/api/v1/credentials", full, http.StatusForbidden, "cannot manage credentials"},
		{"cannot update a credential", http.MethodPut, "/api/v1/credentials/cred_1", full, http.StatusForbidden, "cannot manage credentials"},
		{"cannot delete a credential", http.MethodDelete, "/api/v1/credentials/cred_1", full, http.StatusForbidden, "cannot manage credentials"},
		{"cannot test a credential", http.MethodPost, "/api/v1/credentials/cred_1/test", full, http.StatusForbidden, "cannot manage credentials"},
		{"cannot start google oauth", http.MethodPost, "/api/v1/credentials/cred_1/oauth/start", full, http.StatusForbidden, "cannot manage credentials"},
		{"cannot validate a credential payload", http.MethodPost, "/api/v1/credential-types/slack/test", full, http.StatusForbidden, "cannot manage credentials"},
		// Datastore schema work: rows are data, columns and tables are shape.
		{"cannot create a datastore", http.MethodPost, "/api/v1/datastores", full, http.StatusForbidden, "cannot manage datastores"},
		{"cannot rename a datastore", http.MethodPut, "/api/v1/datastores/ds_1", full, http.StatusForbidden, "cannot manage datastores"},
		{"cannot drop a datastore", http.MethodDelete, "/api/v1/datastores/ds_1", full, http.StatusForbidden, "cannot manage datastores"},
		{"cannot clear a datastore", http.MethodPost, "/api/v1/datastores/ds_1/clear", full, http.StatusForbidden, "cannot manage datastores"},
		{"cannot add a column", http.MethodPost, "/api/v1/datastores/ds_1/columns", full, http.StatusForbidden, "cannot manage datastores"},
		{"cannot rename a column", http.MethodPut, "/api/v1/datastores/ds_1/columns/email", full, http.StatusForbidden, "cannot manage datastores"},
		{"cannot drop a column", http.MethodDelete, "/api/v1/datastores/ds_1/columns/email", full, http.StatusForbidden, "cannot manage datastores"},
		// Approvals are answered by a person.
		{"cannot resume an approval", http.MethodPost, "/api/v1/resume/tok_1", full, http.StatusForbidden, "cannot answer an approval"},
		// And the default arm still refuses what nothing names: a pack
		// install has no HTTP route today, so what is asserted is that the
		// surface stays closed to a key.
		{"refused by the default arm", http.MethodPost, "/api/v1/packs", full, http.StatusForbidden, "cannot use this endpoint"},
		{"refused by the default arm on an unknown route", http.MethodGet, "/api/v1/something-new", full, http.StatusForbidden, "cannot use this endpoint"},
	})
}

// What the scope list buys, arm by arm. The refusals above are the point of the
// ticket; these are the reads and writes an agent is *for*, so a gate that
// refused them would make the token useless.
func TestAScopedKeyReachesWhatItsScopesName(t *testing.T) {
	t.Parallel()

	reader := agentKey("", embed.ScopeRead)
	writer := agentKey("", embed.ScopeWrite)
	runner := agentKey("", embed.ScopeRun)
	datastoreReader := agentKey("", embed.ScopeDatastoreRead)
	datastoreWriter := agentKey("", embed.ScopeDatastoreWrite)

	checkScopeRoutes(t, []scopeRoute{
		// Self-description is how the CLI learns its own scopes before a
		// guarded verb.
		{"describes itself", http.MethodGet, "/api/v1/auth/me", reader, 0, ""},
		{"lists workflows", http.MethodGet, "/api/v1/workflows", reader, 0, ""},
		{"reads a workflow", http.MethodGet, "/api/v1/workflows/wf_1", reader, 0, ""},
		// The dry run is a read of the catalogue: it compiles a document the
		// caller supplies and saves nothing, so the verb that answers "would
		// this run" must not need a write scope.
		{"validates a document", http.MethodPost, "/api/v1/workflows/validate", reader, 0, ""},
		// Converting pasted n8n JSON saves nothing either (BUG-txafja).
		{"converts pasted n8n nodes", http.MethodPost, "/api/v1/workflows/convert", reader, 0, ""},
		{"reads a revision", http.MethodGet, "/api/v1/workflows/wf_1/versions/ver_1", reader, 0, ""},
		{"reads publish events", http.MethodGet, "/api/v1/workflows/wf_1/publish-events", reader, 0, ""},
		{"exports a workflow", http.MethodGet, "/api/v1/workflows/wf_1/export", reader, 0, ""},
		{"reads the node catalogue", http.MethodGet, "/api/v1/node-types", reader, 0, ""},
		{"reads credential names", http.MethodGet, "/api/v1/credentials", reader, 0, ""},
		{"reads one credential's public half", http.MethodGet, "/api/v1/credentials/cred_1", reader, 0, ""},
		{"reads credential types", http.MethodGet, "/api/v1/credential-types", reader, 0, ""},
		{"reads one credential type", http.MethodGet, "/api/v1/credential-types/slack", reader, 0, ""},
		{"lists executions", http.MethodGet, "/api/v1/executions", reader, 0, ""},
		{"reads an execution", http.MethodGet, "/api/v1/executions/ex_1", reader, 0, ""},
		{"streams an execution", http.MethodGet, "/api/v1/executions/ex_1/events", reader, 0, ""},
		// Evaluating an expression re-reads what the trace already returned to
		// this caller, so it is a read. Retrying it is work, and is asserted
		// below to need the run scope.
		{"evaluates against an execution", http.MethodPost, "/api/v1/executions/ex_1/eval", reader, 0, ""},
		{"retries an execution", http.MethodPost, "/api/v1/executions/ex_1/retry", runner, 0, ""},
		{"creates a workflow", http.MethodPost, "/api/v1/workflows", writer, 0, ""},
		{"updates a workflow", http.MethodPut, "/api/v1/workflows/wf_1", writer, 0, ""},
		// A copy is a create, so it needs the scope a create needs — and only
		// that: unlike import it copies a workflow the key can already read.
		{"duplicates a workflow", http.MethodPost, "/api/v1/workflows/wf_1/duplicate", writer, 0, ""},
		{"restores a revision", http.MethodPost, "/api/v1/workflows/wf_1/versions/ver_1/restore", writer, 0, ""},
		{"lists schedules", http.MethodGet, "/api/v1/schedules", reader, 0, ""},
		{"creates a schedule", http.MethodPost, "/api/v1/schedules", writer, 0, ""},
		{"runs a workflow", http.MethodPost, "/api/v1/workflows/wf_1/run", runner, 0, ""},
		{"lists datastores", http.MethodGet, "/api/v1/datastores", datastoreReader, 0, ""},
		{"reads a datastore", http.MethodGet, "/api/v1/datastores/ds_1", datastoreReader, 0, ""},
		{"reads rows", http.MethodGet, "/api/v1/datastores/ds_1/rows", datastoreReader, 0, ""},
		{"exports rows", http.MethodGet, "/api/v1/datastores/ds_1/rows/export", datastoreReader, 0, ""},
		{"inserts a row", http.MethodPost, "/api/v1/datastores/ds_1/rows", datastoreWriter, 0, ""},
		{"updates rows", http.MethodPut, "/api/v1/datastores/ds_1/rows", datastoreWriter, 0, ""},
		{"deletes rows", http.MethodDelete, "/api/v1/datastores/ds_1/rows", datastoreWriter, 0, ""},
		{"upserts a row", http.MethodPost, "/api/v1/datastores/ds_1/rows/upsert", datastoreWriter, 0, ""},
		{"imports rows", http.MethodPost, "/api/v1/datastores/ds_1/rows/import", datastoreWriter, 0, ""},

		// A read scope is not a write scope, and the two families stay apart.
		{"read cannot update a workflow", http.MethodPut, "/api/v1/workflows/wf_1", reader, http.StatusForbidden, "is read-only"},
		{"read cannot create a workflow", http.MethodPost, "/api/v1/workflows", reader, http.StatusForbidden, "is read-only"},
		{"read cannot run", http.MethodPost, "/api/v1/workflows/wf_1/run", reader, http.StatusForbidden, "cannot run workflows"},
		{"write cannot run", http.MethodPost, "/api/v1/workflows/wf_1/run", writer, http.StatusForbidden, "cannot run workflows"},
		// Retrying is starting a run, so it is held to the run scope rather
		// than to the read the rest of the executions prefix takes.
		{"read cannot retry", http.MethodPost, "/api/v1/executions/ex_1/retry", reader, http.StatusForbidden, "cannot run workflows"},
		{"write cannot retry", http.MethodPost, "/api/v1/executions/ex_1/retry", writer, http.StatusForbidden, "cannot run workflows"},
		// A copy is a new workflow, so it is the write scope or nothing.
		{"read cannot duplicate", http.MethodPost, "/api/v1/workflows/wf_1/duplicate", reader, http.StatusForbidden, "is read-only"},
		// A datastore scope reaches no workflow at all, which is how the
		// dry run and the evaluator are still refusals rather than reads.
		{"a datastore scope cannot validate", http.MethodPost, "/api/v1/workflows/validate", datastoreReader, http.StatusForbidden, "cannot read"},
		{"a datastore scope cannot convert pasted nodes", http.MethodPost, "/api/v1/workflows/convert", datastoreReader, http.StatusForbidden, "cannot read"},
		{"a datastore scope cannot evaluate", http.MethodPost, "/api/v1/executions/ex_1/eval", datastoreReader, http.StatusForbidden, "cannot read executions"},
		{"a workflow scope reaches no datastore", http.MethodGet, "/api/v1/datastores/ds_1/rows", reader, http.StatusForbidden, "cannot read"},
		{"a datastore scope reaches no workflow", http.MethodGet, "/api/v1/workflows/wf_1", datastoreReader, http.StatusForbidden, "cannot read"},
		{"a datastore read cannot write rows", http.MethodPost, "/api/v1/datastores/ds_1/rows", datastoreReader, http.StatusForbidden, "is read-only"},
		// The run scope implies read, so a runner can watch what it started.
		{"a run scope still reads", http.MethodGet, "/api/v1/workflows/wf_1", runner, 0, ""},
	})
}

// A workflow-bound token cannot read another workflow, and the answer is 404
// rather than 403: a refusal that said "you may not" would confirm the id is
// real, which is exactly what a token confined to one workflow must not be able
// to ask.
func TestAWorkflowBoundKeyReadsAnotherWorkflowAsMissing(t *testing.T) {
	t.Parallel()

	bound := agentKey("wf_1", embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun,
		embed.ScopeDatastoreRead, embed.ScopeDatastoreWrite)

	checkScopeRoutes(t, []scopeRoute{
		{"reads its own workflow", http.MethodGet, "/api/v1/workflows/wf_1", bound, 0, ""},
		{"reads another workflow as missing", http.MethodGet, "/api/v1/workflows/wf_2", bound, http.StatusNotFound, "Workflow not found"},
		{"updates another workflow as missing", http.MethodPut, "/api/v1/workflows/wf_2", bound, http.StatusNotFound, "Workflow not found"},
		{"runs another workflow as missing", http.MethodPost, "/api/v1/workflows/wf_2/run", bound, http.StatusNotFound, "Workflow not found"},
		{"duplicates another workflow as missing", http.MethodPost, "/api/v1/workflows/wf_2/duplicate", bound, http.StatusNotFound, "Workflow not found"},
		// A bound token may read and run the one workflow it names, and may
		// not bring a sibling into existence — a copy is outside its subject in
		// exactly the way an import is.
		{"cannot duplicate its own workflow", http.MethodPost, "/api/v1/workflows/wf_1/duplicate", bound, http.StatusForbidden, "cannot duplicate workflows"},
		{"lists its own executions", http.MethodGet, "/api/v1/executions?workflowId=wf_1", bound, 0, ""},
		{"cannot list another workflow's executions", http.MethodGet, "/api/v1/executions?workflowId=wf_2", bound, http.StatusForbidden, "must list executions of its own workflow"},
		{"cannot list every execution", http.MethodGet, "/api/v1/executions", bound, http.StatusForbidden, "must list executions of its own workflow"},
		// The collections a bound token must not enumerate: the listing would
		// show it every sibling name, and a schedule names its workflow in the
		// body where this gate cannot read it.
		{"cannot list workflows", http.MethodGet, "/api/v1/workflows", bound, http.StatusForbidden, "cannot list workflows"},
		{"cannot list datastores", http.MethodGet, "/api/v1/datastores", bound, http.StatusForbidden, "cannot manage datastores"},
		{"cannot manage schedules", http.MethodGet, "/api/v1/schedules", bound, http.StatusForbidden, "cannot manage schedules"},
		// Rows under one datastore id are still data-plane, and the binding is
		// about the workflow rather than about which tables exist.
		{"still reads rows", http.MethodGet, "/api/v1/datastores/ds_1/rows", bound, 0, ""},
	})
}

// The tenant-wide key — the legacy credential, no scopes at all — is untouched
// by this arm, which is what keeps an operator's existing automation working.
func TestAScopedArmLeavesTheTenantWideKeyAlone(t *testing.T) {
	t.Parallel()

	gate := ScopeAuth()
	for _, route := range []struct {
		name   string
		method string
		path   string
		ctx    func() *http.Request
	}{
		{
			name: "a tenant-wide key reaches activation", method: http.MethodPost, path: "/api/v1/workflows/wf_1/activate",
			ctx: func() *http.Request {
				return requestWithPrincipal(auth.Principal{
					TenantID: "tenant-a", KeyID: "key_tenant", Kind: auth.KindAPIKey, Label: "host",
				})
			},
		},
		{
			name: "a session reaches key management", method: http.MethodPost, path: "/api/v1/api-keys",
			ctx: func() *http.Request {
				return requestWithPrincipal(auth.Principal{
					TenantID: "tenant-a", UserID: "user_1", Kind: auth.KindSession, Label: "owner@example.com",
				})
			},
		},
		{
			name: "an unauthenticated request is left to the auth layer", method: http.MethodGet, path: "/api/v1/tenants",
			ctx: func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil) },
		},
	} {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			request := route.ctx()
			request.Method = route.method
			request.URL.Path = route.path
			reached := false
			handler := gate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if !reached {
				t.Errorf("%s %s did not reach the handler", route.method, route.path)
			}
		})
	}
}

// requestWithPrincipal is a request the auth layer has already admitted.
func requestWithPrincipal(principal auth.Principal) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	return request.WithContext(auth.WithPrincipal(request.Context(), principal))
}

// A scoped key is refused by the mounted gate with the problem document the CLI
// reads: 403 carries the named reason, and a binding mismatch carries 404.
func TestScopeAuthAnswersWithTheProblemDocument(t *testing.T) {
	t.Parallel()

	handler := ScopeAuth()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a refused request reached the handler")
	}))

	request := requestWithPrincipal(auth.Principal{
		TenantID: "tenant-a", KeyID: "key_agent", Kind: auth.KindAPIKey, Label: "agent",
		Scopes: []embed.Scope{embed.ScopeRead}, WorkflowID: "wf_1",
	})
	request.Method = http.MethodPost
	request.URL.Path = "/api/v1/workflows/wf_1/activate"

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", contentType)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "cannot change activation") {
		t.Errorf("body = %s, want it to name the refusal", body)
	}
}
