package nodes_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
			want: "2026-09-05 07:00",
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
		// n8n answers a duration object keyed by unit, so a downstream
		// `{{ $json.timeDifference.days }}` resolves rather than failing.
		"compare gives a signed distance": {
			parameters: map[string]any{"operation": "getTimeBetweenDates", "date": "2026-09-05T00:00:00Z",
				"endDate": "2026-09-12T00:00:00Z", "unit": "days"},
			want: map[string]any{"days": float64(7)},
		},
		"comparing backwards is negative": {
			parameters: map[string]any{"operation": "getTimeBetweenDates", "date": "2026-09-12T00:00:00Z",
				"endDate": "2026-09-05T00:00:00Z", "unit": "days"},
			want: map[string]any{"days": float64(-7)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			output := runNode(t, nodes.DateTimeNodeType, testCase.parameters, []workflow.Item{{JSON: map[string]any{}}})
			if len(output[0]) != 1 {
				t.Fatalf("output = %#v, want one item", output[0])
			}
			got := output[0][0].JSON["date"]
			// A duration object compares by its JSON encoding rather than by
			// Go equality: test maps and expected maps are different values
			// with the same content.
			if wantMap, ok := testCase.want.(map[string]any); ok {
				gotMap, _ := got.(map[string]any)
				if len(gotMap) != len(wantMap) {
					t.Fatalf("date = %#v, want %#v", got, testCase.want)
				}
				for key, want := range wantMap {
					if gotMap[key] != want {
						t.Errorf("date[%q] = %#v, want %#v", key, gotMap[key], want)
					}
				}
				return
			}
			if got != testCase.want {
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

func TestTheWaitNodeSuspendsInsteadOfHoldingAWorker(t *testing.T) {
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

	items := []workflow.Item{
		{JSON: map[string]any{"id": float64(1)}},
		{JSON: map[string]any{"id": float64(2)}},
	}
	before := time.Now()
	// One suspension for the node, not one per item: the expiry is a single
	// instant computed once, and the per-item reading would be a bug nobody
	// notices until a workflow that used to finish stopped finishing.
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"resume": "timeInterval", "amount": float64(0.15), "unit": "seconds"},
		Definition: definition,
	}, workflow.NodeInput{"main": items}, engine.Request{})
	var suspended *engine.SuspendError
	if !errors.As(err, &suspended) {
		t.Fatalf("Execute() error = %v, want suspension", err)
	}
	if suspended.Mode != engine.WaitModeInterval {
		t.Errorf("suspension mode = %q, want %q", suspended.Mode, engine.WaitModeInterval)
	}
	if until := suspended.ExpiresAt.Sub(before); until < 100*time.Millisecond || until > 5*time.Second {
		t.Errorf("suspension expires in %s, want about 0.15 seconds", until)
	}

	// A pause of zero passes straight through with no suspension.
	output := runNode(t, nodes.WaitNodeType, map[string]any{
		"resume": "timeInterval", "amount": float64(0), "unit": "seconds",
	}, items)
	if len(output[0]) != 2 || output[0][0].JSON["id"] != float64(1) {
		t.Fatalf("output = %#v, want the items passed through unchanged", output[0])
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

	// A two-day pause is what the durable suspension exists for: it parks in
	// storage and costs no worker while it waits, so the old one-hour ceiling
	// refused exactly the waits people write.
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"resume": "timeInterval", "amount": float64(2), "unit": "days"},
		Definition: definition,
	}, workflow.NodeInput{}, engine.Request{})
	var suspended *engine.SuspendError
	if !errors.As(err, &suspended) {
		t.Fatalf("a two-day wait was not suspended: error = %v", err)
	}
	if suspended.Mode != engine.WaitModeInterval {
		t.Errorf("suspension mode = %q, want %q", suspended.Mode, engine.WaitModeInterval)
	}

	// Past the ceiling the wait still fails up front, with the limit named: a
	// wait that cannot resolve is a leak, not a pause.
	_, err = executor.Execute(context.Background(), workflow.IRNode{
		ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"resume": "timeInterval", "amount": float64(30), "unit": "days"},
		Definition: definition,
	}, workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("a thirty-day wait was accepted")
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

func TestTheWaitNodeSuspendsOnTheModesThatWaitForACall(t *testing.T) {
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
	items := []workflow.Item{{JSON: map[string]any{"id": float64(1)}}}

	suspend := func(parameters map[string]any) *engine.SuspendError {
		t.Helper()
		_, err := executor.Execute(context.Background(), workflow.IRNode{
			ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Definition: definition,
		}, workflow.NodeInput{"main": items}, engine.Request{})
		var suspended *engine.SuspendError
		if !errors.As(err, &suspended) {
			t.Fatalf("Execute(%v) error = %v, want a suspension", parameters["resume"], err)
		}
		return suspended
	}

	// A webhook wait with no limit is held as a webhook wait: it ends when
	// something calls the resume URL, and the engine's own wait lifetime is
	// what stops it outliving everyone's memory of it.
	webhook := suspend(map[string]any{"resume": "webhook"})
	if webhook.Mode != engine.WaitModeWebhook {
		t.Errorf("webhook mode = %q, want %q", webhook.Mode, engine.WaitModeWebhook)
	}
	if !webhook.ExpiresAt.IsZero() {
		t.Errorf("webhook expiry = %s, want the engine's default lifetime applied at the service boundary", webhook.ExpiresAt)
	}

	// A form wait is this installation's approval page: a human approves or
	// rejects, and the run continues with that decision.
	form := suspend(map[string]any{"resume": "form"})
	if form.Mode != engine.WaitModeApproval {
		t.Errorf("form mode = %q, want %q", form.Mode, engine.WaitModeApproval)
	}

	// With a limit it is held as a timer instead, because the limit is what
	// resumes it: a call may still end it early, but nothing arriving is no
	// longer a failure the way an unanswered approval is.
	limited := suspend(map[string]any{
		"resume": "webhook", "limitWaitTime": true, "limitType": "afterTimeInterval",
		"limitAmount": float64(30), "limitUnit": "minutes",
	})
	if limited.Mode != engine.WaitModeInterval {
		t.Errorf("limited webhook mode = %q, want %q", limited.Mode, engine.WaitModeInterval)
	}
	if until := time.Until(limited.ExpiresAt); until < 29*time.Minute || until > 31*time.Minute {
		t.Errorf("limited webhook expires in %s, want about 30 minutes", until)
	}

	// The same limit written as a clock time is an absolute one.
	at := suspend(map[string]any{
		"resume": "webhook", "limitWaitTime": true, "limitType": "atSpecifiedTime",
		"limitAt": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
	if at.Mode != engine.WaitModeUntil {
		t.Errorf("at-specified-time limit mode = %q, want %q", at.Mode, engine.WaitModeUntil)
	}

	// A limit already past resumes the node immediately, exactly as a pause of
	// zero does: there is nothing left to wait for.
	if _, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"resume": "webhook", "limitWaitTime": true, "limitType": "atSpecifiedTime",
			"limitAt": "2020-01-01T00:00:00Z",
		},
		Definition: definition,
	}, workflow.NodeInput{"main": items}, engine.Request{}); err != nil {
		t.Fatalf("Execute(past limit) error = %v, want the items passed through", err)
	}

	// Every mode above must validate: the node's own validation is what the
	// editor shows before a workflow is activated.
	for _, parameters := range []map[string]any{
		{"resume": "webhook"},
		{"resume": "form"},
		{"resume": "webhook", "limitWaitTime": true, "limitType": "afterTimeInterval", "limitAmount": float64(1), "limitUnit": "hours"},
		{"resume": "webhook", "limitWaitTime": true, "limitType": "atSpecifiedTime", "limitAt": "2026-01-01T00:00:00Z"},
	} {
		if err := definition.Validate(workflow.Node{ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1), Parameters: parameters}); err != nil {
			t.Errorf("Validate(%v) error = %v", parameters, err)
		}
	}
	// A limit at a specified time with no time is the one shape worth refusing
	// before it runs: the wait would otherwise never end on its own.
	err := definition.Validate(workflow.Node{
		ID: "n1", Name: "Wait", Type: nodes.WaitNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"resume": "webhook", "limitWaitTime": true, "limitType": "atSpecifiedTime"},
	})
	if err == nil {
		t.Error("Validate() accepted a limit at a specified time with no time")
	}
}
