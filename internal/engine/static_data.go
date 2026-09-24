package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ErrStaticDataTooLarge reports a change that would grow a workflow's static
// data past its limit. The change is not kept.
var ErrStaticDataTooLarge = errors.New("workflow static data would grow past its limit")

// StaticData is one execution's view of its workflow's static data: the small
// JSON document n8n's $getWorkflowStaticData reads and writes, which a
// workflow keeps from one run to the next. The document holds one object per
// entry, "global" for the workflow and "node:<name>" for each node that keeps
// its own, which is n8n's own layout.
//
// The document is loaded once per execution, the first time a node asks for
// it, and every node run in the execution sees what the ones before it
// wrote. Whether it is saved is the service's decision, after the run: only
// when the execution succeeded, was not a manual run, and changed it.
type StaticData struct {
	mu      sync.Mutex
	load    func(context.Context) (json.RawMessage, error)
	loaded  bool
	entries map[string]json.RawMessage
	changed bool
}

// NewStaticData builds an execution's handle over load, which reads the
// stored document (nil when there is none yet). A nil load starts empty: the
// data then lives only as long as the execution.
func NewStaticData(load func(context.Context) (json.RawMessage, error)) *StaticData {
	return &StaticData{load: load}
}

// ensure loads the document, once.
func (data *StaticData) ensure(ctx context.Context) error {
	if data.loaded {
		return nil
	}
	data.entries = map[string]json.RawMessage{}
	if data.load != nil {
		stored, err := data.load(ctx)
		if err != nil {
			return fmt.Errorf("load the workflow static data: %w", err)
		}
		if len(stored) > 0 && string(stored) != "null" {
			if err := json.Unmarshal(stored, &data.entries); err != nil {
				return fmt.Errorf("the stored workflow static data does not decode: %w", err)
			}
		}
	}
	data.loaded = true
	return nil
}

// Get returns one entry as a JSON object, {} when it has none.
func (data *StaticData) Get(ctx context.Context, key string) (json.RawMessage, error) {
	data.mu.Lock()
	defer data.mu.Unlock()
	if err := data.ensure(ctx); err != nil {
		return nil, err
	}
	if entry, ok := data.entries[key]; ok {
		return entry, nil
	}
	return json.RawMessage(`{}`), nil
}

// Set replaces entries with what a node run left in them, each a JSON
// object. When the whole document would be larger than limit bytes it
// refuses with ErrStaticDataTooLarge and keeps nothing.
func (data *StaticData) Set(ctx context.Context, changes map[string]json.RawMessage, limit int) error {
	data.mu.Lock()
	defer data.mu.Unlock()
	if err := data.ensure(ctx); err != nil {
		return err
	}
	next := make(map[string]json.RawMessage, len(data.entries)+len(changes))
	for key, entry := range data.entries {
		next[key] = entry
	}
	changed := false
	for key, entry := range changes {
		var compact bytes.Buffer
		if err := json.Compact(&compact, entry); err != nil {
			return fmt.Errorf("workflow static data %q is not JSON: %w", key, err)
		}
		current, had := next[key]
		if !had {
			current = json.RawMessage(`{}`)
		}
		if bytes.Equal(current, compact.Bytes()) {
			continue
		}
		changed = true
		if compact.String() == "{}" {
			// An emptied entry is no entry at all, as one never written is.
			delete(next, key)
			continue
		}
		next[key] = json.RawMessage(compact.Bytes())
	}
	if !changed {
		return nil
	}
	document, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(document) > limit {
		return fmt.Errorf("%w: it would be %d bytes, more than the %d allowed", ErrStaticDataTooLarge, len(document), limit)
	}
	data.entries, data.changed = next, true
	return nil
}

// Changed returns the document to save and whether any run changed it.
func (data *StaticData) Changed() (json.RawMessage, bool) {
	data.mu.Lock()
	defer data.mu.Unlock()
	if !data.changed {
		return nil, false
	}
	document, err := json.Marshal(data.entries)
	if err != nil {
		return nil, false
	}
	return document, true
}
