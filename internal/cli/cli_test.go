package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// runCLI drives Run exactly the way cmd/kilasflow does, with both streams
// captured so a test can assert what a caller would see.
func runCLI(t *testing.T, env Env) (code int, handled bool, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer
	env.Stdout = &out
	env.Stderr = &errOut
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	if env.Version == "" {
		env.Version = "0.0.0-test"
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}

	code, handled = Run(env)

	return code, handled, out.String(), errOut.String()
}

// stubAPI serves the handful of routes a test needs over a real socket, so the
// client goes through a real HTTP round trip rather than a mocked transport.
func stubAPI(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	for path, handler := range routes {
		mux.HandleFunc(path, handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// jsonBody answers with an application/json body.
func jsonBody(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// problemBody answers with an RFC 9457 problem document, the way the API's
// huma error handlers do.
func problemBody(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// envelope decodes the single JSON document a JSON-mode invocation writes to
// stdout, and fails when stdout carried anything else.
func envelope(t *testing.T, stdout string) map[string]any {
	t.Helper()

	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v (stdout=%q)", err, stdout)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout carried more than one JSON document: %q", stdout)
	}

	return doc
}

func TestRunLeavesTheServerPathAlone(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"server flag", []string{"-config", "x.yaml"}},
		{"version flag", []string{"-version"}},
		{"help flag", []string{"-h"}},
		{"explicit serve", []string{"serve"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, handled, _, _ := runCLI(t, Env{Args: tc.args, TTY: true})
			if handled {
				t.Fatalf("Run(%q) handled the invocation; the server path must run instead", tc.args)
			}
			if code != ExitOK {
				t.Fatalf("Run(%q) = exit %d, want %d", tc.args, code, ExitOK)
			}
		})
	}
}

// The server's own flag set refuses a leftover positional argument, so the word
// that selected the server path has to come out of the arguments the server
// parses. Before this, `kilasflow serve` handed the server []string{"serve"}:
// the documented explicit form printed the server's usage and exited 1.
func TestServerArgsDropsTheServeVerb(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"the explicit verb alone", []string{"serve"}, []string{}},
		{"the server's flags are left untouched", []string{"serve", "-config", "x.yaml", "-role", "api"}, []string{"-config", "x.yaml", "-role", "api"}},
		{"no verb at all", []string{"-config", "x.yaml"}, []string{"-config", "x.yaml"}},
		{"nothing to parse", nil, nil},
		{"a word that only resembles the verb", []string{"server"}, []string{"server"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServerArgs(tc.args); !slices.Equal(got, tc.want) {
				t.Fatalf("ServerArgs(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// `-h` is where the reference page sends an agent that does not know a verb, so
// the flags the verb takes have to be there: without the defaults printed, it
// printed the summary alone and the escape hatch's --path/--query/--header/
// --body/--body-file/--out were undiscoverable from the CLI itself.
func TestVerbHelpListsTheFlagsTheVerbTakes(t *testing.T) {
	code, handled, stdout, stderr := runCLI(t, Env{Args: []string{"api", "-h"}, TTY: true})
	if !handled || code != ExitOK {
		t.Fatalf("exit = %d handled = %v, want %d (stdout=%q)", code, handled, ExitOK, stdout)
	}

	// Go's flag package prints the single-dash spelling; both spellings parse,
	// and these are the names the reference page uses.
	for _, flagName := range []string{"-path", "-query", "-header", "-body", "-body-file", "-out", "-url"} {
		if !strings.Contains(stderr, flagName) {
			t.Errorf("`api -h` does not list %s: %q", flagName, stderr)
		}
	}
}

func TestUnknownVerbIsAUsageError(t *testing.T) {
	// `kflow` is a stale binary name, not an alias: it must be refused like any
	// other unknown verb rather than falling through to a server boot.
	code, handled, _, stderr := runCLI(t, Env{Args: []string{"kflow"}, TTY: true})
	if !handled {
		t.Fatal("an unknown verb fell through to the server path")
	}
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "kflow") {
		t.Fatalf("stderr does not name the refused word: %q", stderr)
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	code, handled, _, stderr := runCLI(t, Env{Args: []string{"health", "--nope"}, TTY: true})
	if !handled {
		t.Fatal("a known verb fell through to the server path")
	}
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "nope") {
		t.Fatalf("stderr does not name the refused flag: %q", stderr)
	}
}

func TestACLIVerbIsHandledByTheCLI(t *testing.T) {
	code, handled, stdout, _ := runCLI(t, Env{Args: []string{"version", "--json"}, TTY: true})
	if !handled {
		t.Fatal("`version --json` fell through to the server path")
	}
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q)", code, ExitOK, stdout)
	}

	doc := envelope(t, stdout)
	if doc["ok"] != true {
		t.Fatalf("ok = %v, want true", doc["ok"])
	}
	meta, _ := doc["meta"].(map[string]any)
	if meta["operation"] != "version" {
		t.Fatalf("meta.operation = %v, want version", meta["operation"])
	}
}

func TestFlagValuesAreNotMistakenForPositionals(t *testing.T) {
	// --url takes a value that looks nothing like a flag, and --timeout a
	// duration: both must be consumed rather than left as trailing arguments.
	code, handled, stdout, _ := runCLI(t, Env{
		Args: []string{"version", "--url", "http://127.0.0.1:1", "--timeout", "250ms", "--json"},
		TTY:  true,
	})
	if !handled {
		t.Fatal("`version` fell through to the server path")
	}
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q)", code, ExitOK, stdout)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	server, _ := data["server"].(map[string]any)
	if server["reachable"] != false {
		t.Fatalf("data.server = %v, want an unreachable server", data["server"])
	}
}

func TestIsTTYRejectsAnythingThatIsNotACharacterDevice(t *testing.T) {
	if IsTTY(&bytes.Buffer{}) {
		t.Fatal("IsTTY reported true for a buffer")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	if IsTTY(w) {
		t.Fatal("IsTTY reported true for a pipe")
	}
	if IsTTY(nil) {
		t.Fatal("IsTTY reported true for nil")
	}
}
