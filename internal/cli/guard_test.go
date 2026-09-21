package cli

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runWithVerbs drives run() against an explicit verb list, the way runCLI drives
// Run against the binary's own registry. The guard's tests use it for a fixture
// registered inside the test, so the --yes contract can be driven without
// naming a verb in the real tree.
func runWithVerbs(t *testing.T, verbs []Verb, env Env) (code int, handled bool, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer
	env.Stdout, env.Stderr = &out, &errOut
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	if env.Version == "" {
		env.Version = "0.0.0-test"
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}

	code, handled = run(verbs, env)

	return code, handled, out.String(), errOut.String()
}

// guardedFixture is the verb the guard's tests refuse: it publishes a public
// endpoint, which is what the refusal sentence has to say.
//
// It is registered by the test rather than taken from the tree so the refusal
// can be driven against a verb that does nothing, but it is guarded, so the run
// that passes --yes still reads the caller's identity first: the cases below
// serve a tenant-wide key for exactly that reason.
func guardedFixture(ran *bool) Verb {
	return Verb{
		Path:    "fixture destroy",
		Summary: "a guarded verb registered by the guard's own test",
		Guarded: true,
		Refusal: "publishes a public endpoint",
		Run: func(ctx *Context, _ []string) error {
			*ran = true
			ctx.Data = map[string]any{"destroyed": true}
			ctx.Primary = "fixture-ran"

			return nil
		},
	}
}

// TestGuardedVerbRequiresYes proves the whole refusal contract: the exit code,
// the error code, the message, and that nothing but the literal flag implies
// consent.
func TestGuardedVerbRequiresYes(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
	})

	cases := []struct {
		name       string
		args       []string
		tty        bool
		wantJSON   bool
		wantRefuse bool
	}{
		{name: "no confirmation", args: []string{"fixture", "destroy"}, tty: true, wantRefuse: true},
		{name: "--json alone", args: []string{"fixture", "destroy", "--json"}, tty: true, wantJSON: true, wantRefuse: true},
		{name: "--quiet alone", args: []string{"fixture", "destroy", "--quiet"}, tty: true, wantRefuse: true},
		{name: "stdout is not a terminal", args: []string{"fixture", "destroy"}, tty: false, wantJSON: true, wantRefuse: true},
		{name: "--json and stdout is not a terminal", args: []string{"fixture", "destroy", "--json"}, tty: false, wantJSON: true, wantRefuse: true},
		{name: "--yes confirms it", args: []string{"fixture", "destroy", "--yes"}, tty: true},
		{name: "--yes in JSON mode runs it", args: []string{"fixture", "destroy", "--yes", "--json"}, tty: true, wantJSON: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ran bool
			verbs := append(registry(), guardedFixture(&ran))

			args := append(append([]string{}, tc.args...), "--url", srv.URL)

			code, handled, stdout, stderr := runWithVerbs(t, verbs, Env{Args: args, TTY: tc.tty})
			if !handled {
				t.Fatalf("the guarded verb fell through to the server path (stdout=%q stderr=%q)", stdout, stderr)
			}

			if !tc.wantRefuse {
				if code != ExitOK {
					t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
				}
				if !ran {
					t.Fatal("the verb did not run even though --yes was passed")
				}

				return
			}

			if code != ExitRefused {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitRefused, stdout, stderr)
			}
			if ran {
				t.Fatal("the guarded verb ran without --yes")
			}

			if tc.wantJSON {
				doc := envelope(t, stdout)
				failure := envelopeFailure(doc)
				if doc["ok"] != false {
					t.Fatalf("ok = %v, want false", doc["ok"])
				}
				if failure != confirmationCode {
					t.Fatalf("error.code = %q, want %q", failure, confirmationCode)
				}
				message, _ := doc["error"].(map[string]any)["message"].(string)
				if !strings.Contains(message, "publishes a public endpoint") {
					t.Errorf("error.message %q does not name what the verb does", message)
				}
				if !strings.Contains(message, "--yes") {
					t.Errorf("error.message %q does not say how to confirm", message)
				}

				return
			}

			// Without --json and on a terminal the refusal is a stderr line, so
			// it must say the same thing there.
			if !strings.Contains(stderr, "publishes a public endpoint") || !strings.Contains(stderr, "--yes") {
				t.Fatalf("stderr = %q, want the refusal and the way to confirm", stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want nothing", stdout)
			}
		})
	}
}

// TestUnguardedVerbIgnoresYes: an agent may always pass --yes, so a verb with
// nothing to confirm must run and must not report anything about confirmation.
func TestUnguardedVerbIgnoresYes(t *testing.T) {
	var ran bool
	verbs := append(registry(), Verb{
		Path:    "fixture read",
		Summary: "an unguarded verb registered by the guard's own test",
		Run: func(ctx *Context, _ []string) error {
			ran = true
			ctx.Primary = "fixture-read"

			return nil
		},
	})

	code, _, stdout, stderr := runWithVerbs(t, verbs, Env{
		Args: []string{"fixture", "read", "--yes", "--json"},
		TTY:  true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if !ran {
		t.Fatal("--yes suppressed an unguarded verb")
	}

	if doc := envelope(t, stdout); doc["ok"] != true {
		t.Fatalf("ok = %v, want true", doc["ok"])
	}
}

// TestConfirmationCodeIsDistinctFromScopeDenied drives all three refusals
// through the CLI and asserts what the contract promises: one exit code, three
// error codes, so an agent can tell "I cannot do this" from "I was not
// confirmed" from "I am not authenticated".
func TestConfirmationCodeIsDistinctFromScopeDenied(t *testing.T) {
	envelopes := map[string]string{}

	var ran bool
	guarded := append(registry(), guardedFixture(&ran))
	code, _, stdout, stderr := runWithVerbs(t, guarded, Env{
		Args: []string{"fixture", "destroy", "--json"},
		TTY:  true,
	})
	if code != ExitRefused {
		t.Fatalf("the guard refusal exited %d, want %d (stderr=%q)", code, ExitRefused, stderr)
	}
	envelopes["guard"] = envelopeFailure(envelope(t, stdout))

	for name, status := range map[string]int{"scope_denied": http.StatusForbidden, "unauthenticated": http.StatusUnauthorized} {
		srv := stubAPI(t, map[string]http.HandlerFunc{
			apiPrefix + "/health": problemBody(status, `{"title":"refused","status":`+strconv.Itoa(status)+`}`),
		})
		code, _, stdout, stderr := runCLI(t, Env{
			Args:   []string{"health", "--url", srv.URL, "--json"},
			Getenv: homeEnv(t.TempDir(), nil),
		})
		if code != ExitRefused {
			t.Fatalf("%s exited %d, want %d (stderr=%q)", name, code, ExitRefused, stderr)
		}
		envelopes[name] = envelopeFailure(envelope(t, stdout))
	}

	if envelopes["guard"] != confirmationCode {
		t.Errorf("the guard refusal reported %q, want %q", envelopes["guard"], confirmationCode)
	}

	seen := map[string]string{}
	for source, failure := range envelopes {
		if other, duplicate := seen[failure]; duplicate {
			t.Errorf("%s and %s both report %q; the three refusals must be distinguishable", source, other, failure)
		}
		seen[failure] = source
	}
	if len(seen) != 3 {
		t.Fatalf("the three refusals report %d distinct codes, want 3: %v", len(seen), envelopes)
	}
}

// guardedInvocation is how a test drives one real guarded verb: the operation
// its metadata must name, the arguments that follow the verb's own words, and
// the request the verb makes once both gates let it through.
type guardedInvocation struct {
	operation string
	args      []string
	method    string
	path      string
}

// guardedInvocations is the design's §4.2 guarded set — every operation whose
// verb exists today — written out rather than derived from the registry, because
// it is what checks the registry: a verb that gained Guarded, lost the mark, or
// was registered with the wrong operation fails against this table. It is also
// the table the three gate tests below drive, so every guarded verb is proven
// against a real socket rather than one of them standing in for the rest.
//
// `pack install` is not here: the design marks it guarded, but the pack surface
// is a local loader with no `install` operation behind it, so there is nothing
// for a verb to drive.
func guardedInvocations(t *testing.T) map[string]guardedInvocation {
	t.Helper()

	document := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(document, []byte(`{"name":"SMTP","type":"smtp","fields":{}}`), 0o600); err != nil {
		t.Fatalf("could not write the credential document: %v", err)
	}

	return map[string]guardedInvocation{
		"workflow activate": {
			operation: "activate-workflow", args: []string{"wf_1"},
			method: http.MethodPost, path: apiPrefix + "/workflows/wf_1/activate",
		},
		"workflow deactivate": {
			operation: "deactivate-workflow", args: []string{"wf_1"},
			method: http.MethodPost, path: apiPrefix + "/workflows/wf_1/deactivate",
		},
		"workflow delete": {
			operation: "delete-workflow", args: []string{"wf_1"},
			method: http.MethodDelete, path: apiPrefix + "/workflows/wf_1",
		},
		"credential create": {
			operation: "create-credential", args: []string{"--file", document},
			method: http.MethodPost, path: apiPrefix + "/credentials",
		},
		"credential update": {
			operation: "update-credential", args: []string{"cred_1", "--file", document},
			method: http.MethodPut, path: apiPrefix + "/credentials/cred_1",
		},
		"credential delete": {
			operation: "delete-credential", args: []string{"cred_1"},
			method: http.MethodDelete, path: apiPrefix + "/credentials/cred_1",
		},
		"datastore create": {
			operation: "create-datastore", args: []string{"orders"},
			method: http.MethodPost, path: apiPrefix + "/datastores",
		},
		"datastore rename": {
			operation: "rename-datastore", args: []string{"ds_1", "orders-2026"},
			method: http.MethodPut, path: apiPrefix + "/datastores/ds_1",
		},
		"datastore delete": {
			operation: "delete-datastore", args: []string{"ds_1"},
			method: http.MethodDelete, path: apiPrefix + "/datastores/ds_1",
		},
		"datastore clear": {
			operation: "clear-datastore", args: []string{"ds_1"},
			method: http.MethodPost, path: apiPrefix + "/datastores/ds_1/clear",
		},
		"datastore columns add": {
			operation: "add-datastore-column", args: []string{"ds_1", "note", "--type", "string"},
			method: http.MethodPost, path: apiPrefix + "/datastores/ds_1/columns",
		},
		"datastore columns rename": {
			operation: "rename-datastore-column", args: []string{"ds_1", "note", "memo"},
			method: http.MethodPut, path: apiPrefix + "/datastores/ds_1/columns/note",
		},
		"datastore columns drop": {
			operation: "delete-datastore-column", args: []string{"ds_1", "note"},
			method: http.MethodDelete, path: apiPrefix + "/datastores/ds_1/columns/note",
		},
		"tenant delete": {
			operation: "delete-tenant", args: []string{"acme"},
			method: http.MethodDelete, path: apiPrefix + "/tenants/acme",
		},
	}
}

// guardedRegistryVerbs returns the guarded verbs the binary actually registers,
// and fails when the set is not the one the test table describes.
func guardedRegistryVerbs(t *testing.T, want map[string]guardedInvocation) map[string]Verb {
	t.Helper()

	guarded := map[string]Verb{}
	for _, verb := range registry() {
		if !verb.Guarded {
			continue
		}
		guarded[verb.Path] = verb
		if _, expected := want[verb.Path]; !expected {
			t.Errorf("verb %q is marked guarded but is not in the design's guarded set", verb.Path)
		}
	}
	for path := range want {
		if _, present := guarded[path]; !present {
			t.Errorf("verb %q is in the guarded set but the registry does not mark it guarded", path)
		}
	}

	return guarded
}

// identityWithoutScopes is a tenant-wide key: /auth/me reports a key with no
// scope list, which is the legacy shape the migration leaves untouched.
const identityWithoutScopes = `{"tenantId":"acme","kind":"api_key","keyId":"key_1"}`

// identityWithScopes is an agent token: the same principal with a scope list,
// which is what the authority gate turns on.
const identityWithScopes = `{"tenantId":"acme","kind":"api_key","keyId":"key_2","scopes":["workflow:read"]}`

// TestGuardedVerbSetMatchesTheRegistryAndTheServer is the registry-level gate:
// each guarded verb carries the operation the design's tree names, and the
// running server serves that operation — the same check TestPhaseOneCommandTree
// makes for the unguarded tree, applied to the guarded one.
func TestGuardedVerbSetMatchesTheRegistryAndTheServer(t *testing.T) {
	want := guardedInvocations(t)
	guarded := guardedRegistryVerbs(t, want)

	srv := bootContractServer(t)
	client := &Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 30 * time.Second}}

	index, err := client.Operations(context.Background())
	if err != nil {
		t.Fatalf("read the served operation index: %v", err)
	}
	if len(guarded) != len(want) {
		t.Fatalf("the registry marks %d verbs guarded, the design names %d", len(guarded), len(want))
	}

	for path, invocation := range want {
		verb, present := guarded[path]
		if !present {
			continue
		}
		if verb.Operation != invocation.operation {
			t.Errorf("verb %q drives %q, want %q", path, verb.Operation, invocation.operation)
		}
		if verb.Refusal == "" {
			t.Errorf("guarded verb %q has no refusal sentence to print", path)
		}
		if _, served := index[invocation.operation]; !served {
			t.Errorf("verb %q drives %q, which the running server does not serve", path, invocation.operation)
		}
	}
	t.Logf("%d guarded verbs, %d served operations", len(guarded), len(index))
}

// TestGuardedVerbWithoutYesSendsNothing: consent comes first and it costs
// nothing. The stub is a catch-all, so any request the invocation made — the
// identity probe included — would be recorded and fail the test.
func TestGuardedVerbWithoutYesSendsNothing(t *testing.T) {
	want := guardedInvocations(t)
	guarded := guardedRegistryVerbs(t, want)

	for _, path := range sortedGuardedPaths(want) {
		invocation := want[path]

		t.Run(path, func(t *testing.T) {
			api := newRecordingAPI(map[string]http.HandlerFunc{
				"/": jsonBody(http.StatusOK, `{"id":"never"}`),
			})
			srv := stubAPI(t, api.routesFor(t))

			args := append(strings.Fields(path), invocation.args...)
			args = append(args, "--url", srv.URL, "--json")

			code, handled, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
			if !handled {
				t.Fatalf("the verb fell through to the server path (stdout=%q stderr=%q)", stdout, stderr)
			}
			if code != ExitRefused {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitRefused, stdout, stderr)
			}

			doc := envelope(t, stdout)
			if failure := envelopeFailure(doc); failure != confirmationCode {
				t.Fatalf("error.code = %q, want %q", failure, confirmationCode)
			}
			message, _ := doc["error"].(map[string]any)["message"].(string)
			if !strings.Contains(message, guarded[path].Refusal) {
				t.Errorf("error.message %q does not name what the verb does", message)
			}
			if len(api.calls) != 0 {
				t.Fatalf("the refusal sent %d request(s): %+v, want none", len(api.calls), api.calls)
			}
		})
	}
}

// TestGuardedVerbRefusesAScopedTokenEvenWithYes is the second gate: --yes is
// consent, not authority. A key whose identity carries scopes may not run a
// guarded verb, so the invocation is refused before the operation — the only
// request that leaves the CLI is the identity read that made the refusal
// possible.
func TestGuardedVerbRefusesAScopedTokenEvenWithYes(t *testing.T) {
	want := guardedInvocations(t)
	guarded := guardedRegistryVerbs(t, want)

	for _, path := range sortedGuardedPaths(want) {
		invocation := want[path]

		t.Run(path, func(t *testing.T) {
			api := newRecordingAPI(map[string]http.HandlerFunc{
				apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithScopes),
				"/":                    jsonBody(http.StatusOK, `{"id":"should-not-happen"}`),
			})
			srv := stubAPI(t, api.routesFor(t))

			args := append(strings.Fields(path), invocation.args...)
			args = append(args, "--yes", "--url", srv.URL, "--json")

			code, handled, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
			if !handled {
				t.Fatalf("the verb fell through to the server path (stdout=%q stderr=%q)", stdout, stderr)
			}
			if code != ExitRefused {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitRefused, stdout, stderr)
			}

			doc := envelope(t, stdout)
			if failure := envelopeFailure(doc); failure != scopeDeniedCode {
				t.Fatalf("error.code = %q, want %q", failure, scopeDeniedCode)
			}
			message, _ := doc["error"].(map[string]any)["message"].(string)
			if !strings.Contains(message, guarded[path].Refusal) {
				t.Errorf("error.message %q does not name what the verb does", message)
			}
			if !strings.Contains(message, "tenant-wide key") {
				t.Errorf("error.message %q does not say what would be allowed", message)
			}

			if len(api.calls) != 1 || api.calls[0].Method != http.MethodGet || api.calls[0].Path != apiPrefix+"/auth/me" {
				t.Fatalf("the refusal sent %+v, want exactly the identity read", api.calls)
			}
		})
	}
}

// TestGuardedVerbWithATenantWideKeyReachesTheServer: both gates passed, the verb
// sends the operation it names. The mutating request is asserted by method and
// path, because "it exited 0" alone would not tell a verb that reached the
// server from one that quietly did nothing.
func TestGuardedVerbWithATenantWideKeyReachesTheServer(t *testing.T) {
	want := guardedInvocations(t)
	guardedRegistryVerbs(t, want)

	for _, path := range sortedGuardedPaths(want) {
		invocation := want[path]

		t.Run(path, func(t *testing.T) {
			api := newRecordingAPI(map[string]http.HandlerFunc{
				apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
				"/":                    jsonBody(http.StatusOK, `{"id":"ok_1","name":"ok"}`),
			})
			srv := stubAPI(t, api.routesFor(t))

			args := append(strings.Fields(path), invocation.args...)
			args = append(args, "--yes", "--url", srv.URL, "--json")

			code, handled, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
			if !handled {
				t.Fatalf("the verb fell through to the server path (stdout=%q stderr=%q)", stdout, stderr)
			}
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
			}

			if doc := envelope(t, stdout); doc["ok"] != true {
				t.Fatalf("ok = %v, want true", doc["ok"])
			}

			calls := mutatingCalls(api.calls)
			if len(calls) != 1 {
				t.Fatalf("the verb made %d requests beyond the identity read: %+v, want one", len(calls), calls)
			}
			if calls[0].Method != invocation.method || calls[0].Path != invocation.path {
				t.Fatalf("the verb sent %s %s, want %s %s", calls[0].Method, calls[0].Path, invocation.method, invocation.path)
			}
		})
	}
}

// mutatingCalls drops the identity read, which is the one request a guarded verb
// makes before the operation it was asked for.
func mutatingCalls(calls []recordedCall) []recordedCall {
	out := make([]recordedCall, 0, len(calls))
	for _, call := range calls {
		if call.Method == http.MethodGet && call.Path == apiPrefix+"/auth/me" {
			continue
		}
		out = append(out, call)
	}

	return out
}

// sortedGuardedPaths keeps the subtests deterministic.
func sortedGuardedPaths(want map[string]guardedInvocation) []string {
	paths := make([]string, 0, len(want))
	for path := range want {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	return paths
}
