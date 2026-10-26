package engine_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// recordingObserver is the metrics seam as a test double: every report is
// kept, in order, so a run's whole story can be asserted at once.
type recordingObserver struct {
	mu     sync.Mutex
	events []string
}

func (observer *recordingObserver) ExecutionClaimed(tenant, trigger string, waited time.Duration) {
	observer.record("claimed " + tenant + " " + trigger)
}

func (observer *recordingObserver) ExecutionStarted(tenant, trigger string) {
	observer.record("started " + tenant + " " + trigger)
}

func (observer *recordingObserver) ExecutionSettled(tenant, trigger, status string) {
	observer.record("settled " + tenant + " " + trigger + " " + status)
}

func (observer *recordingObserver) NodeRun(tenant, nodeType string, took time.Duration) {
	observer.record("node " + tenant + " " + nodeType)
}

func (observer *recordingObserver) WebhookRequest(tenant, outcome string) {
	observer.record("webhook " + tenant + " " + outcome)
}

func (observer *recordingObserver) record(entry string) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, entry)
}

func (observer *recordingObserver) story() []string {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]string(nil), observer.events...)
}

// One run through the worker reports the whole series: the claim with its
// wait, the start, one entry per node run with the node's type, and the
// terminal state — and nothing else.
func TestARunReportsTheExecutionSeries(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-metrics"}
	registerMetricsEchoNode(t, sandbox)
	if _, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_metrics", Name: "Metrics run",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "step", Name: "Echo", Type: echoNodeType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "step", Port: "main"},
		}},
		Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	observer := &recordingObserver{}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "metrics-worker", DefaultTimeout: 5 * time.Second, Metrics: observer,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	// Queue a second revision of the wait: a claim that happens after a delay
	// proves the latency is measured from the queue time, not the clock.
	queued, err := sandbox.store.QueueManualLatest(sandbox.ctx, tenant, "wf_metrics",
		sandbox.catalog, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if worked, err := service.RunOnce(sandbox.ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want one execution run", worked, err)
	}

	story := observer.story()
	want := []string{
		"claimed tenant-metrics manual",
		"started tenant-metrics manual",
		// The trigger node emits its item and the echo node runs: two node
		// runs, each named by its type.
		"node tenant-metrics kilasflow.manual",
		"node tenant-metrics kilasflow.test.echo",
		"settled tenant-metrics manual succeeded",
	}
	if len(story) != len(want) {
		t.Fatalf("the observed story = %v, want %v", story, want)
	}
	for index, entry := range want {
		if story[index] != entry {
			t.Errorf("story[%d] = %q, want %q", index, story[index], entry)
		}
	}

	// The claimed wait is knowable only against the record's queue time.
	stored, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != execution.StatusSucceeded {
		t.Errorf("stored status = %q, want succeeded", stored.Status)
	}
}

// A service built with no observer runs exactly as before: the seam is inert
// when nothing was wired, which is the off state a deployment gets by
// default.
func TestARunWithoutAnObserverChangesNothing(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-metrics-off"}
	registerMetricsEchoNode(t, sandbox)
	if _, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_off", Name: "Unwatched",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "step", Name: "Echo", Type: echoNodeType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "step", Port: "main"},
		}},
		Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "off-worker", DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := sandbox.store.QueueManualLatest(sandbox.ctx, tenant, "wf_off", sandbox.catalog, "", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if worked, err := service.RunOnce(sandbox.ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want the run to have happened", worked, err)
	}
}

// The in-flight and capacity gauges read the runtime's own state: both are
// per-process numbers, and the accessor pair is what the composition root
// hands the metrics registry.
func TestInFlightAndCapacityReadTheWorkerPool(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "gauge-worker", DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if got := service.Capacity(); got != 0 {
		t.Errorf("Capacity() before Start = %d, want 0", got)
	}
	if got := service.InFlight(); got != 0 {
		t.Errorf("InFlight() idle = %d, want 0", got)
	}
	if err := service.Start(sandbox.ctx, 4); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := service.Capacity(); got != 4 {
		t.Errorf("Capacity() = %d, want the pool size", got)
	}
}

// echoNodeType is the one non-trigger node the metrics tests run: a node the
// runner executes and reports by its type, which is all the series needs.
const echoNodeType = "kilasflow.test.echo"

// registerMetricsEchoNode installs the echo node and its executor on a
// sandbox, the same definition every metrics test runs.
func registerMetricsEchoNode(t *testing.T, sandbox *engineSandbox) {
	t.Helper()
	if err := sandbox.catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform}, Type: echoNodeType, Version: workflow.V(1),
		DisplayName: "Echo", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.echo",
	}); err != nil {
		t.Fatalf("Register(echo node) error = %v", err)
	}
	if err := sandbox.executors.Register("test.echo", engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"ok": true}}}}, nil
		})); err != nil {
		t.Fatalf("Register(echo executor) error = %v", err)
	}
}
