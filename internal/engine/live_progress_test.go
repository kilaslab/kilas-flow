package engine_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// A run reports progress while it is still running.
//
// Node rows used to reach storage only in the terminal flush, so a workflow
// that takes minutes showed an empty execution with no trace until it finished
// and nothing at all if it never did. The runner hands each row over as it is
// appended; the writer persists it and publishes its event immediately.
//
// The second half is what keeps the two writers honest: the terminal flush
// writes the same rows again from the result in memory, so a row the live
// writer already persisted must come back as a duplicate — counted once in the
// trace and announced once on the feed — rather than being written and
// published a second time.
func TestNodeProgressIsDurableAndPublishedBeforeTheRunEnds(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-live-progress"}
	blocker := newBlockingNode()
	t.Cleanup(blocker.unblock)
	if err := sandbox.executors.Register("test.lease", engine.ExecutorFunc(blocker.execute)); err != nil {
		t.Fatalf("Register(lease executor) error = %v", err)
	}
	queued := sandbox.queuedLeaseWorkflow(t, tenant, "wf_live_progress")

	broker := events.NewBroker(events.BrokerOptions{SubscriberQueue: 64, BufferPerExecution: 64})
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		Events: broker, WorkerID: "live", DefaultTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	subscription := broker.Subscribe(tenant.ID, queued.ID, 0)
	t.Cleanup(subscription.Close)

	done := make(chan error, 1)
	go func() {
		_, err := service.RunOnce(sandbox.ctx)
		done <- err
	}()
	select {
	case <-blocker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the run never reached its node")
	}

	// The slow node is still holding the run open here.
	during, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(in flight) error = %v", err)
	}
	if got, want := during.Status, execution.StatusRunning; got != want {
		t.Fatalf("execution status while its node runs = %q, want %q", got, want)
	}
	if len(during.NodeRuns) == 0 {
		t.Fatal("no node run is durable while the run is in flight: progress only becomes readable when the execution ends")
	}
	if got, want := during.NodeRuns[0].NodeID, "manual"; got != want {
		t.Errorf("first durable node run = %q, want %q: the completed trigger is already recorded", got, want)
	}
	if !sawNodeEvent(subscription, "manual") {
		t.Error("the completed trigger was never published on the live feed: a subscriber sees nothing until the run ends")
	}

	blocker.unblock()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	final, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := final.Status, execution.StatusSucceeded; got != want {
		t.Fatalf("execution status = %q, want %q", got, want)
	}
	taken := map[string]bool{}
	for _, run := range final.NodeRuns {
		key := fmt.Sprintf("%s|%d|%d", run.NodeID, run.Attempt, run.RunIndex)
		if taken[key] {
			t.Errorf("two rows share the trace key %s: the flush wrote a row the live writer had already stored", key)
		}
		taken[key] = true
	}
	if got, want := len(final.NodeRuns), 2; got != want {
		t.Errorf("node runs = %d, want %d (trigger and node, each once)", got, want)
	}

	// Replaying the retained feed counts what every subscriber of this
	// execution was told: one event per node, not one per writer.
	replay := broker.Subscribe(tenant.ID, queued.ID, 0)
	t.Cleanup(replay.Close)
	published := map[string]int{}
	for _, event := range drainEvents(replay) {
		if event.NodeID == "" {
			continue
		}
		published[event.NodeID]++
	}
	for _, nodeID := range []string{"manual", "slow"} {
		if got, want := published[nodeID], 1; got != want {
			t.Errorf("node %q was published %d times, want %d: the terminal flush announced a row the live writer had already published", nodeID, got, want)
		}
	}
}

// sawNodeEvent waits briefly for one node's event on a live subscription. A
// short wait rather than an instant read: the publish happens on the run's
// goroutine, and this asks from the test's.
func sawNodeEvent(subscription *events.Subscription, nodeID string) bool {
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, open := <-subscription.Events():
			if !open {
				return false
			}
			if event.NodeID == nodeID {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// drainEvents reads everything currently queued for a subscription without
// waiting for more. It is only used after Subscribe replayed a retained
// history, which delivers every event before it returns.
func drainEvents(subscription *events.Subscription) []events.Event {
	collected := make([]events.Event, 0, 8)
	for {
		select {
		case event, open := <-subscription.Events():
			if !open {
				return collected
			}
			collected = append(collected, event)
		default:
			return collected
		}
	}
}
