package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCredentialListReadsAPage(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/credentials": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Next-Cursor", "cur_2")
			_, _ = io.WriteString(w, `[{"id":"cred_1","name":"SMTP","type":"smtp","fields":{},"updatedAt":"2026-09-20T10:00:00Z"}]`)
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"credential", "list", "--url", srv.URL, "--limit", "5", "--cursor", "cur_1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/credentials" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/credentials")
	}
	for _, want := range []string{"limit=5", "cursor=cur_1"} {
		if !strings.Contains(call.Query, want) {
			t.Errorf("query %q is missing %s", call.Query, want)
		}
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) || data["nextCursor"] != "cur_2" {
		t.Fatalf("data = %v, want the page and the cursor the API sent", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-credentials" {
		t.Fatalf("meta.operation = %v, want list-credentials", meta["operation"])
	}
}

func TestCredentialGetReadsOneWithoutItsSecret(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/credentials/cred_1": jsonBody(http.StatusOK,
			`{"id":"cred_1","name":"SMTP","type":"smtp","fields":{"password":"[redacted]"},"updatedAt":"2026-09-20T10:00:00Z"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"credential", "get", "cred_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodGet || call.Path != apiPrefix+"/credentials/cred_1" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/credentials/cred_1")
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["name"] != "SMTP" {
		t.Fatalf("data = %v, want the credential", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "get-credential" {
		t.Fatalf("meta.operation = %v, want get-credential", meta["operation"])
	}
}

func TestCredentialTestPostsTheProbeAndReportsTheVerdict(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/credentials/cred_1/test": jsonBody(http.StatusOK,
			`{"ok":true,"resolvedFromStorage":["password"]}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"credential", "test", "cred_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/credentials/cred_1/test" {
		t.Fatalf("call = %+v, want POST %s", call, apiPrefix+"/credentials/cred_1/test")
	}
	if call.Body != "" {
		t.Fatalf("body = %q, want no body: the probe reads the stored credential", call.Body)
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["ok"] != true {
		t.Fatalf("data = %v, want the verdict", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "test-credential" {
		t.Fatalf("meta.operation = %v, want test-credential", meta["operation"])
	}

	// The verdict is the identifier a pipeline branches on, so --quiet prints
	// the API's own boolean rather than the credential id.
	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"credential", "test", "cred_1", "--url", srv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("--quiet exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if stdout != "true\n" {
		t.Fatalf("--quiet printed %q, want the verdict", stdout)
	}
}

// TestVerboseTraceNeverPrintsACredentialSecret: --verbose promises a trace
// with credentials redacted, and the body a credential is created or replaced
// with is nothing but its secrets. A header credential keeps its secret under
// fields.value and an OAuth2 one under names no credential-key list anticipates
// (clientSecret, accessToken, refreshToken), so the trace redacts a request
// body by the keys a problem document is redacted by: fields as a whole, and
// value.
func TestVerboseTraceNeverPrintsACredentialSecret(t *testing.T) {
	secrets := []string{"HEADER-SECRET-1", "CLIENT-SECRET-2", "ACCESS-TOKEN-3", "REFRESH-TOKEN-4"}
	header := `{"name":"Search","type":"httpHeaderAuth","fields":{"name":"X-Api-Key","value":"HEADER-SECRET-1"}}`
	oauth := `{"name":"Mail","type":"oAuth2Api","fields":{"clientId":"client-1","clientSecret":"CLIENT-SECRET-2",` +
		`"accessToken":"ACCESS-TOKEN-3","refreshToken":"REFRESH-TOKEN-4"}}`

	for _, testCase := range []struct {
		name string
		args func(t *testing.T) []string
		want string
	}{
		{
			name: "credential create with a header credential",
			args: func(t *testing.T) []string {
				return []string{"credential", "create", "--file", fileAt(t, header, 0o600)}
			},
			want: "httpHeaderAuth",
		},
		{
			name: "credential update with an OAuth2 credential",
			args: func(t *testing.T) []string {
				return []string{"credential", "update", "cred_1", "--file", fileAt(t, oauth, 0o600)}
			},
			want: "oAuth2Api",
		},
		{
			name: "api create-credential with a header credential",
			args: func(*testing.T) []string { return []string{"api", "create-credential", "--body", header} },
			want: "httpHeaderAuth",
		},
		{
			name: "api create-credential with an OAuth2 credential",
			args: func(*testing.T) []string { return []string{"api", "create-credential", "--body", oauth} },
			want: "oAuth2Api",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			api := newRecordingAPI(map[string]http.HandlerFunc{
				apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
				operationsPath: jsonBody(http.StatusOK, servedDocument(
					[3]string{http.MethodPost, "/api/v1/credentials", "create-credential"},
				)),
				apiPrefix + "/credentials":        jsonBody(http.StatusCreated, `{"id":"cred_1","name":"ok"}`),
				apiPrefix + "/credentials/cred_1": jsonBody(http.StatusOK, `{"id":"cred_1","name":"ok"}`),
			})
			srv := stubAPI(t, api.routesFor(t))

			args := append(testCase.args(t), "--yes", "--verbose", "--url", srv.URL, "--json")
			code, _, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
			}
			if !strings.Contains(stderr, "> body ") || !strings.Contains(stderr, testCase.want) {
				t.Fatalf("the trace did not show the redacted body: %q", stderr)
			}
			for _, secret := range secrets {
				if strings.Contains(stderr, secret) || strings.Contains(stdout, secret) {
					t.Fatalf("the trace printed %s (stderr=%q)", secret, stderr)
				}
			}
		})
	}
}
