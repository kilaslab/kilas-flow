package engine_test

import (
	"context"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// The loop's bookkeeping must not travel on the items.
//
// Split In Batches used to keep its cursor, its iteration count, the items still
// pending and the items it had collected under a `$loop` key stamped onto every
// item it forwarded. They then appeared wherever an item went — a Set
// passthrough, an HTTP request body, stored execution data — and the pending and
// collected lists were copies of items, so the payload grew quadratically with
// the batch's size. n8n's items pass through a loop untouched: the state lives
// in the node's own context, which here is the runner's.
//
// The body below is a passthrough, so the loop's own outputs are the items it
// was handed, and the assertion is the one that matters: nothing on an item the
// source did not put there.
func TestLoopDoesNotWriteItsStateOntoItems(t *testing.T) {
	catalog := node.NewRegistry()
	for _, definition := range []node.Definition{
		{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.looprows", Version: workflow.V(1), DisplayName: "Rows", Category: "Test",
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.looprows",
		},
		{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.loopecho", Version: workflow.V(1), DisplayName: "Echo", Category: "Test",
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.loopecho",
		},
	} {
		if err := catalog.Register(definition); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
	}
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_loop_items", Name: "Loop whose body passes its items through",
		Nodes: []workflow.Node{
			{ID: "src", Name: "Source", Type: "test.looprows", TypeVersion: workflow.V(1)},
			{ID: "loop", Name: "Loop", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "body", Name: "Body", Type: "test.loopecho", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "src", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
				Target: workflow.Endpoint{NodeID: "body", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "body", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
		},
		Settings: map[string]any{},
	}, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	const rows = 3
	executors := engine.NewRegistry()
	if err := executors.Register("test.looprows", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			items := make([]workflow.Item, 0, rows)
			for index := range rows {
				items = append(items, workflow.Item{JSON: map[string]any{"id": float64(index + 1)}})
			}
			return workflow.NodeOutput{items}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	// What a Set carrying its input over, an HTTP request that echoes a body and
	// a Code node returning its items all do.
	if err := executors.Register("test.loopecho", engine.ExecutorFunc(
		func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{loopLeakCopy(input["main"])}, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	inspected := 0
	for _, run := range result.NodeRuns {
		if run.NodeID != "loop" && run.NodeID != "body" {
			continue
		}
		for port, stream := range run.Output {
			for index, item := range stream {
				inspected++
				refuseLoopState(t, run.NodeID, port, index, item.JSON)
				if len(item.JSON) != 1 {
					t.Fatalf("%s port %d item %d = %#v, want the source item alone",
						run.NodeID, port, index, item.JSON)
				}
				if item.JSON["id"] == nil {
					t.Fatalf("%s port %d item %d = %#v, want the id the source wrote",
						run.NodeID, port, index, item.JSON)
				}
			}
		}
	}
	if inspected == 0 {
		t.Fatal("the loop recorded no items to inspect")
	}

	// The body answered with what it was handed, so `done` carries the three
	// source items, in order.
	var done []workflow.Item
	for _, run := range result.NodeRuns {
		if run.NodeID == "loop" {
			done = run.Output[0]
		}
	}
	if len(done) != rows {
		t.Fatalf("done carried %d items, want %d", len(done), rows)
	}
	for index, item := range done {
		if item.JSON["id"] != float64(index+1) {
			t.Errorf("done item %d = %#v, want the source item", index, item.JSON)
		}
	}
}

// refuseLoopState walks a value looking for the key the loop used to stamp onto
// every item it forwarded.
func refuseLoopState(t *testing.T, nodeID string, port, index int, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if stamped, looped := typed["$loop"]; looped {
			t.Fatalf("%s port %d item %d carries $loop = %#v, want the item untouched",
				nodeID, port, index, stamped)
		}
		for _, entry := range typed {
			refuseLoopState(t, nodeID, port, index, entry)
		}
	case []any:
		for _, entry := range typed {
			refuseLoopState(t, nodeID, port, index, entry)
		}
	}
}

// loopLeakCopy snapshots the items an executor was handed, the way a node that
// passes its input through would produce them.
func loopLeakCopy(items []workflow.Item) []workflow.Item {
	copied := make([]workflow.Item, len(items))
	for index, item := range items {
		fields := make(map[string]any, len(item.JSON))
		for key, value := range item.JSON {
			fields[key] = value
		}
		copied[index] = workflow.Item{JSON: fields, Paired: item.Paired}
	}
	return copied
}
