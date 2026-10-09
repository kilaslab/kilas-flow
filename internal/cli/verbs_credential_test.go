package cli

import (
	"net/http"
	"strings"
	"testing"
)

// The server refuses to delete a credential a workflow still attaches, with
// 409 and the workflows named; `credential delete` reports that as a conflict
// and sends nothing extra, and `--force` asks for the delete anyway.
func TestCredentialDeleteForceSendsForceAndARefusalIsAConflict(t *testing.T) {
	inUse := true
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/credentials/cred_1": func(w http.ResponseWriter, r *http.Request) {
			if inUse && r.URL.Query().Get("force") != "true" {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"status":409,"title":"Conflict","detail":"this credential is attached by 1 workflow, which would fail without it: attach another credential to them first, or delete with force=true","errors":[{"message":"inactive workflow \"Orders\" uses it in \"HTTP Request\"","location":"workflows/wf_1"}]}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	routes := api.routesFor(t)
	// A guarded verb asks who the token is first; a tenant-wide key may delete.
	routes[apiPrefix+"/auth/me"] = jsonBody(http.StatusOK, identityWithoutScopes)
	srv := stubAPI(t, routes)

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"credential", "delete", "cred_1", "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitConflict {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitConflict, stdout, stderr)
	}
	if call := api.last(t); call.Method != http.MethodDelete || call.Query != "" {
		t.Fatalf("call = %+v, want a DELETE with no query", call)
	}
	if !strings.Contains(stdout+stderr, "force=true") || !strings.Contains(stdout+stderr, "workflows/wf_1") {
		t.Fatalf("output = %q %q, want the server's refusal and the workflow it names", stdout, stderr)
	}

	api.calls = nil
	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"credential", "delete", "cred_1", "--force", "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit with --force = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if call := api.last(t); call.Method != http.MethodDelete || call.Path != apiPrefix+"/credentials/cred_1" || call.Query != "force=true" {
		t.Fatalf("call = %+v, want DELETE %s/credentials/cred_1?force=true", call, apiPrefix)
	}

	// --force does not replace --yes: the verb stays guarded.
	api.calls = nil
	code, _, _, _ = runCLI(t, Env{
		Args:   []string{"credential", "delete", "cred_1", "--force", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitRefused || len(api.calls) != 0 {
		t.Fatalf("--force without --yes: exit = %d with %d requests, want %d and none", code, len(api.calls), ExitRefused)
	}
}
