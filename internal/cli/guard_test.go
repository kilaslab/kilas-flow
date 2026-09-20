package cli

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// runWithVerbs drives run() against an explicit verb list, the way runCLI drives
// Run against the binary's own registry. The guard's tests need a verb that is
// guarded today, and no phase-1 verb is: every `guarded` mark in design §4.2
// belongs to phase 2 or is blocked on another ticket, so the primitive is
// proven against a fixture registered in the test rather than against a verb
// the binary does not have.
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

			code, handled, stdout, stderr := runWithVerbs(t, verbs, Env{Args: tc.args, TTY: tc.tty})
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
