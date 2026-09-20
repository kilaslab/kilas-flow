package ai

import (
	"context"
	"fmt"
	"strings"
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
	// maxSessionsPerTenant bounds how many conversations one tenant may keep.
	// Zero means unbounded. When a write would exceed it, the tenant's
	// least-recently-touched session is evicted first.
	maxSessionsPerTenant int

	mu       sync.Mutex
	sessions map[string][]storedMessage
	touched  map[string]time.Time
}

var _ Memory = (*BufferMemory)(nil)
var _ PolicyMemory = (*BufferMemory)(nil)

// BufferOption carries a deployment decision the memory store needs.
type BufferOption func(*BufferMemory)

// WithPerTenantSessionLimit bounds how many conversations one tenant may
// retain. A non-positive value leaves the store unbounded.
func WithPerTenantSessionLimit(limit int) BufferOption {
	return func(memory *BufferMemory) {
		if limit > 0 {
			memory.maxSessionsPerTenant = limit
		}
	}
}

// NewBufferMemory constructs bounded memory. A nil clock uses the system one;
// tests supply their own so age-based expiry is deterministic.
func NewBufferMemory(retention Retention, now func() time.Time, options ...BufferOption) (*BufferMemory, error) {
	normalized, err := retention.normalized()
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	memory := &BufferMemory{retention: normalized, now: now, sessions: map[string][]storedMessage{}, touched: map[string]time.Time{}}
	for _, option := range options {
		option(memory)
	}
	return memory, nil
}

// Load returns the retained history for a session.
func (memory *BufferMemory) Load(_ context.Context, session SessionKey) ([]Message, error) {
	return memory.loadPolicy(session, memory.retention)
}

// LoadWithPolicy returns the retained history for a session under per-node
// bounds. A zero retention means the memory's own defaults, matching how the
// constructor treats zero.
func (memory *BufferMemory) LoadWithPolicy(_ context.Context, session SessionKey, retention Retention) ([]Message, error) {
	effective, err := retention.normalized()
	if err != nil {
		return nil, err
	}
	return memory.loadPolicy(session, effective)
}

func (memory *BufferMemory) loadPolicy(session SessionKey, retention Retention) ([]Message, error) {
	if !session.Valid() {
		return nil, fmt.Errorf("memory needs a tenant, workflow, and session")
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()

	retained := memory.prune(session.String(), retention)
	if len(retained) > 0 {
		memory.touched[session.String()] = memory.now().UTC()
	}
	messages := make([]Message, 0, len(retained))
	for _, stored := range retained {
		messages = append(messages, stored.message)
	}
	return messages, nil
}

// Append records new turns and re-applies both bounds.
func (memory *BufferMemory) Append(_ context.Context, session SessionKey, messages []Message) error {
	return memory.appendPolicy(session, messages, memory.retention)
}

// AppendWithPolicy records new turns under per-node bounds. A zero retention
// means the memory's own defaults.
func (memory *BufferMemory) AppendWithPolicy(_ context.Context, session SessionKey, messages []Message, retention Retention) error {
	effective, err := retention.normalized()
	if err != nil {
		return err
	}
	return memory.appendPolicy(session, messages, effective)
}

func (memory *BufferMemory) appendPolicy(session SessionKey, messages []Message, retention Retention) error {
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
	memory.touched[key] = at
	memory.prune(key, retention)
	memory.evictBeyondCeiling(session.TenantID)
	return nil
}

// Forget drops a session outright, which is what a deletion request needs.
func (memory *BufferMemory) Forget(session SessionKey) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	memory.drop(session.String())
}

// ForgetWorkflow drops every session of one workflow, for workflow deletion.
func (memory *BufferMemory) ForgetWorkflow(tenantID, workflowID string) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	prefix := tenantID + "\x00" + workflowID + "\x00"
	for key := range memory.sessions {
		if strings.HasPrefix(key, prefix) {
			memory.drop(key)
		}
	}
}

// ForgetTenant drops every session of one tenant, for tenant deletion.
func (memory *BufferMemory) ForgetTenant(tenantID string) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	prefix := tenantID + "\x00"
	for key := range memory.sessions {
		if strings.HasPrefix(key, prefix) {
			memory.drop(key)
		}
	}
}

func (memory *BufferMemory) drop(key string) {
	delete(memory.sessions, key)
	delete(memory.touched, key)
}

// MemoryStats is the observable usage of the store.
type MemoryStats struct {
	// Sessions is the total number of retained conversations.
	Sessions int
	// PerTenant counts retained conversations by tenant.
	PerTenant map[string]int
}

// Stats reports the current usage of the store.
func (memory *BufferMemory) Stats() MemoryStats {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	stats := MemoryStats{PerTenant: map[string]int{}}
	for key := range memory.sessions {
		stats.Sessions++
		stats.PerTenant[tenantOf(key)]++
	}
	return stats
}

func tenantOf(key string) string {
	if index := strings.IndexByte(key, '\x00'); index >= 0 {
		return key[:index]
	}
	return ""
}

// evictBeyondCeiling drops the tenant's least-recently-touched sessions until
// it is back under the ceiling. The caller holds the lock. An unbounded store
// (zero ceiling) never evicts.
func (memory *BufferMemory) evictBeyondCeiling(tenantID string) {
	if memory.maxSessionsPerTenant <= 0 {
		return
	}
	prefix := tenantID + "\x00"
	for {
		oldest := ""
		var oldestAt time.Time
		count := 0
		for key := range memory.sessions {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			count++
			// An untracked session reads as the zero time, which is older
			// than any tracked one — evict the unknown first.
			if touched := memory.touched[key]; oldest == "" || touched.Before(oldestAt) {
				oldest, oldestAt = key, touched
			}
		}
		if count <= memory.maxSessionsPerTenant || oldest == "" {
			return
		}
		memory.drop(oldest)
	}
}

// prune enforces both bounds and returns what survives. The caller holds the
// lock. Applying age first then count means a long-idle session is emptied
// rather than merely trimmed.
func (memory *BufferMemory) prune(key string, retention Retention) []storedMessage {
	stored := memory.sessions[key]
	if len(stored) == 0 {
		memory.drop(key)
		return nil
	}

	cutoff := memory.now().UTC().Add(-retention.MaxAge)
	fresh := stored[:0:0]
	for _, entry := range stored {
		if entry.at.After(cutoff) {
			fresh = append(fresh, entry)
		}
	}
	if overflow := len(fresh) - retention.MaxMessages; overflow > 0 {
		fresh = fresh[overflow:]
	}
	// The count bound is applied, then moved forward to a turn boundary. A
	// tool result whose tool_call the trim removed — or an assistant turn
	// whose tool results it removed — is a window no provider that enforces
	// message order will accept, and the refusal arrives as a 400 in the
	// middle of a conversation that was working.
	fresh = alignToTurnStart(fresh)
	if len(fresh) == 0 {
		memory.drop(key)
		return nil
	}
	memory.sessions[key] = fresh
	return fresh
}

// alignToTurnStart drops leading messages that cannot open a window.
//
// A tool result belongs to the assistant turn that asked for it, and an
// assistant turn asking for tools is only valid when its results follow. A
// window that begins with either is missing the message that made it
// meaningful, which providers that enforce message order reject outright.
func alignToTurnStart(stored []storedMessage) []storedMessage {
	for index, entry := range stored {
		if entry.message.OpensATurn() {
			return stored[index:]
		}
	}
	return nil
}
