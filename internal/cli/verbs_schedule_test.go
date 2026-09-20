package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestScheduleListReadsAPage(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/schedules": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Next-Cursor", "cur_2")
			_, _ = io.WriteString(w, `[{"id":"sch_1","workflowId":"wf_1","cron":"0 * * * *","active":true,"createdAt":"2026-09-20T10:00:00Z","updatedAt":"2026-09-20T10:00:00Z"}]`)
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"schedule", "list", "--url", srv.URL, "--limit", "5", "--cursor", "cur_1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/schedules" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/schedules")
	}
	for _, want := range []string{"limit=5", "cursor=cur_1"} {
		if !strings.Contains(call.Query, want) {
			t.Errorf("query %q is missing %s", call.Query, want)
		}
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) || data["nextCursor"] != "cur_2" {
		t.Fatalf("data = %v, want the page", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-schedules" {
		t.Fatalf("meta.operation = %v, want list-schedules", meta["operation"])
	}
}
