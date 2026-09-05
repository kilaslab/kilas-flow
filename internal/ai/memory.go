package ai

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Retention is the memory contract.
//
// Both bounds are enforced on every read and write, so a conversation cannot
// grow without limit and a dormant session cannot be resurrected months later
// with data nobody expected to still exist.
type Retention struct {
	// MaxMessages keeps the newest N turns of a session. Zero means the
	// default; a negative value is rejected rather than treated as unlimited.
	MaxMessages int
	// MaxAge discards turns older than this. Zero means the default.
	MaxAge time.Duration
}

// DefaultRetention bounds a session to a working window rather than a log.
func DefaultRetention() Retention {
	return Retention{MaxMessages: 40, MaxAge: 24 * time.Hour}
}

func (retention Retention) normalized() (Retention, error) {
	if retention.MaxMessages < 0 || retention.MaxAge < 0 {
		return Retention{}, fmt.Errorf("memory retention bounds must not be negative")
	}
	defaults := DefaultRetention()
	if retention.MaxMessages == 0 {
		retention.MaxMessages = defaults.MaxMessages
	}
	if retention.MaxAge == 0 {
		retention.MaxAge = defaults.MaxAge
	}
	return retention, nil
}

// storedMessage pairs a turn with when it was recorded, which is what lets
// MaxAge be enforced without a separate index.
type storedMessage struct {
	message Message
	at      time.Time
}

// BufferMemory is an in-process bounded conversation store.
//
// It is deliberately not durable: V1's memory is a working window for an agent
// mid-conversation, and persisting every turn would put user content into
// storage the retention contract would then have to police across restarts.
// A durable implementation can replace it behind the Memory interface.
type BufferMemory struct {
	retention Retention
	now       func() time.Time

	mu       sync.Mutex
	sessions map[string][]storedMessage
}

var _ Memory = (*BufferMemory)(nil)

// NewBufferMemory constructs bounded memory. A nil clock uses the system one;
// tests supply their own so age-based expiry is deterministic.
func NewBufferMemory(retention Retention, now func() time.Time) (*BufferMemory, error) {
	normalized, err := retention.normalized()
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &BufferMemory{retention: normalized, now: now, sessions: map[string][]storedMessage{}}, nil
}

// Load returns the retained history for a session.
func (memory *BufferMemory) Load(_ context.Context, session SessionKey) ([]Message, error) {
	if !session.Valid() {
		return nil, fmt.Errorf("memory needs a tenant, workflow, and session")
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()

	retained := memory.prune(session.String())
	messages := make([]Message, 0, len(retained))
	for _, stored := range retained {
		messages = append(messages, stored.message)
	}
	return messages, nil
}

// Append records new turns and re-applies both bounds.
func (memory *BufferMemory) Append(_ context.Context, session SessionKey, messages []Message) error {
	if !session.Valid() {
		return fmt.Errorf("memory needs a tenant, workflow, and session")
	}
	if len(messages) == 0 {
		return nil
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()

	key := session.String()
	at := memory.now().UTC()
	stored := memory.sessions[key]
	for _, message := range messages {
		stored = append(stored, storedMessage{message: message, at: at})
	}
	memory.sessions[key] = stored
	memory.prune(key)
	return nil
}

// Forget drops a session outright, which is what a deletion request needs.
func (memory *BufferMemory) Forget(session SessionKey) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	delete(memory.sessions, session.String())
}

// prune enforces both bounds and returns what survives. The caller holds the
// lock. Applying age first then count means a long-idle session is emptied
// rather than merely trimmed.
func (memory *BufferMemory) prune(key string) []storedMessage {
	stored := memory.sessions[key]
	if len(stored) == 0 {
		delete(memory.sessions, key)
		return nil
	}

	cutoff := memory.now().UTC().Add(-memory.retention.MaxAge)
	fresh := stored[:0:0]
	for _, entry := range stored {
		if entry.at.After(cutoff) {
			fresh = append(fresh, entry)
		}
	}
	if overflow := len(fresh) - memory.retention.MaxMessages; overflow > 0 {
		fresh = fresh[overflow:]
	}
	if len(fresh) == 0 {
		delete(memory.sessions, key)
		return nil
	}
	memory.sessions[key] = fresh
	return fresh
}
