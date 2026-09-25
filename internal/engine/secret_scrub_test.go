package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// leakySecret is a credential value long enough to be recognised anywhere.
const leakySecret = "QUERY-SECRET-9b2e"

// leakyRun runs Start (three items) into Call, whose executor resolves an
// httpQueryAuth credential and fails with the secret in its error — the shape
// of a transport error that printed the URL it failed on — under the given
// onError, as a node run per item or, with wholeBatch, once for all three.
// It answers the run and its error.
func leakyRun(t *testing.T, onError string, wholeBatch bool, failure func(secret string) error) (engine.Result, error) {
	t.Helper()
	definition := stepType("test.call", "Call")
	definition.WholeBatch = wholeBatch
	catalog := testCatalog(t, startType("test.start", "Start"), definition)
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_leaky", Name: "Leaky",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "test.start", TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Call", Type: "test.call", TypeVersion: workflow.V(1), Settings: map[string]any{"onError": onError}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "call")},
		Settings:    map[string]any{},
	})
	executors := threeItemStart(t, "test.call", func(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		resolved, err := request.Credentials.ResolveCredential(ctx, "cred-1")
		if err != nil {
			return nil, err
		}
		return nil, failure(resolved.Fields["value"])
	})
	return engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
		Credentials: stubCredentials{credential: engine.Credential{
			ID: "cred-1", Name: "Query key", Type: "httpQueryAuth",
			Fields: map[string]string{"name": "api_key", "value": leakySecret},
		}},
	})
}

func transportFailure(secret string) error {
	return fmt.Errorf(`node "Call": Get "http://127.0.0.1:18999/closed?api_key=%s": connection refused`, secret)
}

// A node's error is scrubbed of the secret values of every credential it
// resolved before the runner records it: the row, the run's own error, and
// every error item a tolerated failure hands on to a "notify on failure"
// branch. The secret-free part of the text is kept, so the failure still
// explains itself.
func TestANodeErrorIsScrubbedOfTheCredentialItResolved(t *testing.T) {
	for _, onError := range []string{"stopWorkflow", "continueRegularOutput", "continueErrorOutput"} {
		result, err := leakyRun(t, onError, false, transportFailure)
		if onError == "stopWorkflow" {
			if err == nil {
				t.Fatalf("%s: the run did not fail", onError)
			}
			if strings.Contains(err.Error(), leakySecret) {
				t.Errorf("%s: the run's error %q carries the secret", onError, err)
			}
		} else if err != nil {
			t.Fatalf("%s: Run() error = %v", onError, err)
		}
		runs := nodeRuns(result, "call")
		if len(runs) == 0 {
			t.Fatalf("%s: Call has no row", onError)
		}
		for _, run := range runs {
			if run.Error == nil {
				t.Fatalf("%s: row %#v has no error", onError, run)
			}
			if strings.Contains(run.Error.Error(), leakySecret) {
				t.Errorf("%s: row error %q carries the secret", onError, run.Error)
			}
			if !strings.Contains(run.Error.Error(), "connection refused") {
				t.Errorf("%s: row error %q lost what the failure was", onError, run.Error)
			}
			encoded, _ := json.Marshal(run.Output)
			if strings.Contains(string(encoded), leakySecret) {
				t.Errorf("%s: the error items carry the secret: %s", onError, encoded)
			}
		}
	}
}

// A whole-batch node's per-item failures reach the error items one by one,
// through errors.As rather than through the error's own text, so they are
// scrubbed where they are read out, too.
func TestPerItemOutcomesAreScrubbedToo(t *testing.T) {
	result, err := leakyRun(t, "continueErrorOutput", true, func(secret string) error {
		return engine.ItemOutcomes{
			{Items: []workflow.Item{{JSON: map[string]any{"ok": true}}}},
			{Err: errors.New("second item: " + transportFailure(secret).Error())},
			{Err: &engine.BatchFailure{Err: errors.New("third item leaked " + secret)}},
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	runs := nodeRuns(result, "call")
	encoded, _ := json.Marshal(runs[len(runs)-1].Output)
	if strings.Contains(string(encoded), leakySecret) {
		t.Errorf("the per-item error items carry the secret: %s", encoded)
	}
	if !strings.Contains(string(encoded), "second item") || !strings.Contains(string(encoded), "third item leaked") {
		t.Errorf("the per-item error items lost their messages: %s", encoded)
	}
	if first := runs[len(runs)-1].Error; first == nil || strings.Contains(first.Error(), leakySecret) {
		t.Errorf("row error = %v, want the first failure scrubbed", first)
	}
}

// Scrubbing changes the text and nothing else: a failure that was a deadline
// still reads as one to anything that asks.
func TestAScrubbedErrorKeepsItsCause(t *testing.T) {
	result, _ := leakyRun(t, "stopWorkflow", false, func(secret string) error {
		return fmt.Errorf("waited with %s: %w", secret, context.DeadlineExceeded)
	})
	runs := nodeRuns(result, "call")
	if len(runs) == 0 || !errors.Is(runs[0].Error, context.DeadlineExceeded) {
		t.Fatalf("rows = %#v, want the deadline still reachable through the scrubbed error", runs)
	}
}
