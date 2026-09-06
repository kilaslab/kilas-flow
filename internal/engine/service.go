package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/binary"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// ExecutionStore is the persistence seam used by the durable runtime service.
// Implementations own transactions and ORM details; engine depends only on
// these repository-shaped operations.
type ExecutionStore interface {
	ClaimNext(context.Context, string, time.Time) (execution.Record, workflow.Document, bool, error)
	QueueTriggered(context.Context, repository.TenantScope, string, string, execution.Trigger, string, json.RawMessage) (execution.Record, error)
	Get(context.Context, repository.TenantScope, string) (execution.Record, error)
	UpdateRuntime(context.Context, repository.TenantScope, execution.Record) (execution.Record, error)
	CreateNodeRun(context.Context, repository.TenantScope, execution.NodeRun) (execution.NodeRun, error)
	Cancel(context.Context, repository.TenantScope, string) (execution.Record, error)
	// StartChild creates a sub-workflow execution that is already running and
	// already claimed, and returns the document it is pinned to.
	StartChild(context.Context, repository.TenantScope, repository.ChildExecution) (execution.Record, workflow.Document, error)
	// SuspendExecution parks a running execution as waiting, holding the
	// checkpoint a resumed run continues from.
	SuspendExecution(context.Context, repository.TenantScope, repository.SuspendWaitParams) (repository.Wait, execution.Record, error)
	// FindWaitByToken loads a wait by its token hash for the info surface.
	FindWaitByToken(context.Context, string) (repository.Wait, error)
	// FindActiveWait returns the unconsumed wait holding an execution, if any.
	FindActiveWait(context.Context, repository.TenantScope, string) (repository.Wait, error)
	// ResumeWait consumes one token and re-queues its execution.
	ResumeWait(context.Context, string, string, json.RawMessage, time.Time) (repository.Wait, execution.Record, error)
	// SettleExpiredWait consumes one expired wait the sweeper reaped.
	SettleExpiredWait(context.Context, uint, repository.ExpiredResolution, time.Time) (repository.Wait, execution.Record, error)
	// LoadResumeState returns the checkpoint a re-queued run resumes from.
	LoadResumeState(context.Context, repository.TenantScope, string) (repository.Wait, error)
	// ListExpiredWaits returns unconsumed waits past their deadline.
	ListExpiredWaits(context.Context, time.Time, int) ([]repository.Wait, error)
}

// CredentialStore resolves a stored credential for the tenant that owns the
// running execution.
type CredentialStore interface {
	Resolve(context.Context, repository.TenantScope, string) (credentials.Record, map[string]string, error)
}

// ServiceDeps configures one local durable execution worker.
type ServiceDeps struct {
	// Binaries stores item payloads. Nil leaves a node that needs one failing
	// with a clear message rather than silently dropping an attachment.
	Binaries    binary.Store
	Executions  ExecutionStore
	Catalog     workflow.Catalog
	Runner      *Runner
	Credentials CredentialStore
	// Events receives standardized execution events. It is optional: delivery
	// must never be a prerequisite for durable persistence or for a run to
	// succeed, so a nil broker simply publishes nothing.
	Events *events.Broker
	// Environment is the allowlisted `$env` map. The service never reads the
	// process environment itself, so what a workflow can see is decided once,
	// at composition.
	Environment    map[string]string
	WorkerID       string
	DefaultTimeout time.Duration
	// SubworkflowTriggerType is the node type a called workflow starts from.
	// Empty leaves a sub-workflow call starting from every root, which is what
	// a workflow written before the trigger existed still needs.
	SubworkflowTriggerType string
	// PollInterval bounds idle latency when no wake arrives. Non-positive
	// keeps the 100 ms tick, which stays the fallback under every watcher:
	// a dropped notification costs latency, never a stuck execution.
	PollInterval time.Duration
	// SweepInterval is how often expired waits settle. Non-positive keeps
	// one minute. There is no knob to disable it: without the sweep a
	// forgotten approval would stay suspended forever.
	SweepInterval time.Duration
	// RelayPrefix keys the cross-process LISTEN/NOTIFY channels for live
	// execution events and cancellation interrupts (see
	// ExecutionEventsChannel). Empty matches a database with no table
	// prefix, which is what every existing install has.
	RelayPrefix string
	// RelaySend delivers one pg_notify payload to listening processes. Nil
	// disables cross-process notifications: the local broker and the local
	// active map keep working, and the durable row stays the source of
	// truth. It must be fast and must never fail a run — publish and Cancel
	// log a delivery error and continue — because the row was already
	// written before the notice is sent.
	RelaySend func(channel, payload string) error
	// PublicBaseURL prefixes the resume links handed to waiting executions
	// (server.public_url). Empty renders path-only links, which is all a
	// same-origin dashboard and approval page need.
	PublicBaseURL string
	// Logger receives the failures a worker cannot act on but nobody should
	// have to guess at. Optional: an unset logger falls back to the default
	// rather than becoming a nil dereference, because a service that refused
	// to start over a missing logger would be worse than a quiet one.
	Logger *slog.Logger
}

// Service claims queued execution records and persists their deterministic
// runtime results. It is deliberately transport-independent.
type Service struct {
		executions             ExecutionStore
		binaries               binary.Store
		catalog                workflow.Catalog
		runner                 *Runner
		credentials            CredentialStore
		events                 *events.Broker
		environment            map[string]string
		workerID               string
		defaultTimeout         time.Duration
		pollInterval           time.Duration
		sweepInterval          time.Duration
		relayPrefix            string
		relaySend              func(channel, payload string) error
		publicBaseURL          string
		subworkflowTriggerType string
		activeMu               sync.Mutex
		active                 map[string]context.CancelFunc
		startOnce              sync.Once
		wake                   chan struct{}
		log                    *slog.Logger
	}

func NewService(deps ServiceDeps) (*Service, error) {
	if deps.Executions == nil || deps.Catalog == nil || deps.Runner == nil || deps.WorkerID == "" {
		return nil, fmt.Errorf("engine executions, catalog, runner, and worker ID are required")
	}
	if deps.DefaultTimeout <= 0 {
		return nil, fmt.Errorf("engine default timeout must be positive")
	}
	environment := make(map[string]string, len(deps.Environment))
	for key, value := range deps.Environment {
		environment[key] = value
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// The tick stays the fallback under every watcher: a dropped LISTEN/NOTIFY
	// costs latency, never a stuck execution. Non-positive keeps the stock
	// 100 ms / 1 min so a caller that never heard of the knobs gets them.
	pollInterval := deps.PollInterval
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	sweepInterval := deps.SweepInterval
	if sweepInterval <= 0 {
		sweepInterval = time.Minute
	}
	return &Service{
		log:                    logger,
		executions:             deps.Executions,
		binaries:               deps.Binaries,
		catalog:                deps.Catalog,
		runner:                 deps.Runner,
		credentials:            deps.Credentials,
		events:                 deps.Events,
		environment:            environment,
		workerID:               deps.WorkerID,
		defaultTimeout:         deps.DefaultTimeout,
		pollInterval:           pollInterval,
		sweepInterval:          sweepInterval,
		relayPrefix:            deps.RelayPrefix,
		relaySend:              deps.RelaySend,
		publicBaseURL:          strings.TrimSuffix(deps.PublicBaseURL, "/"),
		subworkflowTriggerType: deps.SubworkflowTriggerType,
		active:                 make(map[string]context.CancelFunc),
		wake:                   make(chan struct{}, 1),
	}, nil
}

// DiscardBinaries removes the payloads one execution wrote.
//
// A binary store with no deletion path is a disk-full incident with a delay
// fuse. Execution retention does not exist yet — nothing in this codebase
// removes an execution row — so this is the single call the pruner in
// FEAT-5fv8gf makes once it does, rather than a directory tree it has to
// reverse-engineer. It is scoped like every other tenant-facing operation, so a
// prune that gets the tenant wrong removes nothing instead of the wrong thing.
func (service *Service) DiscardBinaries(tenantID, executionID string) error {
	if service.binaries == nil {
		return nil
	}
	return service.binaries.DeleteExecution(binary.Scope{TenantID: tenantID, ExecutionID: executionID})
}

// RunOnce claims and completes at most one queued execution. It is exported so
// the application worker loop and deterministic integration tests share the
// exact same processing path.
func (service *Service) RunOnce(ctx context.Context) (bool, error) {
	return service.runOnce(ctx, service.workerID)
}

// Get reads the durable execution record and its deterministic node-run log.
func (service *Service) Get(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Record, error) {
	return service.executions.Get(ctx, tenant, executionID)
}

func (service *Service) runOnce(ctx context.Context, workerID string) (bool, error) {
	leaseUntil := time.Now().UTC().Add(service.defaultTimeout)
	record, document, claimed, err := service.executions.ClaimNext(ctx, workerID, leaseUntil)
	if err != nil || !claimed {
		return claimed, err
	}
	tenant := repository.TenantScope{ID: record.TenantID}
	if record.Status == execution.StatusCancelling {
		now := time.Now().UTC()
		record.Status = execution.StatusCancelled
		record.Output = json.RawMessage("null")
		record.Error = structuredError("execution.cancelled", errors.New("execution cancellation was recovered after its worker lease expired"))
		record.FinishedAt = &now
		if _, err := service.executions.UpdateRuntime(ctx, tenant, record); err != nil {
			return true, err
		}
		return true, nil
	}
	// A re-queued wait carries its checkpoint beside the execution row. Fresh
	// executions have no wait rows and read as not-found, which simply means
	// a fresh run. Anything else fails the claim loudly rather than running
	// the graph from the start and firing every non-idempotent node twice.
	resumeState, err := service.loadResumeState(ctx, tenant, record.ID)
	if err != nil {
		return true, err
	}
	seqBase := 0
	if resumeState != nil {
		seqBase = resumeState.RunCount
	}
	// Minted before the graph runs so a workflow can compose its own resume
	// link before suspending. A run that never suspends discards its token.
	resume, err := service.mintResumeURLs()
	if err != nil {
		return true, err
	}
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: events.ExecutionStarted, Status: execution.StatusRunning,
	})
	runCtx, cancel := context.WithTimeout(ctx, service.defaultTimeout)
	service.activeMu.Lock()
	service.active[record.ID] = cancel
	service.activeMu.Unlock()
	// Cross-process cancellation floor. Cancel persists cancelling in the
	// row; a holder in another process finds it here and interrupts the run
	// between nodes (see the runner's ctx check), without waiting for the
	// lease to expire. WatchCancellations is only the low-latency path: a
	// dropped notification costs up to one poll interval, never a stuck
	// run, because this poll stays even once the notification works.
	stopPoll := make(chan struct{})
	defer close(stopPoll)
	go service.pollCancellation(runCtx, cancel, tenant, record.ID, stopPoll)
	var result Result
	var runErr error
	if resumeState != nil {
		result, runErr = service.resumeRun(runCtx, record, document, []string{record.WorkflowID}, *resumeState, resume)
	} else {
		result, runErr = service.run(runCtx, record, document, resume)
	}
	service.activeMu.Lock()
	delete(service.active, record.ID)
	service.activeMu.Unlock()
	cancel()
	// The terminal writes below must not use ctx. On graceful shutdown ctx is
	// already cancelled (SIGTERM), and a cancelled context fails the very
	// writes that settle the run — leaving the execution running with its
	// lease held until expiry instead of terminal with its lease released.
	// Detaching costs nothing when ctx is live and saves the lease on the
	// one path where it matters.
	persistCtx := context.WithoutCancel(ctx)
	// Suspension is neither success nor failure: the trace produced so far
	// persists, the wait row holds the checkpoint, and the worker is free.
	var suspended *SuspendError
	if runErr != nil && errors.As(runErr, &suspended) {
		parked, err := service.suspend(persistCtx, tenant, record, document, result, suspended, seqBase, resume)
		if err != nil {
			// An invalid suspension (no mode, no deadline, no checkpoint)
			// fails the run like any other error rather than parking an
			// execution nobody can resume.
			if !parked {
				runErr = err
			} else {
				return true, err
			}
		} else {
			return true, nil
		}
	}
	nodeTypes := make(map[string]string, len(document.Nodes))
	for _, node := range document.Nodes {
		nodeTypes[node.ID] = node.Type
	}
	for sequence, run := range result.NodeRuns {
		input, err := json.Marshal(run.Input)
		if err != nil {
			return true, fmt.Errorf("marshal node %q input: %w", run.NodeID, err)
		}
		output, err := json.Marshal(run.Output)
		if err != nil {
			return true, fmt.Errorf("marshal node %q output: %w", run.NodeID, err)
		}
		// The durable trace and the live event carry the projected output,
		// while the next node already received the full one in memory. See
		// projectTrace for what is projected and what is deliberately left.
		output = projectTrace(nodeTypes[run.NodeID], output)
		status := execution.StatusSucceeded
		if run.Skipped {
			// A pruned branch is neither a success nor a failure. Recording it
			// as succeeded would make an untaken arm look like one that ran and
			// happened to produce nothing.
			status = execution.StatusSkipped
		}
		var errorPayload json.RawMessage
		if run.Error != nil {
			status = execution.StatusFailed
			if errors.Is(run.Error, context.Canceled) {
				status = execution.StatusCancelled
				if run.ErrorCode == "" || run.ErrorCode == "node.failed" {
					run.ErrorCode = "execution.cancelled"
				}
			}
			errorPayload = structuredError(run.ErrorCode, run.Error)
		}
		now := time.Now().UTC()
		if _, err := service.executions.CreateNodeRun(persistCtx, tenant, execution.NodeRun{
			TenantID: record.TenantID, ExecutionID: record.ID, NodeID: run.NodeID, Attempt: attemptOf(run), RunIndex: run.RunIndex, Sequence: seqBase + sequence + 1,
			Status: status, Input: input, Output: output, Error: errorPayload, StartedAt: now, FinishedAt: &now, LeaseOwner: record.LeaseOwner,
		}); err != nil {
			return true, fmt.Errorf("persist node %q run: %w", run.NodeID, err)
		}
		// Published only after the node run is durable, so a subscriber can
		// never observe a state the record does not already carry.
		eventType := events.NodeCompleted
		// A skipped node reached a terminal state without failing. Publishing
		// node.failed for it would light a pruned branch up as an error on the
		// live canvas; the node-run record carries the skipped status, which is
		// what a reader needs to tell the two apart.
		if status != execution.StatusSucceeded && status != execution.StatusSkipped {
			eventType = events.NodeFailed
		}
		service.publish(events.Event{
			TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
			NodeID: run.NodeID, Type: eventType, Status: status, Sequence: seqBase + sequence + 1,
			Data: output,
		})
	}
	finishedAt := time.Now().UTC()
	if runErr != nil {
		code := "execution.failed"
		status := execution.StatusFailed
		if errors.Is(runErr, context.Canceled) {
			code = "execution.cancelled"
			status = execution.StatusCancelled
		}
		for _, run := range result.NodeRuns {
			if run.ErrorCode == "execution.timeout" {
				code = "execution.timeout"
				break
			}
		}
		record.Status = status
		record.Error = structuredError(code, runErr)
		record.FinishedAt = &finishedAt
		updated, updateErr := service.executions.UpdateRuntime(persistCtx, tenant, record)
		if updateErr != nil {
			return true, updateErr
		}
		terminal := events.ExecutionFailed
		if updated.Status == execution.StatusCancelled {
			terminal = events.ExecutionCancelled
		}
		service.publish(events.Event{
			TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
			Type: terminal, Status: updated.Status, Data: updated.Error,
		})
		return true, nil
	}
	output, err := json.Marshal(result.Output)
	if err != nil {
		return true, fmt.Errorf("marshal execution output: %w", err)
	}
	record.Status = execution.StatusSucceeded
	record.Output = output
	record.Error = json.RawMessage("null")
	record.FinishedAt = &finishedAt
	updated, err := service.executions.UpdateRuntime(persistCtx, tenant, record)
	if err != nil {
		return true, err
	}
	terminal := events.ExecutionCompleted
	if updated.Status == execution.StatusCancelled {
		terminal = events.ExecutionCancelled
	}
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: terminal, Status: updated.Status, Data: updated.Output,
	})
	return true, nil
}

// Start launches a bounded worker pool. Every worker claims records from the
// durable repository, so queued work is shared across processes pointing at
// the same database — not tied to an HTTP request goroutine or to one
// process — and remains recoverable after a restart. A claim stamps a fenced
// lease owner, so of any number of workers racing for one execution exactly
// one wins. It also starts the expired-wait sweeper, so a forgotten approval
// resolves instead of staying suspended forever.
func (service *Service) Start(ctx context.Context, maxConcurrent int) error {
	if maxConcurrent < 1 {
		return fmt.Errorf("engine max concurrent executions must be positive")
	}
	service.startOnce.Do(func() {
		for index := 1; index <= maxConcurrent; index++ {
			workerID := fmt.Sprintf("%s-%d", service.workerID, index)
			go service.worker(ctx, workerID)
		}
		go service.sweepLoop(ctx)
	})
	return nil
}

func (service *Service) worker(ctx context.Context, workerID string) {
	for {
		worked, err := service.runOnce(ctx, workerID)
		if ctx.Err() != nil {
			return
		}
		// Logged rather than discarded. This error was assigned to _, and the
		// silence is what let a PostgreSQL type mismatch repeat every
		// customer's workflow once a minute without a line anywhere at any
		// severity: the write of the terminal status was rejected, the lease
		// expired, the row was reclaimed, and the whole thing ran again.
		//
		// Not fatal, and not a reason to stop the worker: a failure to record
		// one execution's outcome must not stop the others from running. The
		// point is only that it stops being invisible.
		if err != nil {
			service.log.Error("worker iteration failed", "worker", workerID, "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-service.wake:
		case <-time.After(service.pollIntervalOrDefault()):
		}
	}
}

// pollIntervalOrDefault is the worker's idle wait. NewService already defaults
// it, so the guard here is only for a Service built before the field existed.
func (service *Service) pollIntervalOrDefault() time.Duration {
	if service.pollInterval <= 0 {
		return 100 * time.Millisecond
	}
	return service.pollInterval
}

// pollCancellation watches the durable row for a cancellation request and
// interrupts the run holding it. It is the floor under WatchCancellations:
// the notice arrives in milliseconds when the listener is healthy, and this
// poll finds the same row within one tick when it is not. Poll errors are
// best effort — the next tick retries — so a blip costs latency, never a
// stuck run, and the watcher exits with the run it guards.
func (service *Service) pollCancellation(runCtx context.Context, cancel context.CancelFunc, tenant repository.TenantScope, executionID string, stop <-chan struct{}) {
	ticker := time.NewTicker(service.pollIntervalOrDefault())
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
		pollCtx, stopPoll := context.WithTimeout(context.Background(), 5*time.Second)
		record, err := service.executions.Get(pollCtx, tenant, executionID)
		stopPoll()
		if err != nil {
			continue
		}
		if record.Status == execution.StatusCancelling || record.Status == execution.StatusCancelled {
			cancel()
			return
		}
	}
}

// publish delivers a standardized event when a broker is configured, and
// relays its identifiers to listening processes when a relay is configured.
//
// It is deliberately fire and forget: the caller has already persisted the
// state the event describes, so a dropped event costs a live update, never
// correctness.
func (service *Service) publish(event events.Event) {
	if service.events != nil {
		service.events.Publish(event)
	}
	service.notifyEvent(event)
}

// Events exposes the broker so the API can open live feeds without reaching
// through the service for unrelated state.
func (service *Service) Events() *events.Broker {
	return service.events
}

// Wake asks idle workers to poll immediately after an API queues work.
func (service *Service) Wake() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *Service) run(ctx context.Context, record execution.Record, document workflow.Document, resume resumeURLs) (Result, error) {
	return service.runWithStack(ctx, record, document, []string{record.WorkflowID}, resume)
}

// runWithStack is run with the call chain that reached this execution.
//
// A top-level run's stack is just itself; a sub-workflow's carries every
// workflow above it, which is what lets the next call refuse a cycle by name.
func (service *Service) runWithStack(ctx context.Context, record execution.Record, document workflow.Document, stack []string, resume resumeURLs) (Result, error) {
	ir, err := workflow.Compile(document, service.catalog)
	if err != nil {
		return Result{}, err
	}
	item, err := inputItem(record.Input)
	if err != nil {
		return Result{}, err
	}
	request := service.newRequest(record, document, stack, resume)
	request.Input = item
	request.TriggerNodeID = record.TriggerNodeID
	// Left nil when this server has no binary storage, so a node can ask
	// whether storing a payload is even possible. Boxing a nil store in the
	// interface would make that question unanswerable, and a node that has to
	// attempt a write to find out is a node that fails where it could have
	// degraded.
	if service.binaries != nil {
		request.Binaries = binary.For(service.binaries, record.TenantID, record.ID)
	}
	return service.runner.Run(ctx, ir, request)
}

// tenantCredentials binds credential resolution to the tenant that owns the
// running execution, so a workflow can never name a credential from another
// tenant even if it guesses the ID.
// NewTenantCredentials builds a resolver confined to one tenant.
//
// Exported so a webhook lifecycle hook resolves secrets through exactly the
// same tenant-scoped path an executor does, rather than a parallel one that
// could quietly miss the scoping.
func NewTenantCredentials(store CredentialStore, tenant repository.TenantScope) CredentialResolver {
	return &tenantCredentials{store: store, tenant: tenant}
}

type tenantCredentials struct {
	store  CredentialStore
	tenant repository.TenantScope
}

func (resolver *tenantCredentials) ResolveCredential(ctx context.Context, credentialID string) (Credential, error) {
	if resolver == nil || resolver.store == nil {
		return Credential{}, fmt.Errorf("credential storage is not configured")
	}
	record, fields, err := resolver.store.Resolve(ctx, resolver.tenant, credentialID)
	if err != nil {
		return Credential{}, fmt.Errorf("resolve credential: %w", err)
	}
	return Credential{
		ID: record.ID, Name: record.Name, Type: record.Type,
		Fields: fields, AllowedDomains: record.AllowedDomains,
	}, nil
}

// QueueWebhook persists a queued execution for a resolved webhook binding and
// wakes an idle worker.
//
// The trigger surface goes through the same durable queue as a manual run, so
// a webhook cannot bypass lifecycle, validation, or execution-record rules.
func (service *Service) QueueWebhook(ctx context.Context, binding repository.WebhookBinding, payload json.RawMessage) (execution.Record, error) {
	// The binding already names the node that owns this path, so the run starts
	// from that webhook alone — a workflow with a nightly schedule beside it
	// must not fire the schedule on a delivery.
	record, err := service.executions.QueueTriggered(ctx,
		repository.TenantScope{ID: binding.TenantID}, binding.WorkflowID, binding.WorkflowVersionID,
		execution.TriggerWebhook, binding.NodeID, payload)
	if err != nil {
		return execution.Record{}, err
	}
	service.Wake()
	return record, nil
}

// QueueScheduled persists a queued execution for a due schedule.
func (service *Service) QueueScheduled(ctx context.Context, tenantID, workflowID, versionID, triggerNodeID string, payload json.RawMessage) (execution.Record, error) {
	record, err := service.executions.QueueTriggered(ctx,
		repository.TenantScope{ID: tenantID}, workflowID, versionID,
		execution.TriggerSchedule, triggerNodeID, payload)
	if err != nil {
		return execution.Record{}, err
	}
	service.Wake()
	return record, nil
}

// Cancel requests durable cancellation, interrupts the matching in-process
// worker immediately when it is currently running on this instance, and
// relays the interrupt to the process holding the lease when it runs
// elsewhere. The row is the floor: a holder that misses the notice still
// finds cancelling on its next status poll and stops between nodes.
func (service *Service) Cancel(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Record, error) {
	record, err := service.executions.Cancel(ctx, tenant, executionID)
	if err != nil {
		return execution.Record{}, err
	}
	if record.Status == execution.StatusCancelling {
		service.activeMu.Lock()
		cancel := service.active[executionID]
		service.activeMu.Unlock()
		if cancel != nil {
			cancel()
		}
		service.notifyCancel(record.TenantID, record.ID)
	}
	if record.Status == execution.StatusCancelled {
		// Queued work is cancelled without ever being claimed, so no worker
		// will publish a terminal event for it.
		service.publish(events.Event{
			TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
			Type: events.ExecutionCancelled, Status: record.Status,
		})
	}
	return record, nil
}

func inputItem(payload json.RawMessage) (workflow.Item, error) {
	if len(payload) == 0 || string(payload) == "null" {
		return workflow.Item{JSON: map[string]any{}}, nil
	}
	var input map[string]any
	if err := json.Unmarshal(payload, &input); err != nil {
		return workflow.Item{}, fmt.Errorf("manual input must be a JSON object: %w", err)
	}
	if input == nil {
		input = map[string]any{}
	}
	return workflow.Item{JSON: input}, nil
}

func structuredError(code string, err error) json.RawMessage {
	payload, marshalErr := json.Marshal(map[string]string{"code": code, "message": err.Error()})
	if marshalErr != nil {
		return json.RawMessage(`{"code":"execution.failed","message":"execution failed"}`)
	}
	return payload
}

// attemptOf is the attempt number a node run belongs to.
//
// The persistence layer has carried an Attempt column with a unique index on
// (execution, node, attempt) since V1 and the service wrote 1 into every row,
// so a retried node would have collided with itself the moment retries existed.
// A run recorded before attempts were tracked reports 0; treating that as the
// first attempt keeps those rows valid.
func attemptOf(run NodeRun) int {
	if run.Attempt < 1 {
		return 1
	}
	return run.Attempt
}

// MaxWorkflowCallDepth bounds how deep a chain of sub-workflow calls may go.
//
// A limit beside the cycle check rather than instead of it. The stack refuses a
// workflow that calls itself, directly or through others, but a chain of
// distinct workflows a hundred deep is still a runaway — and every level of it
// holds the caller's goroutine and its stack frame.
const MaxWorkflowCallDepth = 16

// InvokeWorkflow runs another workflow of the same tenant, inline.
//
// Inline, in the calling worker's goroutine, is the whole design. The obvious
// alternative — write a queued child record and let the worker pool pick it up
// while the parent blocks waiting for it — deadlocks the moment the call depth
// reaches the pool size. That pool defaults to ten, so ten nested calls would
// hang the entire installation with no error anywhere: every worker waiting on
// a child no worker is left to run.
//
// The child still gets a durable execution record, with the parent's ID on it,
// so the chain is visible in history and in the events stream. It is created
// already running and already claimed, because a queued one would be visible to
// ClaimNext and could be run a second time in parallel with this one.
func (service *Service) InvokeWorkflow(ctx context.Context, parent ExecutionContext, call WorkflowCall) (WorkflowCallResult, error) {
	if service == nil || service.executions == nil {
		return WorkflowCallResult{}, fmt.Errorf("this runtime cannot run sub-workflows")
	}
	target := strings.TrimSpace(call.WorkflowID)
	if target == "" {
		return WorkflowCallResult{}, fmt.Errorf("a sub-workflow call needs a workflow to run")
	}
	if err := checkCallStack(parent.Stack, target); err != nil {
		return WorkflowCallResult{}, err
	}

	tenant := repository.TenantScope{ID: parent.TenantID}
	input, err := json.Marshal(map[string]any{"items": itemsJSON(call.Items)})
	if err != nil {
		return WorkflowCallResult{}, fmt.Errorf("encode sub-workflow input: %w", err)
	}
	record, document, err := service.executions.StartChild(ctx, tenant, repository.ChildExecution{
		WorkflowID: target, ParentExecutionID: parent.ID,
		Input: input, SelectTrigger: service.subworkflowTrigger,
		LeaseOwner: service.workerID, LeaseUntil: time.Now().UTC().Add(service.defaultTimeout),
	})
	if err != nil {
		return WorkflowCallResult{}, err
	}
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: events.ExecutionStarted, Status: execution.StatusRunning,
	})

	result, runErr := service.runWithStack(ctx, record, document, append(append([]string(nil), parent.Stack...), target), resumeURLs{})
	var suspended *SuspendError
	if runErr != nil && errors.As(runErr, &suspended) {
		runErr = refuseSuspendInChild(target, suspended)
	}
	// Like runOnce's terminal writes: the caller's context may already be
	// cancelled (shutdown, or the parent's own cancellation), and the child's
	// record must still settle rather than stay running with a held lease.
	if err := service.persistChild(context.WithoutCancel(ctx), tenant, record, result, runErr); err != nil {
		return WorkflowCallResult{}, err
	}
	if runErr != nil {
		// Named with the workflow, because "node X failed" inside a
		// sub-workflow reads as a failure of the caller otherwise.
		return WorkflowCallResult{ExecutionID: record.ID}, fmt.Errorf("sub-workflow %q failed: %w", target, runErr)
	}
	if !call.Wait {
		return WorkflowCallResult{ExecutionID: record.ID}, nil
	}
	return WorkflowCallResult{ExecutionID: record.ID, Items: terminalItems(result.Output)}, nil
}

// checkCallStack refuses a cycle, and then a chain that is merely too long.
func checkCallStack(stack []string, target string) error {
	for index, workflowID := range stack {
		if workflowID != target {
			continue
		}
		// The cycle is printed as the path that reached it, so the author can
		// see which call to remove rather than being told a number.
		return fmt.Errorf("sub-workflow call would repeat workflow %q: %s → %s",
			target, strings.Join(stack[index:], " → "), target)
	}
	if len(stack) >= MaxWorkflowCallDepth {
		return fmt.Errorf("sub-workflow calls are nested %d deep, which is the limit; the chain is %s → %s",
			len(stack), strings.Join(stack, " → "), target)
	}
	return nil
}

// persistChild writes the child's node runs and its terminal state.
func (service *Service) persistChild(ctx context.Context, tenant repository.TenantScope, record execution.Record, result Result, runErr error) error {
	for sequence, run := range result.NodeRuns {
		input, err := json.Marshal(run.Input)
		if err != nil {
			return fmt.Errorf("marshal node %q input: %w", run.NodeID, err)
		}
		output, err := json.Marshal(run.Output)
		if err != nil {
			return fmt.Errorf("marshal node %q output: %w", run.NodeID, err)
		}
		status := execution.StatusSucceeded
		if run.Skipped {
			status = execution.StatusSkipped
		}
		var errorPayload json.RawMessage
		if run.Error != nil {
			status = execution.StatusFailed
			errorPayload = structuredError(run.ErrorCode, run.Error)
		}
		now := time.Now().UTC()
		if _, err := service.executions.CreateNodeRun(ctx, tenant, execution.NodeRun{
			TenantID: record.TenantID, ExecutionID: record.ID, NodeID: run.NodeID,
			Attempt: attemptOf(run), RunIndex: run.RunIndex, Sequence: sequence + 1,
			Status: status, Input: input, Output: output, Error: errorPayload,
			StartedAt: now, FinishedAt: &now, LeaseOwner: record.LeaseOwner,
		}); err != nil {
			return fmt.Errorf("persist sub-workflow node %q run: %w", run.NodeID, err)
		}
	}

	finishedAt := time.Now().UTC()
	record.FinishedAt = &finishedAt
	if runErr != nil {
		record.Status = execution.StatusFailed
		record.Error = structuredError("execution.failed", runErr)
		if errors.Is(runErr, context.Canceled) {
			record.Status = execution.StatusCancelled
			record.Error = structuredError("execution.cancelled", runErr)
		}
	} else {
		output, err := json.Marshal(result.Output)
		if err != nil {
			return fmt.Errorf("marshal sub-workflow output: %w", err)
		}
		record.Status = execution.StatusSucceeded
		record.Output = output
		record.Error = json.RawMessage("null")
	}
	updated, err := service.executions.UpdateRuntime(ctx, tenant, record)
	if err != nil {
		return err
	}
	terminal := events.ExecutionCompleted
	switch updated.Status {
	case execution.StatusFailed:
		terminal = events.ExecutionFailed
	case execution.StatusCancelled:
		terminal = events.ExecutionCancelled
	}
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: terminal, Status: updated.Status, Data: updated.Output,
	})
	return nil
}

// subworkflowTrigger names the node a called workflow starts from.
//
// A workflow may carry a webhook or a schedule beside its sub-workflow trigger,
// and a call that started from every root would fire those too — a sub-workflow
// invoked once would post to whatever its webhook branch posts to. Naming the
// node is what confines the call to the branch that was meant for it.
//
// A workflow with no sub-workflow trigger returns "", which starts every root:
// that is what makes any existing workflow callable without being edited first.
func (service *Service) subworkflowTrigger(document workflow.Document) string {
	if service.subworkflowTriggerType == "" {
		return ""
	}
	for _, node := range document.Nodes {
		if node.Type == service.subworkflowTriggerType {
			return node.ID
		}
	}
	return ""
}

// itemsJSON renders items for the child's trigger input.
func itemsJSON(items []workflow.Item) []any {
	rendered := make([]any, 0, len(items))
	for _, item := range items {
		json := item.JSON
		if json == nil {
			json = map[string]any{}
		}
		rendered = append(rendered, json)
	}
	return rendered
}

// terminalItems flattens a child's output into the items the caller receives.
//
// Every terminal node's first port, in the map's order — which is why the
// Execute Workflow node's own documentation says a sub-workflow should end in
// one branch. Making that a rule enforced here would refuse graphs that are
// perfectly valid and simply return more than the caller expected.
func terminalItems(output map[string]workflow.NodeOutput) []workflow.Item {
	names := make([]string, 0, len(output))
	for name := range output {
		names = append(names, name)
	}
	// Sorted, so a sub-workflow with two terminal branches returns its items in
	// the same order on every run rather than in Go's map order.
	sort.Strings(names)
	items := make([]workflow.Item, 0, 8)
	for _, name := range names {
		for _, port := range output[name] {
			items = append(items, port...)
		}
	}
	return items
}
