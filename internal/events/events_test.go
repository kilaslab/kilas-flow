package events_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/events"
)

func TestBrokerDeliversEventsToEverySubscriberOfThatExecution(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{})
	first := broker.Subscribe("tenant-a", "exec-1", 0)
	defer first.Close()
	second := broker.Subscribe("tenant-a", "exec-1", 0)
	defer second.Close()
	other := broker.Subscribe("tenant-a", "exec-2", 0)
	defer other.Close()

	broker.Publish(events.Event{
		TenantID: "tenant-a", ExecutionID: "exec-1", WorkflowID: "wf-1",
		Type: events.ExecutionStarted,
	})

	for name, subscription := range map[string]*events.Subscription{"first": first, "second": second} {
		select {
		case event := <-subscription.Events():
			if event.Type != events.ExecutionStarted || event.ExecutionID != "exec-1" {
				t.Errorf("%s received %#v", name, event)
			}
		case <-time.After(time.Second):
			t.Errorf("%s received nothing", name)
		}
	}

	select {
	case event := <-other.Events():
		t.Errorf("a subscriber of another execution received %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBrokerIsolatesTenants(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{})
	// Same execution ID, different tenant: a guessed ID must not become a feed.
	intruder := broker.Subscribe("tenant-b", "exec-1", 0)
	defer intruder.Close()

	broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.ExecutionStarted})

	select {
	case event := <-intruder.Events():
		t.Fatalf("another tenant received %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBrokerAssignsMonotonicIDsPerExecution(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{})
	subscription := broker.Subscribe("tenant-a", "exec-1", 0)
	defer subscription.Close()

	for range 3 {
		broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeStarted})
	}

	var ids []uint64
	for range 3 {
		select {
		case event := <-subscription.Events():
			ids = append(ids, event.ID)
		case <-time.After(time.Second):
			t.Fatal("event was not delivered")
		}
	}
	if ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("event IDs = %v, want 1,2,3", ids)
	}
}

func TestSubscribeReplaysEventsAfterTheRequestedID(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{BufferPerExecution: 10})
	for range 3 {
		broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeStarted})
	}

	// A browser reconnecting sends Last-Event-ID; it must resume, not restart.
	subscription := broker.Subscribe("tenant-a", "exec-1", 1)
	defer subscription.Close()

	var ids []uint64
	for range 2 {
		select {
		case event := <-subscription.Events():
			ids = append(ids, event.ID)
		case <-time.After(time.Second):
			t.Fatal("replayed event was not delivered")
		}
	}
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("replayed IDs = %v, want 2,3", ids)
	}
}

func TestSubscribeFromZeroReplaysTheWholeRetainedHistory(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{BufferPerExecution: 10})
	broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.ExecutionStarted})
	broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.ExecutionCompleted})

	// A page opened after the run finished still sees what happened.
	subscription := broker.Subscribe("tenant-a", "exec-1", 0)
	defer subscription.Close()

	first := <-subscription.Events()
	second := <-subscription.Events()
	if first.Type != events.ExecutionStarted || second.Type != events.ExecutionCompleted {
		t.Fatalf("replay = %s, %s, want the full history", first.Type, second.Type)
	}
}

// The subscriber queue used to be the replay's ceiling: Subscribe pushed the
// retained history into a 64-slot channel, so a finished run with more events
// than that lost everything after the 64th — its outcome included — and a
// trace of it ended on a reconstructed frame instead of the run's own.
func TestSubscribeReplaysAHistoryLongerThanTheSubscriberQueue(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{SubscriberQueue: 4, BufferPerExecution: 100})
	for range 99 {
		broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeCompleted})
	}
	broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.ExecutionFailed})

	subscription := broker.Subscribe("tenant-a", "exec-1", 0)
	defer subscription.Close()

	var replayed []events.Event
	for drained := false; !drained; {
		select {
		case event := <-subscription.Events():
			replayed = append(replayed, event)
		default:
			drained = true
		}
	}
	if len(replayed) != 100 {
		t.Fatalf("replayed %d events, want the whole retained history of 100", len(replayed))
	}
	for index, event := range replayed {
		if event.ID != uint64(index+1) {
			t.Fatalf("replayed[%d].ID = %d, want %d: the replay is out of order", index, event.ID, index+1)
		}
	}
	if last := replayed[len(replayed)-1]; last.Type != events.ExecutionFailed {
		t.Errorf("last replayed event = %s, want the run's own outcome", last.Type)
	}
	if subscription.Lagged() || subscription.Truncated() {
		t.Errorf("Lagged() = %v, Truncated() = %v, want neither for a replay that fitted", subscription.Lagged(), subscription.Truncated())
	}
}

// A replay that starts after the requested id is missing the events in
// between, and says so rather than looking complete.
func TestSubscribeReportsHistoryThatAgedOutBeforeTheReplay(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{BufferPerExecution: 3})
	for range 10 {
		broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeStarted})
	}

	for _, test := range []struct {
		after     uint64
		truncated bool
	}{
		{after: 0, truncated: true},
		{after: 6, truncated: true},
		{after: 7, truncated: false},
		{after: 10, truncated: false},
	} {
		subscription := broker.Subscribe("tenant-a", "exec-1", test.after)
		if got := subscription.Truncated(); got != test.truncated {
			t.Errorf("Subscribe(after %d).Truncated() = %v, want %v", test.after, got, test.truncated)
		}
		subscription.Close()
	}
}

func TestRetainedHistoryIsBoundedPerExecution(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{BufferPerExecution: 3})
	for range 10 {
		broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeStarted})
	}

	subscription := broker.Subscribe("tenant-a", "exec-1", 0)
	defer subscription.Close()

	var ids []uint64
	for range 3 {
		select {
		case event := <-subscription.Events():
			ids = append(ids, event.ID)
		case <-time.After(time.Second):
			t.Fatal("replayed event was not delivered")
		}
	}
	// Only the newest three survive: an unbounded log would let one long
	// execution exhaust memory.
	if ids[0] != 8 || ids[2] != 10 {
		t.Fatalf("retained IDs = %v, want the newest three", ids)
	}
}

func TestPublishNeverBlocksOnASlowSubscriber(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{SubscriberQueue: 2, BufferPerExecution: 100})
	slow := broker.Subscribe("tenant-a", "exec-1", 0)
	defer slow.Close()

	// The runtime must not be held up by a client that stopped reading. This
	// would deadlock if delivery were synchronous.
	done := make(chan struct{})
	go func() {
		for range 50 {
			broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeStarted})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}
	if !slow.Lagged() {
		t.Error("a subscriber that fell behind was not marked as lagged")
	}
}

func TestCloseStopsDeliveryAndIsIdempotent(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{})
	subscription := broker.Subscribe("tenant-a", "exec-1", 0)
	subscription.Close()
	subscription.Close()

	broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeStarted})

	select {
	case _, open := <-subscription.Events():
		if open {
			t.Error("a closed subscription still received an event")
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("a closed subscription's channel was not closed")
	}
}

func TestTerminalEventsAreIdentifiable(t *testing.T) {
	t.Parallel()

	for _, terminal := range []events.Type{events.ExecutionCompleted, events.ExecutionFailed, events.ExecutionCancelled} {
		if !terminal.Terminal() {
			t.Errorf("%s should be terminal", terminal)
		}
	}
	for _, ongoing := range []events.Type{events.ExecutionStarted, events.NodeStarted, events.NodeCompleted, events.NodeFailed, events.WorkflowSaved} {
		if ongoing.Terminal() {
			t.Errorf("%s should not be terminal", ongoing)
		}
	}
}

func TestPublishRedactsEventPayloads(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{})
	subscription := broker.Subscribe("tenant-a", "exec-1", 0)
	defer subscription.Close()

	broker.Publish(events.Event{
		TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeCompleted,
		Data: json.RawMessage(`{"headers":{"Authorization":"Bearer live-token"}}`),
	})

	event := <-subscription.Events()
	if string(event.Data) == "" {
		t.Fatal("event data was dropped entirely")
	}
	if contains(string(event.Data), "live-token") {
		t.Fatalf("event payload leaked a credential: %s", event.Data)
	}
}

func TestBrokerForgetsAnExecutionOnceItIsDone(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{BufferPerExecution: 10})
	broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.ExecutionStarted})
	broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.ExecutionCompleted})
	broker.Forget("tenant-a", "exec-1")

	subscription := broker.Subscribe("tenant-a", "exec-1", 0)
	defer subscription.Close()

	select {
	case event := <-subscription.Events():
		t.Fatalf("a forgotten execution replayed %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBrokerIsSafeUnderConcurrentUse(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{SubscriberQueue: 8, BufferPerExecution: 32})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var group sync.WaitGroup
	for worker := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			subscription := broker.Subscribe("tenant-a", "exec-1", 0)
			defer subscription.Close()
			for range 20 {
				broker.Publish(events.Event{TenantID: "tenant-a", ExecutionID: "exec-1", Type: events.NodeStarted})
				select {
				case <-subscription.Events():
				case <-ctx.Done():
					return
				default:
				}
			}
			_ = worker
		}()
	}
	group.Wait()
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (func() bool {
		for index := 0; index+len(needle) <= len(haystack); index++ {
			if haystack[index:index+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
