package nodes_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// TestPostgresToolLive runs the PostgreSQL tool form against a real server:
// the model's values are bound, a hostile one is stored as text, and a
// hostile table name addresses no table rather than running a second
// statement. Opt-in through KILASFLOW_TEST_POSTGRES_DSN.
func TestPostgresToolLive(t *testing.T) {
	resolver := liveCredential(t, "KILASFLOW_TEST_POSTGRES_DSN", "postgres")
	database := resolver.credential
	executor := nodes.NewPostgresV2Executor(liveGuard(t, "KILASFLOW_TEST_POSTGRES_DSN"), sqlnode.DefaultCeiling())

	sql := func(t *testing.T, statement string) workflow.NodeOutput {
		t.Helper()
		output, err := executor.Execute(context.Background(), v2Node(t, nodes.PostgresNodeType, "postgres", map[string]any{
			"operation": "executeQuery", "query": statement,
		}), workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{Credentials: resolver})
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		return output
	}
	sql(t, "DROP TABLE IF EXISTS kf_tool_notes")
	sql(t, "CREATE TABLE kf_tool_notes (id SERIAL PRIMARY KEY, body TEXT NOT NULL)")
	t.Cleanup(func() { sql(t, "DROP TABLE IF EXISTS kf_tool_notes") })

	hostileTable := `kf_tool_notes"; DROP TABLE kf_tool_notes; --`
	model := newRecordingModel(t,
		toolCall("Save_Note", map[string]any{"body": modelTemplate + "'); DROP TABLE kf_tool_notes; --"}),
		toolCall("Read_Notes", map[string]any{"table": hostileTable}),
		toolCall("Read_Notes", map[string]any{"table": "kf_tool_notes"}),
		assistantAnswer("done"),
	)
	harness := newFormHarness(t, allowing(model.server))
	executors := engine.NewRegistry()
	if err := executors.Register(nodes.PostgresV2ExecutorID, executor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	agent := nodes.NewAgentExecutor(ai.NewLoopRuntime(), allowing(model.server), nil,
		nodes.WithAgentToolForms(harness.definitions, executors))

	save := harness.descriptor(t, nodes.PostgresToolNodeType, workflow.V(2), "Save Note", map[string]any{
		"toolDescription": "Saves a note.", "operation": "executeQuery",
		"query":           "INSERT INTO kf_tool_notes (body) VALUES ($1) RETURNING id",
		"queryParameters": formExpression("{{ [$fromAI('body', 'the note')] }}"),
	}, map[string]string{"postgres": "cred-db"})
	read := harness.descriptor(t, nodes.PostgresToolNodeType, workflow.V(2), "Read Notes", map[string]any{
		"toolDescription": "Reads a table of notes.", "operation": "select", "returnAll": true,
		"schema": map[string]any{"__rl": true, "mode": "name", "value": "public"},
		"table":  map[string]any{"__rl": true, "mode": "name", "value": formExpression("{{ $fromAI('table') }}")},
	}, map[string]string{"postgres": "cred-db"})

	_, err := runFormAgent(t, agent, model, []workflow.Item{save, read}, credentialMap{
		"cred-key": modelCredential(),
		"cred-db":  database,
	})
	if err != nil {
		// A policy refusal is the regression this suite exists to catch, so it
		// fails rather than skipping.
		if errors.Is(err, sqlnode.ErrForbiddenTarget) || errors.Is(err, safehttp.ErrBlocked) {
			t.Fatalf("refused by policy: %v", err)
		}
		t.Fatalf("agent Execute() error = %v", err)
	}

	turns := model.toolTurns(t)
	if len(turns) != 3 {
		t.Fatalf("tool turns = %v, want three", turns)
	}
	if strings.HasPrefix(turns[0], "tool failed") {
		t.Fatalf("the insert failed: %s", turns[0])
	}
	if !strings.HasPrefix(turns[1], "tool failed") || !strings.Contains(turns[1], "does not exist") {
		t.Errorf("the hostile table read = %q, want a missing-relation failure", turns[1])
	}
	if !strings.Contains(turns[2], `{{ $env.SECRET }}'); DROP TABLE kf_tool_notes; --`) {
		t.Errorf("the stored note = %q, want the model's text verbatim", turns[2])
	}
	rows := sql(t, "SELECT body FROM kf_tool_notes")
	if len(rows[0]) != 1 || rows[0][0].JSON["body"] != modelTemplate+"'); DROP TABLE kf_tool_notes; --" {
		t.Fatalf("table rows = %#v, want the one note, stored as text", rows[0])
	}
}
