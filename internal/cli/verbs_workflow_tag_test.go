package cli

import (
	"io"
	"net/http"
	"net/url"
	"testing"
)

func TestWorkflowListSendsTheTagFilter(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"wf_1","name":"Orders","active":false,"tags":["Prod","billing"]}]`)
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "list", "--url", srv.URL, "--tag", "Prod team", "--limit", "5", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	query, err := url.ParseQuery(api.last(t).Query)
	if err != nil {
		t.Fatalf("ParseQuery() error = %v", err)
	}
	if query.Get("tag") != "Prod team" || query.Get("limit") != "5" {
		t.Errorf("query = %v, want tag=Prod team and limit=5", query)
	}

	// The summary's tags reach the output as the server wrote them.
	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	items, _ := data["items"].([]any)
	first, _ := items[0].(map[string]any)
	if tags, _ := first["tags"].([]any); len(tags) != 2 || tags[0] != "Prod" {
		t.Errorf("data.items[0].tags = %v, want the server's tags", first["tags"])
	}
}

func TestWorkflowListSendsNoTagParameterWithoutTheFlag(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows": jsonBody(http.StatusOK, `[]`),
	})
	srv := stubAPI(t, api.routesFor(t))

	for _, args := range [][]string{
		{"workflow", "list", "--url", srv.URL, "--json"},
		{"workflow", "list", "--url", srv.URL, "--tag", "  ", "--json"},
	} {
		code, _, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
		if code != ExitOK {
			t.Fatalf("%v: exit = %d, want %d (stdout=%q stderr=%q)", args, code, ExitOK, stdout, stderr)
		}
		if query := api.last(t).Query; query != "" {
			t.Errorf("%v: query = %q, want no parameters", args, query)
		}
		api.calls = nil
	}
}

func TestTheWorkflowListToolTakesATagArgument(t *testing.T) {
	srv, api := mcpStub(t, map[string]http.HandlerFunc{
		"/": jsonBody(http.StatusOK, `[{"id":"wf_1","name":"Orders","active":false,"tags":["Prod"]}]`),
	})
	session := startMCPServe(t, srv.URL)
	session.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "harness", "version": "1"},
	})
	session.notify("notifications/initialized")

	call := session.call("workflow_list", map[string]any{"tag": "prod"})
	if call.isError {
		t.Fatalf("workflow_list failed: %q", call.text)
	}
	if len(api.calls) != 1 || api.calls[0].Query != "tag=prod" {
		t.Errorf("the tool call reached the server as %+v, want one request with tag=prod", api.calls)
	}
}
