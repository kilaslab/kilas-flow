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
	// CreateNodeRuns appends a whole completed trace in one transaction,
	// giving a row whose run index the runner left unset the next free one
	// instead of failing the write.
	CreateNodeRuns(context.Context, repository.TenantScope, []execution.NodeRun) ([]execution.NodeRun, error)
	// ExtendLease renews the fenced lease of a running execution. False means
	// this worker no longer holds it.
	ExtendLease(context.Context, repository.TenantScope, string, string, time.Time) (bool, error)
	// ExecutionState reads one execution's status and whether cancellation has
	// been requested, without loading its trace.
	ExecutionState(context.Context, repository.TenantScope, string) (execution.Status, bool, error)
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
	// MaxTimeout caps the timeout a workflow asks for in its own settings
	// (settings.executionTimeout, n8n's own EXECUTIONS_TIMEOUT_MAX). Zero
	// applies no ceiling of its own: a workflow that names a timeout gets it,
	// and one that names none still gets DefaultTimeout.
	//
	// The default timeout is a budget for one run, not a policy about how long
	// a workflow may take: an imported workflow that says it needs five minutes
	// was being killed at sixty seconds, which is a workflow that fails
	// everywhere it used to work.
	MaxTimeout time.Duration
	// DefaultTimezone is the instance's IANA zone (n8n's GENERIC_TIMEZONE).
	// A workflow that names no zone — or carries n8n's DEFAULT sentinel —
	// reads its clock in it, both for its schedules and for `$now`, `$today`
	// and DateTime.local() inside expressions. Empty means UTC, which is what
	// an unnamed zone has always meant here.
	DefaultTimezone string
	// LeaseDuration is how long a claim is held before another worker may
	// treat the execution as abandoned. It is deliberately not the run
	// timeout: the lease is renewed by a heartbeat while the worker holds it,
	// so it only has to be longer than the interval between two renewals, and
	// a run that takes its full timeout — or a trace that lands just after
	// it — no longer outlives its own claim. Non-positive derives two run
	// timeouts with a floor (see leaseDurationOrDefault).
	LeaseDuration time.Duration
	// SubworkflowTriggerType is the node type a called workflow starts from.
	// Empty leaves a sub-workflow call starting from every root, which is what
	// a workflow written before the trigger existed still needs.
	SubworkflowTriggerType string
	// ErrorTriggerType is the node type an error workflow starts from, for the
	// workflow named by settings.errorWorkflow. Empty leaves an error workflow
	// starting from every root, which is what it would otherwise do when the
	// error workflow also carries a webhook or a schedule.
	ErrorTriggerType string
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
	leaseDuration          time.Duration
	pollInterval           time.Duration
	sweepInterval          time.Duration
	maxTimeout             time.Duration
	defaultTimezone        string
	relayPrefix            string
	relaySend              func(channel, payload string) error
	publicBaseURL          string
	subworkflowTriggerType string
	errorTriggerType       string
	activeMu               sync.Mutex
	active                 map[string]context.CancelFunc
	waitTimers             waitTimers
	workers                sync.WaitGroup
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
		leaseDuration:          deps.LeaseDuration,
		pollInterval:           pollInterval,
		sweepInterval:          sweepInterval,
		maxTimeout:             deps.MaxTimeout,
		defaultTimezone:        strings.TrimSpace(deps.DefaultTimezone),
		relayPrefix:            deps.RelayPrefix,
		relaySend:              deps.RelaySend,
		publicBaseURL:          strings.TrimSuffix(deps.PublicBaseURL, "/"),
		subworkflowTriggerType: deps.SubworkflowTriggerType,
		errorTriggerType:       deps.ErrorTriggerType,
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

// ExecutionState reads one execution's status and nothing else: no node runs,
// no payloads. It exists for the callers that poll a run they did not start —
// a cancellation watcher, a webhook waiting to answer its trigger — because
// reading the whole record ten times a second re-reads every payload the run
// has written, which is the largest row in the database read for the smallest
// fact in it.
//
// It is deliberately a thin delegation so a caller holding this service, and
// not the store, can still ask the light question.
func (service *Service) ExecutionState(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Status, bool, error) {
	return service.executions.ExecutionState(ctx, tenant, executionID)
}

func (service *Service) runOnce(ctx context.Context, workerID string) (bool, error) {
	// The lease is not the run timeout. It used to be the same number, which
	// made the claim an assertion about how long the run would take: a run that
	// used its whole timeout, or a trace written just after it, outlived the
	// claim, and another worker reclaimed the execution while this one was
	// still persisting its result — the same workflow, twice, from one trigger
	// (BUG-1tj5wy). The lease is now renewed by a heartbeat for as long as this
	// worker holds the execution, so it only has to outlive the interval
	// between two renewals.
	record, document, claimed, err := service.executions.ClaimNext(ctx, workerID, time.Now().UTC().Add(service.leaseDurationOrDefault()))
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
	// Resolved once, before the graph runs: the live writer and the terminal
	// flush both need each node's type to project its output for the trace.
	nodeTypes := make(map[string]string, len(document.Nodes))
	for _, node := range document.Nodes {
		nodeTypes[node.ID] = node.Type
	}
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: events.ExecutionStarted, Status: execution.StatusRunning,
	})
	runCtx, cancel := service.runBudget(ctx, document)
	service.activeMu.Lock()
	service.active[record.ID] = cancel
	service.activeMu.Unlock()
	// Renewed from the moment the claim lands until this worker has settled the
	// execution — through the run, and through the trace write that used to
	// fall outside the lease. Losing the fence cancels the run: the execution
	// has been reclaimed, so it is somebody else's work now, and every node
	// this one still executed would be a side effect performed twice.
	heartbeat := service.startLeaseHeartbeat(ctx, tenant, record, cancel)
	defer heartbeat.Stop()
	// Cross-process cancellation floor. Cancel persists cancelling in the
	// row; a holder in another process finds it here and interrupts the run
	// between nodes (see the runner's ctx check), without waiting for the
	// lease to expire. WatchCancellations is only the low-latency path: a
	// dropped notification costs up to one poll interval, never a stuck
	// run, because this poll stays even once the notification works.
	stopPoll := make(chan struct{})
	defer close(stopPoll)
	go service.pollCancellation(runCtx, cancel, tenant, record.ID, stopPoll)
	// Live progress: every node row the runner appends is persisted and
	// published as it happens, so a run that takes minutes is readable while it
	// runs instead of appearing all at once when it ends. The writer is best
	// effort by design — the terminal flush below writes the same rows from the
	// result in memory — so a failure here costs a live update, never a trace.
	trace := service.newTraceWriter(runCtx, tenant, record, nodeTypes, seqBase)
	var result Result
	var runErr error
	if resumeState != nil {
		result, runErr = service.resumeRun(runCtx, record, document, []string{record.WorkflowID}, *resumeState, resume, trace)
	} else {
		result, runErr = service.run(runCtx, record, document, resume, trace)
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
		parked, err := service.suspend(persistCtx, tenant, record, document, result, suspended, seqBase, resume, trace)
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
	rows, err := service.traceRows(record, nodeTypes, seqBase, result.NodeRuns)
	if err != nil {
		return service.failPersist(persistCtx, tenant, record, err)
	}
	// One transaction for the whole trace. Written row by row this was a
	// round trip per node inside the window where the lease had to survive,
	// and the write of row 40 of 100 could already have outlived the claim.
	// Idempotent by row identity too, so a run whose trace was partly written
	// before a crash cannot collide with itself here (BUG-hfhzq6).
	runs := make([]execution.NodeRun, 0, len(rows))
	announce := make(map[int]events.Event, len(rows))
	for _, row := range rows {
		runs = append(runs, row.run)
		announce[row.run.Sequence] = row.event
	}
	created, err := service.executions.CreateNodeRuns(persistCtx, tenant, runs)
	if err != nil {
		return service.failPersist(persistCtx, tenant, record, fmt.Errorf("persist execution trace: %w", err))
	}
	// Only the rows this write added are announced: a row the live writer
	// already persisted — and already published — comes back as a duplicate and
	// stays silent, so one node is never published twice. Keyed by sequence,
	// because a repaired run index is not the index the row arrived with.
	for _, row := range created {
		if event, known := announce[row.Sequence]; known {
			service.publish(event)
		}
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
		// The workflow named by settings.errorWorkflow, if any. Started after
		// the failed execution is terminal, so the error workflow reads a
		// settled record, and never for a cancellation: n8n does not treat a
		// user stopping a run as a failure to report.
		if updated.Status == execution.StatusFailed {
			service.runErrorWorkflow(persistCtx, tenant, record, document, result, code, runErr)
		}
		return true, nil
	}
	output, err := json.Marshal(result.Output)
	if err != nil {
		// A node produced something JSON cannot carry — a NaN, an infinity, a
		// channel — and the result cannot be persisted. Terminal, like a failed
		// trace write: left running it would be reclaimed and the whole graph
		// run again, side effects included, to fail at the same marshal.
		return service.failPersist(persistCtx, tenant, record, fmt.Errorf("marshal execution output: %w", err))
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

// defaultLeaseDuration is the shortest claim a worker takes when the
// deployment names no lease: long enough that recovering a crashed worker is
// not the first thing every restart does, short enough that an execution whose
// worker died resumes without an operator.
const defaultLeaseDuration = 30 * time.Second

// leaseDurationOrDefault decouples the worker lease from the run timeout.
//
// The two used to be the same number, which made the claim an assertion about
// how long a run would take rather than a statement that a worker is alive.
// Twice the run timeout, with a floor, so a claim covers a run that uses its
// whole budget and the trace write after it even if no renewal lands; the
// heartbeat under it is what makes the length almost irrelevant.
func (service *Service) leaseDurationOrDefault() time.Duration {
	if service.leaseDuration > 0 {
		return service.leaseDuration
	}
	lease := 2 * service.defaultTimeout
	if lease < defaultLeaseDuration {
		lease = defaultLeaseDuration
	}
	return lease
}

// leaseHeartbeat renews one worker's claim for as long as the worker holds it.
type leaseHeartbeat struct {
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// startLeaseHeartbeat renews one execution's lease until Stop is called.
//
// It is the difference between a lease that means "this worker was predicted
// to need this long" and one that means "this worker is still here". A worker
// that dies stops renewing, so its execution is reclaimed exactly as before
// once the lease runs out; a worker that is alive but slower than its lease —
// a long run, a slow trace write, a database that paused — keeps its claim and
// no second worker starts the same graph (BUG-1tj5wy).
//
// lostFence runs when the renewal finds the execution is no longer this
// worker's: it has been reclaimed or settled, so stopping the run is what keeps
// its side effects from happening twice. The last renewal is deliberately not
// required to succeed — a lease is allowed to expire while the worker finishes
// a write it has already started, and the writes themselves are fenced.
func (service *Service) startLeaseHeartbeat(ctx context.Context, tenant repository.TenantScope, record execution.Record, lostFence func()) *leaseHeartbeat {
	lease := service.leaseDurationOrDefault()
	interval := lease / 3
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	beat := &leaseHeartbeat{stop: make(chan struct{}), done: make(chan struct{})}
	// Detached from the run's own cancellation on purpose: cancelling a run
	// (shutdown, a cancel request) still ends in a terminal write under this
	// lease, and that write must not race a claim that has just expired.
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		defer close(beat.done)
		defer stopHeartbeat()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-beat.stop:
				return
			case <-ticker.C:
			}
			extendCtx, stopExtend := context.WithTimeout(heartbeatCtx, 5*time.Second)
			held, err := service.executions.ExtendLease(extendCtx, tenant, record.ID, record.LeaseOwner, time.Now().UTC().Add(lease))
			stopExtend()
			if err != nil {
				// A blip costs one renewal when there is still two thirds of
				// the lease left to spend; the next tick retries. A database
				// that is unreachable for longer than the lease has a louder
				// problem than this line.
				service.log.Debug("execution lease renewal failed", "execution", record.ID, "error", err)
				continue
			}
			if !held {
				if lostFence != nil {
					lostFence()
				}
				return
			}
		}
	}()
	return beat
}

// Stop ends the heartbeat and waits for it to return, so no renewal can be
// issued after the caller has settled the execution.
func (beat *leaseHeartbeat) Stop() {
	if beat == nil {
		return
	}
	beat.stopOnce.Do(func() { close(beat.stop) })
	<-beat.done
}

// traceRow is one durable node-run row beside the live event that announces it.
//
// Built together because they carry the same status: publishing from the row
// after the write is what guarantees a subscriber never sees a state the record
// does not already hold, and computing the status twice is how the two drifted
// apart in the first place.
type traceRow struct {
	run   execution.NodeRun
	event events.Event
}

// traceRows renders one run's node results into the rows and events to persist.
//
// RunIndex is passed through as the runner recorded it. The runner leaves it
// unset on the rows it writes for something other than a run — skipped, failed,
// retried, tolerated by continueOnFail — so a node inside a loop produces
// several rows with the same (node, attempt, run_index). Stamping a counter
// here would depend on this loop seeing every row of the execution, which is
// not true of a resumed run or of a trace written in segments; the store owns
// the key instead, where the whole trace is in scope (CreateNodeRuns).
func (service *Service) traceRows(record execution.Record, nodeTypes map[string]string, seqBase int, runs []NodeRun) ([]traceRow, error) {
	rows := make([]traceRow, 0, len(runs))
	for sequence, run := range runs {
		input, err := json.Marshal(run.Input)
		if err != nil {
			return nil, fmt.Errorf("marshal node %q input: %w", run.NodeID, err)
		}
		output, err := json.Marshal(run.Output)
		if err != nil {
			return nil, fmt.Errorf("marshal node %q output: %w", run.NodeID, err)
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
		sequenceNumber := seqBase + sequence + 1
		eventType := events.NodeCompleted
		// A skipped node reached a terminal state without failing. Publishing
		// node.failed for it would light a pruned branch up as an error on the
		// live canvas; the node-run record carries the skipped status, which is
		// what a reader needs to tell the two apart.
		if status != execution.StatusSucceeded && status != execution.StatusSkipped {
			eventType = events.NodeFailed
		}
		rows = append(rows, traceRow{
			run: execution.NodeRun{
				TenantID: record.TenantID, ExecutionID: record.ID, NodeID: run.NodeID,
				Attempt: attemptOf(run), RunIndex: run.RunIndex, Sequence: sequenceNumber,
				Status: status, Input: input, Output: output, Error: errorPayload,
				Response:  run.Response,
				StartedAt: now, FinishedAt: &now, LeaseOwner: record.LeaseOwner,
			},
			event: events.Event{
				TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
				NodeID: run.NodeID, Type: eventType, Status: status, Sequence: sequenceNumber,
				Data: output,
			},
		})
	}
	return rows, nil
}

// traceWriter persists a run's node rows while the graph is still running.
//
// The runner appends a row per completed node and hands each one over through
// Request.NodeRunSink as it happens; without a writer those rows reach storage
// only in the terminal flush, so a run that takes minutes shows nothing at all
// until it ends and a failure at the end shows nothing even then. The writer
// writes the row and publishes its event immediately, which is what makes the
// canvas and the execution page follow a long run.
//
// Best effort, deliberately: the terminal flush writes the same rows again from
// the result in memory, and that write is idempotent by row identity, so a row
// the writer managed to persist comes back as a duplicate and is not written or
// published twice, while a row it could not persist (a cancelled run context, a
// transient database error) is simply written once by the flush. A failure here
// therefore costs a live update, never the trace.
type traceWriter struct {
	service   *Service
	ctx       context.Context
	tenant    repository.TenantScope
	record    execution.Record
	nodeTypes map[string]string
	seqBase   int
	mu        sync.Mutex
	written   map[int]bool
}

func (service *Service) newTraceWriter(ctx context.Context, tenant repository.TenantScope, record execution.Record, nodeTypes map[string]string, seqBase int) *traceWriter {
	return &traceWriter{
		service: service, ctx: ctx, tenant: tenant, record: record,
		nodeTypes: nodeTypes, seqBase: seqBase, written: make(map[int]bool),
	}
}

// sink persists and publishes one row the runner has just appended.
func (writer *traceWriter) sink(index int, run NodeRun) {
	if writer == nil {
		return
	}
	rows, err := writer.service.traceRows(writer.record, writer.nodeTypes, writer.seqBase+index, []NodeRun{run})
	if err != nil {
		writer.service.log.Debug("live progress row could not be rendered", "execution", writer.record.ID, "node", run.NodeID, "error", err)
		return
	}
	if _, err := writer.service.executions.CreateNodeRun(writer.ctx, writer.tenant, rows[0].run); err != nil {
		writer.service.log.Debug("live progress row could not be persisted", "execution", writer.record.ID, "node", run.NodeID, "error", err)
		return
	}
	writer.mu.Lock()
	writer.written[rows[0].run.Sequence] = true
	writer.mu.Unlock()
	writer.service.publish(rows[0].event)
}

// start publishes a node the runner is about to execute.
//
// The other half of live progress, and the half a reader notices: without it a
// node lights up only when it finishes, so a graph with one slow step looks
// frozen for as long as that step takes. The event carries no sequence and no
// data — nothing about the node is known yet beyond that it is running — which
// is why the reader keys it by node ID.
func (writer *traceWriter) start(nodeID string) {
	if writer == nil || nodeID == "" {
		return
	}
	writer.service.publish(events.Event{
		TenantID: writer.record.TenantID, ExecutionID: writer.record.ID, WorkflowID: writer.record.WorkflowID,
		NodeID: nodeID, Type: events.NodeStarted, Status: execution.StatusRunning,
	})
}

// wrote reports whether the live writer already persisted the row for one
// sequence. The suspend path asks before it writes a segment the runner has
// already handed over, so a suspension neither writes the same row twice nor
// publishes an event a subscriber has seen.
func (writer *traceWriter) wrote(sequence int) bool {
	if writer == nil {
		return false
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.written[sequence]
}

// failPersist settles an execution whose trace could not be written.
//
// Leaving it running is what made a broken trace expensive instead of visible:
// the row kept its lease, the lease expired, the next worker reclaimed the
// execution, deleted the trace and ran the whole graph — side effects included
// — only to fail at the same write. That is how one collision on a run index
// became a workflow that never stopped running (BUG-hfhzq6). Terminal, with the
// reason on the record, is the same failure reported once.
func (service *Service) failPersist(ctx context.Context, tenant repository.TenantScope, record execution.Record, cause error) (bool, error) {
	now := time.Now().UTC()
	record.Status = execution.StatusFailed
	record.Output = json.RawMessage("null")
	record.Error = structuredError("execution.persist_failed", cause)
	record.FinishedAt = &now
	updated, err := service.executions.UpdateRuntime(ctx, tenant, record)
	if err != nil {
		// The fence is gone as well — the row was reclaimed or settled
		// elsewhere — so there is nothing left to mark, and the original cause
		// is the one worth reporting.
		return true, cause
	}
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: events.ExecutionFailed, Status: updated.Status, Data: updated.Error,
	})
	return true, cause
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
			service.workers.Add(1)
			go func() {
				defer service.workers.Done()
				service.worker(ctx, workerID)
			}()
		}
		go service.sweepLoop(ctx)
	})
	return nil
}

// Drain waits for the workers to finish the run each of them is in the middle
// of, bounded by ctx.
//
// Without it, shutdown is a lie the database tells on the next restart: the
// process exits with a run still in flight, the row stays `running`, the lease
// expires, and the workflow runs again from its trigger — every payment,
// message and API call it had already made happening a second time, with the
// final history showing one clean run. Waiting for the workers is what lets
// each of them settle its execution as cancelled and release its lease before
// the process goes away, which is also why the terminal writes in runOnce
// deliberately do not use the cancelled context.
//
// The bound matters as much as the wait: a worker stuck in a node that ignores
// cancellation must not hold shutdown open forever. When ctx expires the
// caller exits anyway, and the lease expiry reclaims whatever was left — the
// same recovery as a crash, which is the honest floor.
func (service *Service) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		service.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
//
// It reads one execution's status and nothing else. Reading the whole record
// meant loading every node-run payload this execution had written, ten times a
// second, for an answer that is one small column — which is the largest,
// dumbest reader of the biggest rows in the database.
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
		_, cancelling, err := service.executions.ExecutionState(pollCtx, tenant, executionID)
		stopPoll()
		if err != nil {
			continue
		}
		if cancelling {
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

func (service *Service) run(ctx context.Context, record execution.Record, document workflow.Document, resume resumeURLs, trace *traceWriter) (Result, error) {
	return service.runWithStack(ctx, record, document, []string{record.WorkflowID}, resume, trace)
}

// runWithStack is run with the call chain that reached this execution.
//
// A top-level run's stack is just itself; a sub-workflow's carries every
// workflow above it, which is what lets the next call refuse a cycle by name.
func (service *Service) runWithStack(ctx context.Context, record execution.Record, document workflow.Document, stack []string, resume resumeURLs, trace *traceWriter) (Result, error) {
	ir, err := workflow.Compile(document, service.catalog)
	if err != nil {
		return Result{}, err
	}
	item, err := inputItem(record.Input)
	if err != nil {
		return Result{}, err
	}
	request := service.newRequest(record, document, stack, resume, trace)
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

// runErrorWorkflow starts the workflow a failed run names as its error
// workflow, if it names one.
//
// n8n's settings.errorWorkflow: a workflow carries the ID of another workflow
// to run when it fails, and that workflow starts from an Error Trigger with the
// error. Best effort and logged, deliberately: an error workflow that cannot be
// started — it was deleted, it is not active, it fails itself — must not change
// the outcome of the execution that already failed, and it must not turn a
// failed execution into a worker that reports an error nobody can act on.
func (service *Service) runErrorWorkflow(ctx context.Context, tenant repository.TenantScope, record execution.Record, document workflow.Document, result Result, code string, runErr error) {
	target := errorWorkflowID(document)
	if target == "" {
		return
	}
	if target == record.WorkflowID {
		// A workflow naming itself would recurse: the error run fails, which
		// starts the error run again. Refused here rather than left to the call
		// stack, because this is a configuration mistake worth naming.
		service.log.Warn("error workflow names its own workflow; not starting it",
			"execution", record.ID, "workflow", record.WorkflowID)
		return
	}
	item := errorWorkflowItem(record, document, result, code, runErr)
	if service.errorTriggerType == "" {
		service.log.Warn("no error trigger type is configured; the error workflow starts from every root",
			"execution", record.ID, "errorWorkflow", target)
	}
	invoked, err := service.invokeWorkflow(ctx, ExecutionContext{
		ID: record.ID, Mode: string(record.Trigger), TenantID: record.TenantID,
		WorkflowID: record.WorkflowID, Stack: []string{record.WorkflowID},
	}, WorkflowCall{WorkflowID: target, Items: []workflow.Item{item}}, service.errorTriggerSelector)
	if err != nil {
		service.log.Error("error workflow failed", "execution", record.ID, "errorWorkflow", target, "error", err)
		return
	}
	service.log.Info("error workflow started", "execution", record.ID, "errorWorkflow", target, "errorExecution", invoked.ExecutionID)
}

// errorTriggerSelector names the node an error workflow starts from.
func (service *Service) errorTriggerSelector(document workflow.Document) string {
	if service.errorTriggerType == "" {
		return ""
	}
	for _, node := range document.Nodes {
		if node.Type == service.errorTriggerType {
			return node.ID
		}
	}
	return ""
}

// errorWorkflowID reads the workflow named in the document's settings.
//
// The setting is n8n's, and it is a workflow ID rather than a name: the
// imported documents carry exactly that, and resolving a name would make the
// error workflow a workflow somebody can rename into silence.
func errorWorkflowID(document workflow.Document) string {
	declared, _ := document.Settings[errorWorkflowSetting].(string)
	return strings.TrimSpace(declared)
}

// errorWorkflowSetting names the document setting holding the error workflow.
const errorWorkflowSetting = "errorWorkflow"

// errorWorkflowItem builds the item an Error Trigger receives.
//
// The shape is n8n's, because the workflow on the other side is usually one
// imported from n8n and reads `{{ $json.execution.error.message }}`,
// `{{ $json.execution.id }}` and `{{ $json.workflow.name }}`. Renaming or
// flattening those keys would make every imported error workflow silently
// report nothing, which is worse than a failure it cannot report.
func errorWorkflowItem(record execution.Record, document workflow.Document, result Result, code string, runErr error) workflow.Item {
	message := "the workflow failed"
	if runErr != nil {
		message = runErr.Error()
	}
	errorDetail := map[string]any{"message": message, "code": code, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	lastNode := ""
	for _, run := range result.NodeRuns {
		if run.Error == nil {
			continue
		}
		lastNode = run.NodeID
		for _, node := range document.Nodes {
			if node.ID == run.NodeID {
				errorDetail["node"] = map[string]any{"id": node.ID, "name": node.Name, "type": node.Type}
				break
			}
		}
	}
	// Both modes report how the failed execution was started: the error run is
	// a consequence of that trigger, and an error workflow that branches on
	// "was this a webhook or a schedule" is reading the run that failed.
	return workflow.Item{JSON: map[string]any{
		"execution": map[string]any{
			"id":               record.ID,
			"mode":             string(record.Trigger),
			"lastNodeExecuted": lastNode,
			"error":            errorDetail,
		},
		"workflow": map[string]any{"id": record.WorkflowID, "name": document.Name},
		"trigger":  map[string]any{"mode": string(record.Trigger)},
	}}
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
	return service.invokeWorkflow(ctx, parent, call, service.subworkflowTrigger)
}

// invokeWorkflow runs another workflow inline, starting from the root the
// selector names.
//
// The selector is the caller's: a sub-workflow call starts from the workflow's
// own sub-workflow trigger, while an error workflow starts from its Error
// Trigger, and both must be confined to that branch — a workflow carrying a
// webhook beside the trigger it was called for must not also post to it.
func (service *Service) invokeWorkflow(ctx context.Context, parent ExecutionContext, call WorkflowCall, selectTrigger func(workflow.Document) string) (WorkflowCallResult, error) {
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
		Input: input, SelectTrigger: selectTrigger,
		LeaseOwner: service.workerID, LeaseUntil: time.Now().UTC().Add(service.leaseDurationOrDefault()),
	})
	if err != nil {
		return WorkflowCallResult{}, err
	}
	// A child runs inline in this goroutine but is still a durable execution
	// with a lease of its own, and a workflow whose executionTimeout is longer
	// than the lease would otherwise outlive its own claim mid-call and be
	// reclaimed by a worker that runs it in parallel with this one. Renewed the
	// way a top-level run is, for the run and the trace write after it. No
	// cancellation on a lost fence: this goroutine has no cancel of its own to
	// call — the caller's context belongs to the parent — and the child's
	// writes are fenced by the lease, so a stolen child cannot write over its
	// successor's trace either way.
	heartbeat := service.startLeaseHeartbeat(ctx, tenant, record, nil)
	defer heartbeat.Stop()
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: events.ExecutionStarted, Status: execution.StatusRunning,
	})

	// No live-progress writer for a child: its trace is written by persistChild
	// with its own sequencing once the call returns, so there is no window to
	// fill, and publishing a child's nodes onto the parent's live feed from
	// here would attribute them to the wrong execution.
	result, runErr := service.runWithStack(ctx, record, document, append(append([]string(nil), parent.Stack...), target), resumeURLs{}, nil)
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
	// One transaction, like the top-level trace: a sub-workflow is a whole
	// execution and its child lease has to survive its trace write too.
	rows := make([]execution.NodeRun, 0, len(result.NodeRuns))
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
		rows = append(rows, execution.NodeRun{
			TenantID: record.TenantID, ExecutionID: record.ID, NodeID: run.NodeID,
			Attempt: attemptOf(run), RunIndex: run.RunIndex, Sequence: sequence + 1,
			Status: status, Input: input, Output: output, Error: errorPayload,
			Response:  run.Response,
			StartedAt: now, FinishedAt: &now, LeaseOwner: record.LeaseOwner,
		})
	}
	if _, err := service.executions.CreateNodeRuns(ctx, tenant, rows); err != nil {
		// The child is a durable execution with a lease of its own, so a trace it
		// cannot write is settled the way a top-level run's is: left running, a
		// reclaim would run the child's graph — and its side effects — a second
		// time to fail at the same write. The cause is returned either way, so
		// the node that called it fails with the same reason.
		cause := fmt.Errorf("persist sub-workflow trace: %w", err)
		_, _ = service.failPersist(ctx, tenant, record, cause)
		return cause
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

// ExecutionTimeoutSetting is the document setting holding a workflow's own run
// budget in seconds, under n8n's name for it so an imported workflow keeps
// meaning what it meant. A negative value is n8n's "no execution timeout".
const ExecutionTimeoutSetting = "executionTimeout"

// runBudget bounds one run by the budget its workflow asked for.
//
// Every execution used to get the instance's default timeout whatever the
// workflow said, so an imported workflow declaring five minutes died at sixty
// seconds on the first slow call. The workflow's own setting wins, capped by
// the instance ceiling when one is configured — which is n8n's own precedence
// (settings.executionTimeout against EXECUTIONS_TIMEOUT_MAX). A workflow that
// names nothing still gets the instance default, so the bound is never absent
// by accident.
func (service *Service) runBudget(ctx context.Context, document workflow.Document) (context.Context, context.CancelFunc) {
	seconds, found := settingNumber(document.Settings[ExecutionTimeoutSetting])
	if !found || seconds == 0 {
		return context.WithTimeout(ctx, service.defaultTimeout)
	}
	if seconds < 0 {
		// No timeout at all, which is what n8n's default install runs with.
		// Nothing is left unbounded by it: the run still ends on cancellation,
		// on a node's own timeout, and on the worker lease's fence.
		return context.WithCancel(ctx)
	}
	budget := time.Duration(seconds * float64(time.Second))
	if service.maxTimeout > 0 && budget > service.maxTimeout {
		budget = service.maxTimeout
	}
	return context.WithTimeout(ctx, budget)
}
