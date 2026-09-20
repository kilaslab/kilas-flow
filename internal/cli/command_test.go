package cli

import (
	"context"
	"flag"
	"net/http"
	"testing"
	"time"
)

func TestSplitVerbPrefersTheLongestRegisteredPrefix(t *testing.T) {
	verbs := []Verb{
		{Path: "workflow get", Summary: "show one workflow"},
		{Path: "workflow get-version", Summary: "show one version"},
		{Path: "workflow list", Summary: "list workflows"},
		{Path: "version", Summary: "print the version"},
	}

	cases := []struct {
		name   string
		args   []string
		want   string
		rest   []string
		wantOK bool
	}{
		{name: "two words then flags", args: []string{"workflow", "get", "--json", "abc"}, want: "workflow get", rest: []string{"--json", "abc"}, wantOK: true},
		{name: "two words alone", args: []string{"workflow", "list"}, want: "workflow list", rest: []string{}, wantOK: true},
		{name: "longest prefix wins", args: []string{"workflow", "get-version", "v1"}, want: "workflow get-version", rest: []string{"v1"}, wantOK: true},
		{name: "one word", args: []string{"version"}, want: "version", rest: []string{}, wantOK: true},
		{name: "leading flag is not a verb", args: []string{"--config", "x"}, wantOK: false},
		{name: "no arguments", args: nil, wantOK: false},
		{name: "unknown verb", args: []string{"kflow"}, wantOK: false},
		{name: "partial prefix is not a verb", args: []string{"workflow"}, wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verb, rest, ok := splitVerb(verbs, tc.args)
			if ok != tc.wantOK {
				t.Fatalf("splitVerb(%q) ok = %v, want %v", tc.args, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if verb.Path != tc.want {
				t.Fatalf("verb = %q, want %q", verb.Path, tc.want)
			}
			if len(rest) != len(tc.rest) {
				t.Fatalf("rest = %q, want %q", rest, tc.rest)
			}
			for i := range rest {
				if rest[i] != tc.rest[i] {
					t.Fatalf("rest = %q, want %q", rest, tc.rest)
				}
			}
		})
	}
}

func TestParseFlagsAcceptsFlagsBeforeAfterAndBetweenPositionals(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantJSON   bool
		wantQuiet  bool
		wantURL    string
		positional []string
	}{
		{
			name:       "flags first",
			args:       []string{"--json", "--url", "http://x.test", "abc"},
			wantJSON:   true,
			wantURL:    "http://x.test",
			positional: []string{"abc"},
		},
		{
			name:       "flags last",
			args:       []string{"abc", "--json", "--url", "http://x.test"},
			wantJSON:   true,
			wantURL:    "http://x.test",
			positional: []string{"abc"},
		},
		{
			name:       "interleaved",
			args:       []string{"abc", "--json", "def", "--url", "http://x.test", "ghi"},
			wantJSON:   true,
			wantURL:    "http://x.test",
			positional: []string{"abc", "def", "ghi"},
		},
		{
			name:       "double dash ends flag parsing",
			args:       []string{"--quiet", "--", "--json"},
			wantQuiet:  true,
			positional: []string{"--json"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			flags := registerGlobalFlags(fs)
			positional, err := parseFlags(fs, flags, tc.args)
			if err != nil {
				t.Fatalf("parseFlags(%q): %v", tc.args, err)
			}
			if flags.JSON != tc.wantJSON {
				t.Fatalf("JSON = %v, want %v", flags.JSON, tc.wantJSON)
			}
			if flags.Quiet != tc.wantQuiet {
				t.Fatalf("Quiet = %v, want %v", flags.Quiet, tc.wantQuiet)
			}
			if flags.URL != tc.wantURL {
				t.Fatalf("URL = %q, want %q", flags.URL, tc.wantURL)
			}
			if len(positional) != len(tc.positional) {
				t.Fatalf("positional = %q, want %q", positional, tc.positional)
			}
			for i := range positional {
				if positional[i] != tc.positional[i] {
					t.Fatalf("positional = %q, want %q", positional, tc.positional)
				}
			}
		})
	}
}

func TestGlobalFlagsCarryTheDocumentedDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := registerGlobalFlags(fs)
	if _, err := parseFlags(fs, flags, nil); err != nil {
		t.Fatalf("parseFlags(nil): %v", err)
	}
	if flags.Timeout != 30*time.Second {
		t.Fatalf("default --timeout = %v, want 30s", flags.Timeout)
	}
}

func TestRegistryIsWellFormed(t *testing.T) {
	verbs := registry()
	if len(verbs) == 0 {
		t.Fatal("the registry is empty")
	}

	seen := make(map[string]bool, len(verbs))
	for _, verb := range verbs {
		if verb.Path == "" {
			t.Fatal("a verb has no path")
		}
		if seen[verb.Path] {
			t.Fatalf("verb path %q is registered twice", verb.Path)
		}
		seen[verb.Path] = true
		if verb.Summary == "" {
			t.Fatalf("verb %q has no summary, so `help` would print a blank line", verb.Path)
		}
		if verb.Run == nil {
			t.Fatalf("verb %q has no Run", verb.Path)
		}
	}

	for _, path := range []string{"serve", "version", "health", "ready", "help"} {
		if !seen[path] {
			t.Fatalf("verb %q is not registered", path)
		}
	}

	// A guarded verb without a refusal message would refuse with nothing to say.
	for _, verb := range verbs {
		if verb.Guarded && verb.Refusal == "" {
			t.Fatalf("guarded verb %q has no refusal message", verb.Path)
		}
	}
}

func TestServeVerbSignalsTheServerPath(t *testing.T) {
	verb, _, ok := splitVerb(registry(), []string{"serve"})
	if !ok {
		t.Fatal("serve is not registered")
	}
	if err := verb.Run(nil, nil); err != errServeRequested {
		t.Fatalf("serve Run = %v, want errServeRequested", err)
	}
}

// TestPhaseOneCommandTree pins the whole phase-1 tree: exactly these verbs,
// each with the operation id it drives.
//
// It is the complete assertion the stage-3 test deliberately deferred, and it
// is what makes the "one verb per operation" rule checkable: a verb renamed, a
// verb registered with the wrong operation, or a verb the phase was supposed to
// have and does not, fails here. The presence of `serve` is asserted too — it
// is the server path, not a CLI verb, but it is registered so `help` lists it
// and an unknown-word check cannot mistake it for a typo.
func TestPhaseOneCommandTree(t *testing.T) {
	want := map[string]string{
		"serve":                   "",
		"version":                 "",
		"health":                  "get-health",
		"ready":                   "get-ready",
		"help":                    "",
		"context":                 "",
		"api":                     "",
		"auth login":              "",
		"auth logout":             "",
		"auth whoami":             "get-me",
		"workflow list":           "list-workflows",
		"workflow get":            "get-workflow",
		"workflow create":         "create-workflow",
		"workflow versions":       "list-workflow-versions",
		"workflow get-version":    "get-workflow-version",
		"workflow publish-events": "list-workflow-publish-events",
		"workflow export":         "export-workflow",
		"workflow diagnostics":    "workflow-diagnostics",
		"run":                     "run-workflow",
		"exec list":               "list-executions",
		"exec get":                "get-execution",
		"exec trace":              "stream-execution-events",
		"exec tail":               "stream-execution-events",
		"exec cancel":             "cancel-execution",
		"node list":               "list-node-types",
		"node describe":           "list-node-types",
		"node options":            "load-node-property-options",
		"credential list":         "list-credentials",
		"credential get":          "get-credential",
		"credential test":         "test-credential",
		"datastore list":          "list-datastores",
		"datastore get":           "get-datastore",
		"datastore rows":          "list-datastore-rows",
		"datastore export":        "export-datastore-rows",
		"schedule list":           "list-schedules",
		"tenant list":             "list-tenants",
		"tenant get":              "get-tenant",
		"tenant users":            "list-tenant-users",
		"pack validate":           "",
	}

	// local are the verbs whose Operation names no API call: they either need
	// no server at all or reach several operations, so the served-operation
	// check below must not look for them in the document.
	local := map[string]bool{
		"serve": true, "version": true, "help": true, "context": true,
		"api": true, "auth login": true, "auth logout": true, "pack validate": true,
	}

	registered := make(map[string]Verb, len(want))
	for _, verb := range registry() {
		registered[verb.Path] = verb
	}

	for path, operation := range want {
		verb, present := registered[path]
		if !present {
			t.Errorf("verb %q is not registered", path)

			continue
		}
		if verb.Operation != operation {
			t.Errorf("verb %q drives %q, want %q", path, verb.Operation, operation)
		}
	}
	for path := range registered {
		if _, expected := want[path]; !expected {
			t.Errorf("verb %q is registered but is not part of the phase-1 tree", path)
		}
	}

	// The one operation two verbs share: trace collects it and tail streams it,
	// and both name the API operation they call. `node list` and `node describe`
	// are the mirror image — two views of one catalogue, which is the reverse
	// of the one-verb-per-operation rule and is why they are asserted together.
	if registered["exec trace"].Operation != registered["exec tail"].Operation {
		t.Errorf("exec trace drives %q, exec tail drives %q, want the same operation",
			registered["exec trace"].Operation, registered["exec tail"].Operation)
	}
	if registered["node list"].Operation != registered["node describe"].Operation {
		t.Errorf("node list drives %q, node describe drives %q, want the same operation",
			registered["node list"].Operation, registered["node describe"].Operation)
	}

	// A verb that names an operation must name one the server actually serves:
	// an id the document does not hold would send the verb to the SPA catch-all,
	// which answers 200 HTML and no exit code can distinguish from an answer.
	srv := bootContractServer(t)
	client := &Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 30 * time.Second}}

	index, err := client.Operations(context.Background())
	if err != nil {
		t.Fatalf("read the served operation index: %v", err)
	}
	for path, operation := range want {
		if local[path] || operation == "" {
			continue
		}
		if _, served := index[operation]; !served {
			t.Errorf("verb %q drives %q, which the running server does not serve", path, operation)
		}
	}
	if len(index) == 0 {
		t.Fatal("the served document holds no operations")
	}
	t.Logf("the phase-1 tree holds %d verbs; the server serves %d operations", len(want), len(index))
}
