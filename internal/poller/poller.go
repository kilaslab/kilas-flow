package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// TickResolution is how often due polls are looked for by default.
const TickResolution = 15 * time.Second

// DefaultLease is how long one worker owns a poll tick. It is longer than
// DefaultBudget so a tick still running cannot be claimed by a replica.
const DefaultLease = 3 * time.Minute

// DefaultBudget is how long one poll may spend fetching remote items.
const DefaultBudget = 2 * time.Minute

// Handler fetches new items for one poll trigger.
type Handler interface {
	Poll(ctx context.Context, claim Claim) ([]workflow.Item, string, error)
}

// Claim is one leased poll tick.
type Claim struct {
	Cursor    repository.PollCursor
	Node      workflow.Node
	Document  workflow.Document
	VersionID string
	Now       time.Time
	Request   engine.Request
}

// QueueFunc starts one polled execution.
type QueueFunc func(ctx context.Context, tenantID, workflowID, versionID, triggerNodeID string, payload json.RawMessage) error

// VersionLoader loads the published document a poll is pinned to.
type VersionLoader interface {
	GetVersionByID(ctx context.Context, tenant repository.TenantScope, workflowID, versionID string) (workflow.Version, error)
}

// Service ticks leased poll triggers.
type Service struct {
	cursors  repository.PollCursorStore
	versions VersionLoader
	handlers map[string]Handler
	queue    QueueFunc
	request  func(tenantID string) engine.Request
	workerID string
	lease    time.Duration
	budget   time.Duration
	interval time.Duration
	clock    func() time.Time
	logger   *slog.Logger
}

// Options configures the poller.
type Options struct {
	Cursors  repository.PollCursorStore
	Versions VersionLoader
	Handlers map[string]Handler
	Queue    QueueFunc
	Request  func(tenantID string) engine.Request
	WorkerID string
	Lease    time.Duration
	Budget   time.Duration
	Interval time.Duration
	Clock    func() time.Time
	Logger   *slog.Logger
}

// New constructs the poller.
func New(options Options) (*Service, error) {
	if options.Cursors == nil || options.Queue == nil || options.Versions == nil {
		return nil, fmt.Errorf("poller requires cursor storage, a version loader and a queue function")
	}
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	if options.Lease <= 0 {
		options.Lease = DefaultLease
	}
	if options.Budget <= 0 {
		options.Budget = DefaultBudget
	}
	if options.Interval <= 0 {
		options.Interval = TickResolution
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Handlers == nil {
		options.Handlers = map[string]Handler{}
	}
	if options.Request == nil {
		options.Request = func(string) engine.Request { return engine.Request{} }
	}
	return &Service{
		cursors: options.Cursors, versions: options.Versions, handlers: options.Handlers,
		queue: options.Queue, request: options.Request, workerID: options.WorkerID,
		lease: options.Lease, budget: options.Budget, interval: options.Interval,
		clock: options.Clock, logger: options.Logger,
	}, nil
}

// Tick claims due polls one at a time, fetches new items, queues one
// execution per item, and advances each cursor. Claiming one row per
// transaction lets SKIP LOCKED fan replicas out instead of locking the
// whole due set.
func (service *Service) Tick(ctx context.Context) (int, error) {
	now := service.clock()
	queued := 0
	for ctx.Err() == nil {
		due, err := service.cursors.ClaimDue(ctx, now, service.workerID, service.lease)
		if err != nil {
			return queued, err
		}
		if len(due) == 0 {
			return queued, nil
		}
		for _, item := range due {
			count, err := service.runOne(ctx, now, item)
			if err != nil && ctx.Err() == nil {
				service.logger.Error("poll tick", "poll", item.Cursor.ID, "node", item.Cursor.NodeID, "error", err)
			}
			queued += count
		}
	}
	return queued, ctx.Err()
}

func (service *Service) runOne(ctx context.Context, now time.Time, due repository.DuePoll) (int, error) {
	handler, found := service.handlers[due.Cursor.NodeType]
	interval := due.Cursor.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	next := now.Add(interval)
	if !found {
		_ = service.complete(ctx, due, due.Cursor.Cursor, next)
		return 0, fmt.Errorf("no poll handler for %s", due.Cursor.NodeType)
	}
	if due.WorkflowVersionID == "" {
		_ = service.complete(ctx, due, due.Cursor.Cursor, next)
		return 0, fmt.Errorf("workflow has no active revision")
	}
	version, err := service.versions.GetVersionByID(ctx, repository.TenantScope{ID: due.Cursor.TenantID}, due.Cursor.WorkflowID, due.WorkflowVersionID)
	if err != nil {
		_ = service.complete(ctx, due, due.Cursor.Cursor, next)
		return 0, err
	}
	node, ok := findNode(version.Document, due.Cursor.NodeID)
	if !ok {
		_ = service.complete(ctx, due, due.Cursor.Cursor, next)
		return 0, fmt.Errorf("poll node %s is no longer in the document", due.Cursor.NodeID)
	}
	budget, cancel := context.WithTimeout(ctx, service.budget)
	defer cancel()
	items, cursor, err := handler.Poll(budget, Claim{
		Cursor: due.Cursor, Node: node, Document: version.Document,
		VersionID: due.WorkflowVersionID, Now: now,
		Request: service.request(due.Cursor.TenantID),
	})
	if err != nil {
		_ = service.complete(ctx, due, due.Cursor.Cursor, next)
		return 0, err
	}
	if cursor == "" {
		cursor = due.Cursor.Cursor
	}
	queued := 0
	for _, payloadItem := range items {
		body, marshalErr := json.Marshal(payloadItem.JSON)
		if marshalErr != nil {
			_ = service.complete(ctx, due, due.Cursor.Cursor, next)
			return queued, fmt.Errorf("encode poll item: %w", marshalErr)
		}
		if err := service.queue(ctx, due.Cursor.TenantID, due.Cursor.WorkflowID, due.WorkflowVersionID, due.Cursor.NodeID, body); err != nil {
			// Keep the old cursor so these items are fetched again. Advancing
			// after a queue failure would drop mail that never ran.
			_ = service.complete(ctx, due, due.Cursor.Cursor, next)
			return queued, fmt.Errorf("queue polled execution: %w", err)
		}
		queued++
	}
	if err := service.complete(ctx, due, cursor, next); err != nil {
		return queued, err
	}
	return queued, nil
}

func (service *Service) complete(ctx context.Context, due repository.DuePoll, cursor string, next time.Time) error {
	return service.cursors.Complete(ctx, due.Cursor.ID, cursor, next, service.workerID)
}

// Start runs Tick on an interval until the context is cancelled.
func (service *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(service.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.Tick(ctx); err != nil && ctx.Err() == nil {
					service.logger.Error("poller tick", "error", err)
				}
			}
		}
	}()
}

func findNode(document workflow.Document, nodeID string) (workflow.Node, bool) {
	for _, node := range document.Nodes {
		if node.ID == nodeID {
			return node, true
		}
	}
	return workflow.Node{}, false
}
