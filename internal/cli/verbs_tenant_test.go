package cli

import (
	"net/http"
	"testing"
)

func TestTenantListReadsTheTenants(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/tenants": jsonBody(http.StatusOK,
			`{"items":[{"id":"acme","name":"Acme","userCount":2,"createdAt":"2026-09-20T10:00:00Z"}]}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"tenant", "list", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/tenants" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/tenants")
	}
	if call.Query != "" {
		t.Fatalf("query = %q, want none: list-tenants is not paged", call.Query)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) {
		t.Fatalf("data = %v, want the tenants", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-tenants" {
		t.Fatalf("meta.operation = %v, want list-tenants", meta["operation"])
	}

	// The operator listing needs an operator key, so a customer key's refusal
	// has to survive the verb: 403 is exit 3 with the API's own problem
	// document, not a silent empty list.
	refused := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/tenants": problemBody(http.StatusForbidden,
			`{"title":"forbidden","status":403,"detail":"this endpoint is reserved for the operator"}`),
	})
	refusedSrv := stubAPI(t, refused.routesFor(t))

	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"tenant", "list", "--url", refusedSrv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitRefused, stderr)
	}
	if failure := envelopeFailure(envelope(t, stdout)); failure != "scope_denied" {
		t.Fatalf("error.code = %q, want scope_denied", failure)
	}
}

func TestTenantGetReadsOneTenant(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/tenants/acme": jsonBody(http.StatusOK,
			`{"id":"acme","name":"Acme","userCount":2,"createdAt":"2026-09-20T10:00:00Z"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"tenant", "get", "acme", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodGet || call.Path != apiPrefix+"/tenants/acme" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/tenants/acme")
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["name"] != "Acme" {
		t.Fatalf("data = %v, want the tenant", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "get-tenant" {
		t.Fatalf("meta.operation = %v, want get-tenant", meta["operation"])
	}
}

func TestTenantUsersReadsTheAccounts(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/tenants/acme/users": jsonBody(http.StatusOK,
			`{"items":[{"id":"usr_1","tenantId":"acme","email":"ops@acme.test","name":"Ops","createdAt":"2026-09-20T10:00:00Z"}]}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"tenant", "users", "acme", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodGet || call.Path != apiPrefix+"/tenants/acme/users" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/tenants/acme/users")
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) {
		t.Fatalf("data = %v, want the accounts", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-tenant-users" {
		t.Fatalf("meta.operation = %v, want list-tenant-users", meta["operation"])
	}
}
