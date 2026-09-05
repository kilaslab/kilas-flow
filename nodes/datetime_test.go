package nodes_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// runNode executes one registered node over the given items.
func runNode(t *testing.T, nodeType string, parameters map[string]any, items []workflow.Item) workflow.NodeOutput {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodeType)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := executors.Lookup(definition.ExecutorID)
	if !found {
		t.Fatalf("executor %q is not registered", definition.ExecutorID)
	}
	ir := workflow.IRNode{
		ID: "n1", Name: "Node", Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": items}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

func TestTheDateNodeDoesEachOperationInAnExplicitZone(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		parameters map[string]any
		want       any
	}{
		"add two months lands on the calendar day": {
			parameters: map[string]any{"operation": "addToDate", "date": "2026-01-31T00:00:00Z",
				"duration": float64(2), "unit": "months"},
			want: "2026-03-31T00:00:00Z",
		},
		"subtract seven days": {
			parameters: map[string]any{"operation": "subtractFromDate", "date": "2026-03-08T12:00:00Z",
				"duration": float64(7), "unit": "days"},
			want: "2026-03-01T12:00:00Z",
		},
		"format uses Luxon tokens": {
			parameters: map[string]any{"operation": "formatDate", "date": "2026-09-05T14:30:00Z",
				"format": "cccc d MMMM yyyy 'at' HH:mm"},
			want: "Saturday 5 September 2026 at 14:30",
		},
		// A zoneless date is read in the node's zone; an offset in the value
		// wins, because the sender already said which instant they meant.
		"a zoneless date is read in the configured zone": {
			parameters: map[string]any{"operation": "formatDate", "date": "2026-09-05 09:00:00",
				"timezone": "Asia/Jakarta", "format": "yyyy-MM-dd HH:mm ZZ"},
			want: "2026-09-05 09:00 +07:00",
		},
		"an offset in the value wins over the zone": {
			parameters: map[string]any{"operation": "formatDate", "date": "2026-09-05T00:00:00Z",
				"timezone": "Asia/Jakarta", "format": "yyyy-MM-dd HH:mm"},
			want: "2026-09-05 00:00",
		},
		"round down to the start of the month": {
			parameters: map[string]any{"operation": "roundDate", "date": "2026-09-17T13:45:12Z",
				"roundTo": "months", "roundMode": "roundDown"},
			want: "2026-09-01T00:00:00Z",
		},
		"round up to the start of the next hour": {
			parameters: map[string]any{"operation": "roundDate", "date": "2026-09-17T13:45:12Z",
				"roundTo": "hours", "roundMode": "roundUp"},
			want: "2026-09-17T14:00:00Z",
		},
		"extract the weekday as cron numbers it": {
			parameters: map[string]any{"operation": "extractDate", "date": "2026-09-05T00:00:00Z", "part": "weekday"},
			want:       float64(6),
		},
		"extract the ISO week": {
			parameters: map[string]any{"operation": "extractDate", "date": "2026-09-05T00:00:00Z", "part": "week"},
			want:       float64(36),
		},
		// Signed and second-minus-first, so a future date is positive.
		"compare gives a signed distance": {
			parameters: map[string]any{"operation": "getTimeBetweenDates", "date": "2026-09-05T00:00:00Z",
				"endDate": "2026-09-12T00:00:00Z", "unit": "days"},
			want: float64(7),
		},
		"comparing backwards is negative": {
			parameters: map[string]any{"operation": "getTimeBetweenDates", "date": "2026-09-12T00:00:00Z",
				"endDate": "2026-09-05T00:00:00Z", "unit": "days"},
			want: float64(-7),
		},
		// A Unix timestamp is what half the APIs a workflow talks to return.
		"a unix timestamp in seconds is a date": {
			parameters: map[string]any{"operation": "formatDate", "date": float64(1_767_225_600), "format": "yyyy-MM-dd"},
			want:       "2026-01-01",
		},
		"a unix timestamp in milliseconds is a date": {
			parameters: map[string]any{"operation": "formatDate", "date": float64(1_767_225_600_000), "format": "yyyy-MM-dd"},
			want:       "2026-01-01",
		},
	} {
		t.Run(name, func(t *testing.T) {
			output := runNode(t, nodes.DateTimeNodeType, testCase.parameters, []workflow.Item{{JSON: map[string]any{}}})
			if len(output[0]) != 1 {
				t.Fatalf("output = %#v, want one item", output[0])
			}
			if got := output[0][0].JSON["date"]; got != testCase.want {
				t.Errorf("date = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestTheDateNodeKeepsTheIncomingItemAndNamesItsOutputField(t *testing.T) {
	t.Parallel()

	output := runNode(t, nodes.DateTimeNodeType, map[string]any{
		"operation": "extractDate", "date": "2026-09-05T00:00:00Z", "part": "year",
		"outputField": "meta.year",
	}, []workflow.Item{{JSON: map[string]any{"id": float64(7), "name": "Ada"}}})

	item := output[0][0].JSON
	if item["id"] != float64(7) || item["name"] != "Ada" {
		t.Errorf("item = %#v, want the incoming fields kept", item)
	}
	// Dot notation on, as the Set node has it, so the result nests.
	meta, _ := item["meta"].(map[string]any)
	if meta == nil || meta["year"] != float64(2026) {
		t.Errorf("item = %#v, want the year nested under meta", item)
	}
}

func TestTheDateNodeRefusesAZoneThatDependsOnTheServer(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.DateTimeNodeType, workflow.V(1))

	for name, zone := range map[string]string{
		"a misspelled zone":  "Asia/Jakata",
		"the server's local": "Local",
	} {
		t.Run(name, func(t *testing.T) {
			err := definition.Validate(workflow.Node{
				ID: "n1", Name: "Date", Type: nodes.DateTimeNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"operation": "getCurrentDate", "timezone": zone},
			})
			if err == nil {
				t.Fatalf("Validate() accepted timezone %q", zone)
			}
		})
	}
	// A real zone is accepted, and so is one supplied by an expression, which
	// cannot be checked until the node runs.
	for _, zone := range []any{"Asia/Jakarta", "UTC", map[string]any{"mode": "expression", "value": "{{ $json.zone }}"}} {
		if err := definition.Validate(workflow.Node{
			ID: "n1", Name: "Date", Type: nodes.DateTimeNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{"operation": "getCurrentDate", "timezone": zone},
		}); err != nil {
			t.Errorf("Validate() with timezone %#v = %v, want accepted", zone, err)
		}
	}
}

func TestTheWaitNodePausesAndPassesItsItemsThrough(t *testing.T) {
	t.Parallel()

	items := []workflow.Item{
		{JSON: map[string]any{"id": float64(1)}},
		{JSON: map[string]any{"id": float64(2)}},
	}
	started := time.Now()
	// One wait for the node, not one per item: "wait a moment" said once over
	// two items means one moment, and the per-item reading would be a bug
	// nobody notices until a workflow that used to finish stops finishing.
	output := runNode(t, nodes.WaitNodeType, map[string]any{
		"resume": "timeInterval", "amount": float64(0.15), "unit": "seconds",
	}, items)
	elapsed := time.Since(started)
	if len(output[0]) != 2 || output[0][0].JSON["id"] != float64(1) {
		t.Fatalf("output = %#v, want the items passed through unchanged", output[0])
	}
	if elapsed > 5*time.Second {
		t.Errorf("two items waited %s, which is more than once", elapsed)
	}
}

func TestAWaitLongerThanTheServerAllowsIsRefusedRatherThanHeld(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.WaitNodeType, workflow.V(1))
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup(definition.ExecutorID)

	// The run holds a worker for the whole of the wait, so a two-day pause is
	// refused up front rather than discovered as a timeout two days later.
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"resume": "timeInterval", "amount": float64(2), "unit": "days"},
		Definition: definition,
	}, workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("a two-day wait was accepted")
	}
	if !strings.Contains(err.Error(), nodes.MaxWaitDuration.String()) {
		t.Errorf("error = %v, want the server's limit named", err)
	}
}

func TestAWaitUntilATimeAlreadyPastResumesImmediately(t *testing.T) {
	t.Parallel()

	started := time.Now()
	output := runNode(t, nodes.WaitNodeType, map[string]any{
		"resume": "specificTime", "dateTime": "2020-01-01T00:00:00Z", "timezone": "UTC",
	}, []workflow.Item{{JSON: map[string]any{"id": float64(1)}}})
	// A workflow that waits until 09:00 and runs late is simply already there.
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("a past time held the execution for %s", elapsed)
	}
	if len(output[0]) != 1 {
		t.Fatalf("output = %#v, want the item passed through", output[0])
	}
}

func TestTheWaitNodeRefusesTheModesThatNeedDurableSuspension(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.WaitNodeType, workflow.V(1))

	for _, mode := range []string{"webhook", "form"} {
		err := definition.Validate(workflow.Node{
			ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{"resume": mode},
		})
		if err == nil {
			t.Fatalf("Validate() accepted resume %q", mode)
		}
		// The message has to say what to do instead, or it is only a refusal.
		if !strings.Contains(err.Error(), "Webhook trigger") {
			t.Errorf("error for %q = %v, want the alternative named", mode, err)
		}
	}
}
