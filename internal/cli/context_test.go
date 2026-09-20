package cli

import (
	"net/http"
	"testing"
)

// contextStub is the read surface `context` briefs from, with every section's
// answer overridable so one test can fail a single section.
type contextStub struct {
	ready      http.HandlerFunc
	health     http.HandlerFunc
	me         http.HandlerFunc
	workflows  http.HandlerFunc
	datastores http.HandlerFunc
	nodeTypes  http.HandlerFunc
	// limits records the limit each listing was asked for.
	limits map[string]string
}

func newContextStub() *contextStub {
	return &contextStub{
		ready:      jsonBody(http.StatusOK, `{"status":"ready"}`),
		health:     jsonBody(http.StatusOK, `{"status":"ok","version":"9.9.9"}`),
		me:         jsonBody(http.StatusOK, `{"tenantId":"t_1","kind":"api_key","label":"agent","keyId":"k_1"}`),
		workflows:  jsonBody(http.StatusOK, `[{"id":"wf_1","name":"Manual","active":true,"latestRevision":1}]`),
		datastores: jsonBody(http.StatusOK, `{"items":[{"id":"ds_1","name":"Orders","columns":[]}]}`),
		nodeTypes:  jsonBody(http.StatusOK, `[{},{},{}]`),
		limits:     map[string]string{},
	}
}

// routes renders the stub's handlers under a recording wrapper.
func (s *contextStub) routes(t *testing.T) map[string]http.HandlerFunc {
	t.Helper()

	record := func(name string, handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.limits[name] = r.URL.Query().Get("limit")
			handler(w, r)
		}
	}

	return map[string]http.HandlerFunc{
		apiPrefix + "/ready":      s.ready,
		apiPrefix + "/health":     s.health,
		apiPrefix + "/auth/me":    s.me,
		apiPrefix + "/workflows":  record("workflows", s.workflows),
		apiPrefix + "/datastores": record("datastores", s.datastores),
		apiPrefix + "/node-types": s.nodeTypes,
	}
}

func TestContextBriefsAnAgentFromTheReadOperations(t *testing.T) {
	stub := newContextStub()
	stub.workflows = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Next-Cursor", "cur_2")
		_, _ = w.Write([]byte(`[{"id":"wf_1","name":"Manual","active":true,"latestRevision":3}]`))
	}
	srv := stubAPI(t, stub.routes(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:    []string{"context", "--url", srv.URL, "--json"},
		TTY:     true,
		Version: "1.2.3",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	if doc["ok"] != true {
		t.Fatalf("envelope = %v, want ok", doc)
	}
	meta, _ := doc["meta"].(map[string]any)
	if meta["operation"] != "context" {
		t.Fatalf("meta.operation = %v, want context", meta["operation"])
	}

	data, _ := doc["data"].(map[string]any)
	cli, _ := data["cli"].(map[string]any)
	if cli["version"] != "1.2.3" {
		t.Fatalf("data.cli = %v, want the binary version", data["cli"])
	}

	server, _ := data["server"].(map[string]any)
	if server["url"] != srv.URL {
		t.Fatalf("data.server.url = %v, want %s", server["url"], srv.URL)
	}
	if server["ready"] != true {
		t.Fatalf("data.server.ready = %v, want true", server["ready"])
	}
	health, _ := server["health"].(map[string]any)
	if health["status"] != "ok" {
		t.Fatalf("data.server.health = %v, want the get-health body", server["health"])
	}

	identity, _ := data["identity"].(map[string]any)
	for field, want := range map[string]any{"tenantId": "t_1", "kind": "api_key", "label": "agent", "keyId": "k_1"} {
		if identity[field] != want {
			t.Errorf("data.identity.%s = %v, want %v", field, identity[field], want)
		}
	}
	if _, present := identity["error"]; present {
		t.Errorf("data.identity = %v, want no error section", identity)
	}

	workflows, _ := data["workflows"].(map[string]any)
	if workflows["count"] != float64(1) || workflows["truncated"] != true {
		t.Fatalf("data.workflows = %v, want one item and truncated true", workflows)
	}
	items, _ := workflows["items"].([]any)
	first, _ := items[0].(map[string]any)
	if first["id"] != "wf_1" || first["name"] != "Manual" || first["active"] != true {
		t.Fatalf("data.workflows.items[0] = %v, want id, name and active", items[0])
	}
	if _, present := first["latestRevision"]; present {
		t.Fatalf("data.workflows.items[0] = %v, want only id, name and active", first)
	}

	datastores, _ := data["datastores"].(map[string]any)
	if datastores["count"] != float64(1) || datastores["truncated"] != false {
		t.Fatalf("data.datastores = %v, want one item and truncated false", datastores)
	}
	nodeTypes, _ := data["nodeTypes"].(map[string]any)
	if nodeTypes["count"] != float64(3) {
		t.Fatalf("data.nodeTypes = %v, want the catalogue size", data["nodeTypes"])
	}

	if stub.limits["workflows"] != "20" || stub.limits["datastores"] != "20" {
		t.Fatalf("limits = %v, want both listings capped at 20", stub.limits)
	}
}

func TestContextRecordsASectionFailureWithoutFailingTheVerb(t *testing.T) {
	stub := newContextStub()
	stub.workflows = problemBody(http.StatusServiceUnavailable, `{"title":"Unavailable","status":503,"detail":"workflow storage unavailable"}`)
	srv := stubAPI(t, stub.routes(t))

	code, _, stdout, stderr := runCLI(t, Env{Args: []string{"context", "--url", srv.URL, "--json"}, TTY: true})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d: one section failing is not the briefing failing (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	workflows, _ := data["workflows"].(map[string]any)
	failure, _ := workflows["error"].(map[string]any)
	if failure["code"] != "not_ready" {
		t.Fatalf("data.workflows.error = %v, want the section's error code", workflows["error"])
	}
	if failure["message"] == nil || failure["message"] == "" {
		t.Fatalf("data.workflows.error = %v, want a message", workflows["error"])
	}

	// The sections that did answer are still there: that is the point of
	// failing one section rather than the verb.
	nodeTypes, _ := data["nodeTypes"].(map[string]any)
	if nodeTypes["count"] != float64(3) {
		t.Fatalf("data.nodeTypes = %v, want the sections that answered", data["nodeTypes"])
	}
}

func TestContextTreatsAnUnauthenticatedIdentityAsASection(t *testing.T) {
	stub := newContextStub()
	stub.me = problemBody(http.StatusUnauthorized, `{"title":"Unauthorized","status":401,"detail":"this request is not authenticated"}`)
	srv := stubAPI(t, stub.routes(t))

	code, _, stdout, stderr := runCLI(t, Env{Args: []string{"context", "--url", srv.URL, "--json"}, TTY: true})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d: an auth-disabled server has no identity (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	identity, _ := data["identity"].(map[string]any)
	failure, _ := identity["error"].(map[string]any)
	if failure["code"] != "unauthenticated" {
		t.Fatalf("data.identity = %v, want an unauthenticated section", identity)
	}
}

func TestContextTreatsAnUnreadableListingAsASectionFailure(t *testing.T) {
	stub := newContextStub()
	stub.workflows = jsonBody(http.StatusOK, `{"items":[]}`)
	srv := stubAPI(t, stub.routes(t))

	code, _, stdout, stderr := runCLI(t, Env{Args: []string{"context", "--url", srv.URL, "--json"}, TTY: true})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	workflows, _ := data["workflows"].(map[string]any)
	if workflows["error"] == nil {
		t.Fatalf("data.workflows = %v, want a section error: an unreadable page is not an empty one", workflows)
	}
	if workflows["count"] != float64(0) {
		t.Fatalf("data.workflows.count = %v, want 0", workflows["count"])
	}
}

func TestContextFailsWhenReadinessFails(t *testing.T) {
	stub := newContextStub()
	stub.ready = problemBody(http.StatusServiceUnavailable, `{"title":"Unavailable","status":503,"detail":"migrations are outstanding"}`)
	srv := stubAPI(t, stub.routes(t))

	code, _, stdout, _ := runCLI(t, Env{Args: []string{"context", "--url", srv.URL, "--json"}, TTY: true})
	if code != ExitNotReady {
		t.Fatalf("exit = %d, want %d", code, ExitNotReady)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "not_ready" {
		t.Fatalf("error.code = %v, want not_ready", failure["code"])
	}
}

func TestContextRefusesToBriefWithoutAServerURL(t *testing.T) {
	code, handled, stdout, _ := runCLI(t, Env{
		Args: []string{"context", "--json", "--url", ""},
		TTY:  true,
	})
	if !handled || code != ExitUsage {
		t.Fatalf("exit = %d handled = %v, want %d", code, handled, ExitUsage)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "usage" {
		t.Fatalf("error.code = %v, want usage", failure["code"])
	}
}
