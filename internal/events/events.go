// Package events carries the standardized execution event contract and the
// in-process broker that fans events out to live subscribers.
//
// The contract is deliberately independent of any node type: node progress,
// webhook processing, and AI token traces all arrive on this one channel, so a
// later feature adds an event type rather than a parallel delivery mechanism.
//
// Delivery is best effort by design. Durable execution and node-run records
// are the source of truth; a run must succeed whether or not anyone is
// watching, so nothing here can block or fail a workflow.
package events

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/execution"
)

// Type names one standardized event.
type Type string

const (
	ExecutionStarted   Type = "execution.started"
	ExecutionCompleted Type = "execution.completed"
	ExecutionFailed    Type = "execution.failed"
	ExecutionCancelled Type = "execution.cancelled"
	NodeStarted        Type = "node.started"
	NodeOutput         Type = "node.output"
	NodeCompleted      Type = "node.completed"
	NodeFailed         Type = "node.failed"
	WorkflowSaved      Type = "workflow.saved"
)

// Terminal reports whether an event ends an execution's stream. A live feed
// closes after one, which is what lets a client stop reconnecting.
func (eventType Type) Terminal() bool {
	switch eventType {
	case ExecutionCompleted, ExecutionFailed, ExecutionCancelled:
		return true
	default:
		return false
	}
}

// Event is one standardized occurrence.
//
// Correlation IDs are always present so a consumer can join an event to the
// durable record it describes without parsing Data.
type Event struct {
	// ID is monotonic per execution and is what a reconnecting client sends
	// back as Last-Event-ID.
	ID          uint64           `json:"id"`
	Type        Type             `json:"type"`
	TenantID    string           `json:"-"`
	ExecutionID string           `json:"executionId"`
	WorkflowID  string           `json:"workflowId,omitempty"`
	NodeID      string           `json:"nodeId,omitempty"`
	Status      execution.Status `json:"status,omitempty"`
	Sequence    int              `json:"sequence,omitempty"`
	At          time.Time        `json:"at"`
	// Data carries type-specific detail. It is redacted on publish, so a
	// subscriber can never see credential material even if a node returned it.
	Data json.RawMessage `json:"data,omitempty"`
}

// BrokerOptions tunes fan-out and retention.
type BrokerOptions struct {
	// SubscriberQueue bounds one subscriber's pending events.
	SubscriberQueue int
	// BufferPerExecution bounds the replay history kept per execution.
	BufferPerExecution int
}

const (
	defaultSubscriberQueue    = 64
	defaultBufferPerExecution = 256
)

// Broker fans standardized events out to live subscribers and retains a
// bounded per-execution history for replay after a reconnect.
type Broker struct {
	options BrokerOptions

	mu      sync.Mutex
	streams map[streamKey]*stream
}

type streamKey struct {
	tenantID    string
	executionID string
}

type stream struct {
	lastID  uint64
	history []Event
	// subscribers is keyed by subscription so Close is O(1) and idempotent.
	subscribers map[*Subscription]struct{}
}

// Subscription is one live feed. Events closes after Close, or after the
// stream's terminal event has been delivered.
type Subscription struct {
	events chan Event
	key    streamKey
	broker *Broker

	mu     sync.Mutex
	closed bool
	lagged bool
}

// NewBroker creates an in-process broker.
func NewBroker(options BrokerOptions) *Broker {
	if options.SubscriberQueue <= 0 {
		options.SubscriberQueue = defaultSubscriberQueue
	}
	if options.BufferPerExecution <= 0 {
		options.BufferPerExecution = defaultBufferPerExecution
	}
	return &Broker{options: options, streams: make(map[streamKey]*stream)}
}

// Publish assigns the next ID, retains the event for replay, and delivers it to
// current subscribers.
//
// It never blocks: a subscriber whose queue is full is marked lagged and the
// event is dropped for that subscriber only. The client learns it fell behind
// and can re-read the durable record, which is a better failure than stalling
// the engine behind a browser that stopped reading.
func (broker *Broker) Publish(event Event) {
	if broker == nil || event.ExecutionID == "" {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	event.Data = execution.Redact(event.Data)

	key := streamKey{tenantID: event.TenantID, executionID: event.ExecutionID}

	broker.mu.Lock()
	current, found := broker.streams[key]
	if !found {
		current = &stream{subscribers: make(map[*Subscription]struct{})}
		broker.streams[key] = current
	}
	current.lastID++
	event.ID = current.lastID
	current.history = append(current.history, event)
	if overflow := len(current.history) - broker.options.BufferPerExecution; overflow > 0 {
		current.history = append(current.history[:0], current.history[overflow:]...)
	}
	subscribers := make([]*Subscription, 0, len(current.subscribers))
	for subscriber := range current.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	broker.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.deliver(event)
	}
}

// Subscribe opens a live feed for one execution, replaying every retained event
// after afterID first. Passing 0 replays the whole retained history, which is
// what makes a page opened mid-run — or after it finished — correct.
//
// The tenant is part of the key, so guessing an execution ID from another
// tenant yields an empty feed rather than someone else's data.
func (broker *Broker) Subscribe(tenantID, executionID string, afterID uint64) *Subscription {
	key := streamKey{tenantID: tenantID, executionID: executionID}
	subscription := &Subscription{
		events: make(chan Event, broker.options.SubscriberQueue),
		key:    key,
		broker: broker,
	}

	broker.mu.Lock()
	current, found := broker.streams[key]
	if !found {
		current = &stream{subscribers: make(map[*Subscription]struct{})}
		broker.streams[key] = current
	}
	current.subscribers[subscription] = struct{}{}
	replay := make([]Event, 0, len(current.history))
	for _, event := range current.history {
		if event.ID > afterID {
			replay = append(replay, event)
		}
	}
	broker.mu.Unlock()

	for _, event := range replay {
		subscription.deliver(event)
	}
	return subscription
}

// Forget drops an execution's retained history and closes its subscribers.
// The durable record remains; only the live buffer is released.
func (broker *Broker) Forget(tenantID, executionID string) {
	key := streamKey{tenantID: tenantID, executionID: executionID}

	broker.mu.Lock()
	current, found := broker.streams[key]
	if found {
		delete(broker.streams, key)
	}
	broker.mu.Unlock()

	if !found {
		return
	}
	for subscriber := range current.subscribers {
		subscriber.Close()
	}
}

// Events is the channel a consumer reads. It is closed when the subscription
// is closed.
func (subscription *Subscription) Events() <-chan Event {
	return subscription.events
}

// Lagged reports whether this subscription ever dropped an event because it
// was not reading fast enough.
func (subscription *Subscription) Lagged() bool {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.lagged
}

// Close detaches the subscription and closes its channel. It is safe to call
// more than once, which matters because both the reader and Forget may close.
func (subscription *Subscription) Close() {
	subscription.mu.Lock()
	if subscription.closed {
		subscription.mu.Unlock()
		return
	}
	subscription.closed = true
	close(subscription.events)
	subscription.mu.Unlock()

	broker := subscription.broker
	broker.mu.Lock()
	if current, found := broker.streams[subscription.key]; found {
		delete(current.subscribers, subscription)
	}
	broker.mu.Unlock()
}

func (subscription *Subscription) deliver(event Event) {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	if subscription.closed {
		return
	}
	select {
	case subscription.events <- event:
	default:
		subscription.lagged = true
	}
}
