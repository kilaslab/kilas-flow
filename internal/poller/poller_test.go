package poller_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

type memoryCursors struct {
	due      []repository.DuePoll
	complete []string
}

func (store *memoryCursors) ClaimDue(context.Context, time.Time, string, time.Duration) ([]repository.DuePoll, error) {
	if len(store.due) == 0 {
		return nil, nil
	}
	next := store.due[0]
	store.due = store.due[1:]
	return []repository.DuePoll{next}, nil
}

func (store *memoryCursors) Complete(_ context.Context, id, cursor string, _ time.Time, _ string) error {
	store.complete = append(store.complete, id+":"+cursor)
	return nil
}

type memoryVersions struct {
	document workflow.Document
}

func (store memoryVersions) GetVersionByID(context.Context, repository.TenantScope, string, string) (workflow.Version, error) {
	return workflow.Version{Document: store.document}, nil
}

type onceHandler struct {
	items []workflow.Item
}

func (handler onceHandler) Poll(context.Context, poller.Claim) ([]workflow.Item, string, error) {
	return handler.items, "next", nil
}

func TestTickQueuesOneExecutionPerItemAndAdvancesTheCursor(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		Nodes: []workflow.Node{{
			ID: "gmail", Name: "Gmail Trigger", Type: "kilasflow.gmailTrigger", TypeVersion: workflow.V(1),
		}},
	}
	queued := 0
	service, err := poller.New(poller.Options{
		Cursors: &memoryCursors{due: []repository.DuePoll{{
			Cursor: repository.PollCursor{
				ID: "poll-1", TenantID: "t", WorkflowID: "wf", NodeID: "gmail",
				NodeType: "kilasflow.gmailTrigger", Interval: time.Minute,
			},
			WorkflowVersionID: "ver",
		}}},
		Versions: memoryVersions{document: document},
		Handlers: map[string]poller.Handler{
			"kilasflow.gmailTrigger": onceHandler{items: []workflow.Item{
				{JSON: map[string]any{"id": "m1"}},
				{JSON: map[string]any{"id": "m2"}},
			}},
		},
		Queue: func(context.Context, string, string, string, string, json.RawMessage) error {
			queued++
			return nil
		},
		Request:  func(string) engine.Request { return engine.Request{} },
		WorkerID: "test",
		Clock:    func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	count, err := service.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if count != 2 || queued != 2 {
		t.Fatalf("queued %d (return %d), want 2", queued, count)
	}
}

func TestTickKeepsTheOldCursorWhenQueueFails(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		Nodes: []workflow.Node{{
			ID: "gmail", Name: "Gmail Trigger", Type: "kilasflow.gmailTrigger", TypeVersion: workflow.V(1),
		}},
	}
	cursors := &memoryCursors{due: []repository.DuePoll{{
		Cursor: repository.PollCursor{
			ID: "poll-1", TenantID: "t", WorkflowID: "wf", NodeID: "gmail",
			NodeType: "kilasflow.gmailTrigger", Interval: time.Minute, Cursor: "old",
		},
		WorkflowVersionID: "ver",
	}}}
	service, err := poller.New(poller.Options{
		Cursors:  cursors,
		Versions: memoryVersions{document: document},
		Handlers: map[string]poller.Handler{
			"kilasflow.gmailTrigger": onceHandler{items: []workflow.Item{
				{JSON: map[string]any{"id": "m1"}},
			}},
		},
		Queue: func(context.Context, string, string, string, string, json.RawMessage) error {
			return errors.New("queue down")
		},
		Request:  func(string) engine.Request { return engine.Request{} },
		WorkerID: "test",
		Clock:    func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	count, err := service.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if count != 0 {
		t.Fatalf("queued %d, want 0", count)
	}
	if len(cursors.complete) != 1 || cursors.complete[0] != "poll-1:old" {
		t.Fatalf("complete = %v, want the old cursor retained", cursors.complete)
	}
}
