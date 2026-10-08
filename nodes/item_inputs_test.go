package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// sqliteCatalog is the catalogue a SQLite install compiles against, the one
// the e2e harness runs: every built-in node plus the vector store carrying the
// install's pgvector verdict.
func sqliteCatalog(t *testing.T) *node.Registry {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := nodes.RegisterVectorNodes(catalog, nodes.VectorUnavailableReason("sqlite")); err != nil {
		t.Fatalf("RegisterVectorNodes() error = %v", err)
	}
	return catalog
}

// manualInto is a Manual Trigger wired into one node, the shape a run started
// from the editor or POST /workflows/{id}/run takes.
func manualInto(nodeType string, parameters map[string]any) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_item_inputs", Name: "Item inputs", Settings: map[string]any{},
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "subject", Name: "Subject", Type: nodeType, TypeVersion: workflow.V(1), Parameters: parameters},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"}, Target: workflow.Endpoint{NodeID: "subject", Port: "main"},
		}},
	}
}

func subjectError(t *testing.T, ir workflow.IR, input workflow.Item) error {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	result, _ := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: input})
	for _, run := range result.NodeRuns {
		if run.NodeID == "subject" {
			return run.Error
		}
	}
	t.Fatal("the subject node has no trace row")
	return nil
}

// An Embeddings node with only its model, and an Extract From File node with
// only its operation, are accepted for a run: what they lack — the item's text,
// the item's file — is carried by the items, and a manual run's input or the
// node upstream can supply it. The run then fails on that node by name, which
// is the answer the e2e coverage reads (BUG-x94b3d). The internal vector store
// is still refused before the run on SQLite, where it has nowhere to write.
//
// Until 018af94 the embeddings node was refused at compile on SQLite with the
// pgvector message; that commit made embeddings independent of pgvector and the
// specs went on expecting a refusal no validator had ever given.
func TestNodesThatNeedTheirItemsAreQueuedAndFailTheRunByName(t *testing.T) {
	t.Parallel()

	catalog := sqliteCatalog(t)
	cases := []struct {
		name       string
		nodeType   string
		parameters map[string]any
		want       string
	}{
		{"embeddings without text", nodes.EmbeddingsNodeType, map[string]any{"model": "text-embedding-3-small"}, "carries no text to embed"},
		{"extract without a file", nodes.ExtractFromFileNodeType, map[string]any{"operation": "text"}, "binary storage is not configured"},
	}
	for _, testCase := range cases {
		ir, err := workflow.Compile(manualInto(testCase.nodeType, testCase.parameters), catalog)
		if err != nil {
			t.Fatalf("%s: Compile() error = %v, want the run accepted: the items decide, not the document", testCase.name, err)
		}
		runErr := subjectError(t, ir, workflow.Item{JSON: map[string]any{}})
		if runErr == nil || !strings.Contains(runErr.Error(), testCase.want) {
			t.Fatalf("%s: run error = %v, want it to name %q", testCase.name, runErr, testCase.want)
		}
	}

	// The same embeddings document, run with text in its input, gets past the
	// text: a compile-time refusal would have refused a run that can work.
	ir, err := workflow.Compile(manualInto(nodes.EmbeddingsNodeType, map[string]any{"model": "text-embedding-3-small"}), catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	withText := subjectError(t, ir, workflow.Item{JSON: map[string]any{"text": "hello"}})
	if withText == nil || strings.Contains(withText.Error(), "no text to embed") || !strings.Contains(withText.Error(), "credential") {
		t.Fatalf("embeddings with text in the run input: error = %v, want it past the text and stopped at the missing credential", withText)
	}

	_, err = workflow.Compile(manualInto(nodes.VectorStoreNodeType, map[string]any{"operation": "insert", "collection": "coverage"}), catalog)
	if err == nil || !strings.Contains(err.Error(), "pgvector") {
		t.Fatalf("vector store on SQLite: Compile() error = %v, want the pgvector refusal", err)
	}
}
