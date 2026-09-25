package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// `$('X').all()`, `.first()` and `.last()` read the output of X that the
// node evaluating them is connected to, as n8n reads it: found by walking
// upstream from the node, through any nodes in between, to the first
// connection out of X. A node that X does not feed reads X's first output.
// `$items('X')` still reads output 0 unless told otherwise.
//
// Gate sends t1 and t2 out of true and f1 out of false. Direct hangs off
// false, Later sits behind Mid on false, and Aside hangs off Start, so X is
// not upstream of it at all.
func TestDollarNodeReadsTheOutputTheNodeIsConnectedTo(t *testing.T) {
	gate := stepType("test.gate", "Gate")
	gate.Outputs = []workflow.Port{{Name: "true", Kind: workflow.ConnectionMain}, {Name: "false", Kind: workflow.ConnectionMain}}
	catalog := testCatalog(t, startType("test.start", "Start"), stepType("test.step", "Step"), gate)
	node := func(id, name, typeID string, y float64) workflow.Node {
		return workflow.Node{ID: id, Name: name, Type: typeID, TypeVersion: workflow.V(1), Position: workflow.Position{X: 200, Y: y}}
	}
	ir := compileDoc(t, catalog, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_node_branch", Name: "Read the connected branch",
		Nodes: []workflow.Node{
			node("start", "Start", "test.start", 0),
			node("gate", "Gate", "test.gate", 0),
			node("direct", "Direct", "test.step", 0),
			node("mid", "Mid", "test.step", 100),
			node("later", "Later", "test.step", 100),
			node("aside", "Aside", "test.step", 900),
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "gate"),
			mainEdge("c2", "gate", "false", "direct"),
			mainEdge("c3", "gate", "false", "mid"),
			mainEdge("c4", "mid", "main", "later"),
			mainEdge("c5", "start", "main", "aside"),
		},
		Settings: map[string]any{},
	})
	templates := []string{
		"{{ $('Gate').all().map(i => i.json.v).join() }}",
		"{{ $('Gate').first().json.v }}",
		"{{ $('Gate').last().json.v }}",
		"{{ $items('Gate').map(i => i.json.v).join() }}",
	}
	read := map[string]string{}
	executors := withExecutors(t, map[string]engine.ExecutorFunc{
		"test.start": func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{}}}}, nil
		},
		"test.gate": func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			item := func(v string) workflow.Item { return workflow.Item{JSON: map[string]any{"v": v}} }
			return workflow.NodeOutput{{item("t1"), item("t2")}, {item("f1")}}, nil
		},
		"test.step": func(_ context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
			context := request.ExpressionContext(input["main"][0], input, 0)
			var values []string
			for _, template := range templates {
				value, err := expression.Evaluate(template, context)
				if err != nil {
					values = append(values, "error: "+err.Error())
					continue
				}
				values = append(values, fmt.Sprint(value))
			}
			read[node.Name] = strings.Join(values, " ")
			return workflow.NodeOutput{input["main"]}, nil
		},
	})
	if _, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{Input: workflow.Item{JSON: map[string]any{}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := map[string]string{
		"Direct": "f1 f1 f1 t1,t2",
		"Later":  "f1 f1 f1 t1,t2",
		"Aside":  "t1,t2 t1 t2 t1,t2",
	}
	for name, reads := range want {
		if read[name] != reads {
			t.Errorf("%s read %q, want %q", name, read[name], reads)
		}
	}
}
