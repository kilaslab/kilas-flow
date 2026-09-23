package nodes

import (
	"context"
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// rootsRequest is a request as the runner builds it for a node three items
// into a run: an upstream Source node produced three items, each stamped with
// its own origin, and the node's input descends from them in a different
// order, which only lineage can recover.
func rootsRequest() (engine.Request, workflow.NodeInput) {
	origin := func(index int) *workflow.PairedItem {
		return &workflow.PairedItem{SourceNodeID: "src", SourcePort: "main", ItemIndex: index}
	}
	source := expression.NodeItem{
		Items:       []map[string]any{{"v": "a"}, {"v": "b"}, {"v": "c"}},
		ItemOrigins: []string{expression.OriginKey("src", "main", 0, 0), expression.OriginKey("src", "main", 0, 1), expression.OriginKey("src", "main", 0, 2)},
		Parameters:  map[string]any{"path": "hook"},
	}
	source.JSON = source.Items[0]
	input := workflow.NodeInput{"main": {
		{JSON: map[string]any{"n": 0}, Paired: origin(2)},
		{JSON: map[string]any{"n": 1}, Paired: origin(0)},
		{JSON: map[string]any{"n": 2}, Paired: origin(1)},
	}}
	request := engine.Request{
		Execution: engine.ExecutionContext{ID: "ex-1", Mode: "manual", ResumeURL: "https://kf.example/resume"},
		Env:       map[string]string{"REGION": "eu"},
		NodeItems: map[string]expression.NodeItem{"Source": source},
		Workflow:  expression.WorkflowContext{ID: "wf-1", Name: "Orders", Active: true},
		RunIndex:  3,
	}
	return request, input
}

// A Code node and a `{{ }}` field of the same node must read the same data:
// the same paired item, the same workflow, the same run index.
func TestTheCodeRootsMatchWhatAnExpressionSees(t *testing.T) {
	request, input := rootsRequest()
	ir := workflow.IRNode{ID: "code", Name: "Code", TypeVersion: workflow.V(2)}
	checks := map[string]string{
		"paired":    "$('Source').item.json.v",
		"legacy":    "$node['Source'].json.v",
		"workflow":  "$workflow.name",
		"execution": "$execution.id",
		"resume":    "$execution.resumeUrl",
		"env":       "$env.REGION",
		"run":       "$runIndex",
		"all":       "$('Source').all().length",
		"params":    "$('Source').params.path",
	}
	source := "return { json: {"
	for key, expr := range checks {
		source += fmt.Sprintf(" %s: %s,", key, expr)
	}
	source += " } }"

	runner := jsrun.NewRunner(jsrun.Options{})
	result, err := runner.Run(context.Background(), jsrun.Task{
		Source: source, Mode: jsrun.ModeEachItem, Items: input["main"], Roots: jsRootsOf(ir, input, request),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for index, item := range input["main"] {
		context := request.ExpressionContext(item, input, index)
		for key, expr := range checks {
			want, err := expression.Evaluate("{{ "+expr+" }}", context)
			if err != nil {
				t.Fatalf("item %d: expression %s: %v", index, expr, err)
			}
			if got := result.Items[index].JSON[key]; fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("item %d: %s = %v in code, %v in an expression", index, expr, got, want)
			}
		}
	}
	// Lineage, not position, decided the pairing.
	if got := result.Items[0].JSON["paired"]; got != "c" {
		t.Errorf("item 0 paired with %v, want c, the item it descends from", got)
	}
}

// The workflow's time zone is the Code node's too: $now, Luxon's DateTime and
// Date's locale formatting all read the zone an expression's $now reads.
func TestTheCodeRunsInTheWorkflowsTimezone(t *testing.T) {
	request, input := rootsRequest()
	request.Workflow.Timezone = "Asia/Jakarta"
	roots := jsRootsOf(workflow.IRNode{Name: "Code"}, input, request)
	if roots.Timezone != "Asia/Jakarta" {
		t.Fatalf("Timezone = %q, want the workflow's", roots.Timezone)
	}
	result, err := jsrun.NewRunner(jsrun.Options{}).Run(context.Background(), jsrun.Task{
		Source: "return [{ json: { now: $now.zoneName, local: DateTime.local(2026, 1, 1).toISO(), date: new Date(Date.UTC(2026, 0, 1)).toLocaleString() } }]",
		Items:  input["main"], Roots: roots,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := result.Items[0].JSON
	for key, want := range map[string]string{"now": "Asia/Jakarta", "local": "2026-01-01T00:00:00.000+07:00", "date": "1/1/2026, 7:00:00 AM"} {
		if got[key] != want {
			t.Errorf("%s = %v, want %s", key, got[key], want)
		}
	}
}

func TestANodeThatHasNotRunIsNamed(t *testing.T) {
	request, input := rootsRequest()
	roots := jsRootsOf(workflow.IRNode{Name: "Code"}, input, request)
	if _, ok := roots.Node("Nowhere"); ok {
		t.Fatal("a node that never ran was found")
	}
	if index, reason := roots.Pair("Nowhere", 0); index != -1 || reason == "" {
		t.Fatalf("Pair() = %d, %q; want -1 and a reason", index, reason)
	}
}
