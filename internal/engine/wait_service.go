// Durable wait integration: the service side of suspend, resume and sweep.
//
// The mechanism (token shape, checkpoint codec, refusal vocabulary) lives in
// approval.go, checkpoint.go and internal/repository/waits.go. This file is
// the wiring between them: catching SuspendError at the runOnce boundary,
// persisting the trace produced so far, parking the execution as waiting,
// continuing a re-queued run from its checkpoint, settling expired waits on a
// period, and waking workers when PostgreSQL announces queued work.
//
// A wait row is the only durable state. Nothing here keeps a suspended run in
// memory, so a suspended execution survives a process restart by construction:
// the next process finds a waiting row with a checkpoint and no lease, and a
// resume (HTTP or sweep) re-queues it like any other queued execution.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// ResumePrefix is the HTTP prefix the per-execution resume URLs live under.
// It is served beside /webhook, and never modelled as a webhook binding: the
// bindings route index is unique on (method, path), so every suspended
// execution contending for one route would collide there.
const ResumePrefix = "/resume"

// ApprovalPrefix is the dashboard page that records an approval decision and
// then calls the resume URL on the decider's behalf. A workflow that wants to
// send a human a link sends this one; the resume URL itself is the machine
// surface the page calls.
const ApprovalPrefix = "/approve"

// NewResumeToken mints one opaque single-use resume token: 16 bytes of
// entropy, base64url-encoded, like the webhook route segments elsewhere in
// this codebase. Unguessable in practice, short enough for a URL tapped on a
// phone. The token travels in the URL; only its SHA-256 travels in indexes.
func NewResumeToken() (string, error) {
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("mint resume token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(entropy), nil
}

// ResumePath renders the machine resume URL path for one token.
func ResumePath(token string) string {
	return ResumePrefix + "/" + token
}

// ApprovalPath renders the human approval page path for one token.
func ApprovalPath(token string) string {
	return ApprovalPrefix + "/" + token
}

// ResumeURL prefixes the resume path with the instance's public base URL.
// Empty base renders a path-only link, which is all a same-origin dashboard
// and approval page need.
func (service *Service) ResumeURL(token string) string {
	return service.publicBaseURL + ResumePath(token)
}

// ApprovalURL prefixes the approval page path the same way.
func (service *Service) ApprovalURL(token string) string {
	return service.publicBaseURL + ApprovalPath(token)
}

// resumeURLs are the links one runOnce claim hands to its run. The token is
// minted before the graph runs so a workflow can compose its own resume link
// — $execution.resumeUrl — and send it before suspending. A run that never
// suspends discards its token: the URL was never backed by a wait row, so it
// answers 404 rather than resuming anything.
type resumeURLs struct {
	token       string
	resumeURL   string
	approvalURL string
}

// mintResumeURLs mints the token one claim will suspend with, if it suspends.
func (service *Service) mintResumeURLs() (resumeURLs, error) {
	token, err := NewResumeToken()
	if err != nil {
		return resumeURLs{}, err
	}
	return resumeURLs{token: token, resumeURL: service.ResumeURL(token), approvalURL: service.ApprovalURL(token)}, nil
}

// WatchQueue LISTENs for queued executions and wakes an idle worker for each
// notification. The 100 ms poll stays the fallback: a dropped notification
// costs latency, never a stuck execution. SQLite has no LISTEN/NOTIFY — call
// this on PostgreSQL only; elsewhere it would only report dial errors while
// the tick does the work.
func (service *Service) WatchQueue(ctx context.Context, dsn, tablePrefix string, onError func(error)) error {
	return repository.WatchExecutions(ctx, dsn, tablePrefix, func(repository.ExecutionWake) {
		service.Wake()
	}, onError)
}

// sweepLoop settles expired waits on the service's sweep interval until ctx
// is done, and owns the exact timers the suspensions arm. There is no knob to
// disable it: without the sweep a forgotten approval would stay suspended
// forever, and a wait suspended by a process that then exited would have
// nobody left to notice its deadline.
func (service *Service) sweepLoop(ctx context.Context) {
	service.waitTimers.attach(ctx)
	defer service.waitTimers.detach()
	interval := service.sweepInterval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := service.SweepWaits(ctx); err != nil {
				service.log.Error("sweeping expired waits", "error", err)
			}
		}
	}
}

// waitTimers holds the exact wake-ups one process armed for the waits it
// suspended.
//
// A suspended wait used to resume only when the periodic sweep noticed its
// deadline, so a two-second pause took up to a minute — the whole point of
// suspending (releasing the worker) was paid for in latency, and a webhook
// whose workflow waited always answered 504 before the wait ever finished.
// Each suspension therefore arms a timer for its own deadline, and the sweep
// stays as the floor: a process that exits with waits outstanding costs the
// next process's sweep interval, never a stuck execution.
type waitTimers struct {
	mu      sync.Mutex
	ctx     context.Context
	stopped bool
	armed   map[*time.Timer]struct{}
	// afterFunc schedules one wake-up; nil means time.AfterFunc. Tests set it
	// through the unexported seam in export_test.go so they own when a wait's
	// deadline fires instead of waiting on the wall clock.
	afterFunc func(time.Duration, func()) *time.Timer
}

// attach binds the timer set to the sweep loop's lifetime. Called once per
// process by Start; a Service used without Start (a single RunOnce, a test)
// leaves the set unattached and each timer carries its own bounded context.
func (timers *waitTimers) attach(ctx context.Context) {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	timers.ctx = ctx
	timers.stopped = false
	if timers.armed == nil {
		timers.armed = make(map[*time.Timer]struct{})
	}
}

// detach stops every armed timer and refuses new ones. Races with the
// shutdown that ends the sweep loop, which is when it runs.
func (timers *waitTimers) detach() {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	timers.stopped = true
	timers.ctx = nil
	for timer := range timers.armed {
		timer.Stop()
	}
	timers.armed = nil
}

// context returns the lifetime an armed timer settles inside, or nil when this
// process never started a sweep loop.
func (timers *waitTimers) context() context.Context {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	return timers.ctx
}

// arm schedules one wake-up after the given delay.
func (timers *waitTimers) arm(after time.Duration, fire func()) {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	if timers.stopped {
		return
	}
	if timers.armed == nil {
		// A Service used without Start: the timers still have to fire.
		timers.armed = make(map[*time.Timer]struct{})
	}

	// The handle reaches its own callback through a channel rather than being
	// captured directly. The scheduler may run the callback before it
	// returns — and the delay here is zero whenever a wait has already
	// expired — so a closure that reads the variable this function is still
	// assigning is a data race, which is exactly what the detector reports
	// when an expired wait and a running sweep loop overlap. The callback's
	// receive cannot happen before the send below, and the send happens after
	// the assignment, which is what orders the two.
	handle := make(chan *time.Timer, 1)
	schedule := timers.afterFunc
	if schedule == nil {
		schedule = time.AfterFunc
	}
	timer := schedule(after, func() {
		timers.forget(<-handle)
		fire()
	})
	handle <- timer
	timers.armed[timer] = struct{}{}
}

func (timers *waitTimers) forget(timer *time.Timer) {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	delete(timers.armed, timer)
}

// clock reads the service's time source. Every wait deadline is validated,
// swept and armed against this one call, so production reads the wall clock
// and a test can freeze or move it through the unexported seam in
// export_test.go. Reading time.Now directly at each site is what let a
// loaded machine slip a 50 ms deadline into the past between the point a
// test minted it and the point suspend validated it.
func (service *Service) clock() time.Time {
	if service.now != nil {
		return service.now()
	}
	return time.Now()
}

// armWaitTimer arms the exact wake-up for one suspension. The delay is taken
// from the deadline the wait row was written with, so the wake-up and the row
// can never disagree about when the wait ends.
func (service *Service) armWaitTimer(expiresAt time.Time) {
	delay := expiresAt.Sub(service.clock())
	if delay < 0 {
		delay = 0
	}
	service.waitTimers.arm(delay, service.settleDueWaits)
}

// settleDueWaits runs the sweep for one armed timer. It uses the sweep loop's
// lifetime when there is one, so the work a process still owes is cancelled by
// the shutdown that stops the loop; without a loop the timer carries its own
// bounded context, which is what a single RunOnce in a test needs.
func (service *Service) settleDueWaits() {
	ctx := service.waitTimers.context()
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
	}
	if _, err := service.SweepWaits(ctx); err != nil {
		service.log.Error("settling a due wait", "error", err)
	}
}

// SweepWaits settles every unconsumed wait past its deadline: timer waits
// (interval, until) re-queue and continue past the suspending node, approval
// and webhook waits fail by name so no execution stays suspended forever. A
// wait another call already consumed reports settled, not failed: the sweeper
// moves on rather than failing an execution twice.
func (service *Service) SweepWaits(ctx context.Context) (int, error) {
	now := service.clock().UTC()
	waits, err := service.executions.ListExpiredWaits(ctx, now, 0)
	if err != nil {
		return 0, fmt.Errorf("list expired waits: %w", err)
	}
	settled := 0
	requeued := false
	for _, wait := range waits {
		resolution := ExpiredResolution{FailCode: repository.WaitExpiredCode}
		if wait.Mode == WaitModeInterval || wait.Mode == WaitModeUntil {
			output, err := timerResumeOutput(wait.Checkpoint)
			if err != nil {
				// A checkpoint that cannot be read cannot be continued: fail
				// the wait rather than re-queueing a run that would crash.
				service.log.Error("expired timer wait has an unreadable checkpoint; failing it",
					"execution", wait.ExecutionID, "node", wait.NodeID, "error", err)
			} else {
				resolution = ExpiredResolution{Requeue: true, ResumeOutput: output}
			}
		}
		_, record, err := service.executions.SettleExpiredWait(ctx, wait.ID, repository.ExpiredResolution{
			Requeue:      resolution.Requeue,
			ResumeOutput: resolution.ResumeOutput,
			FailCode:     resolution.FailCode,
		}, now)
		if err != nil {
			if errors.Is(err, repository.ErrWaitConsumed) || errors.Is(err, repository.ErrWaitNotFound) {
				continue
			}
			return settled, fmt.Errorf("settle expired wait %d: %w", wait.ID, err)
		}
		settled++
		if resolution.Requeue {
			requeued = true
			continue
		}
		service.publish(events.Event{
			TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
			Type: events.ExecutionFailed, Status: record.Status, Data: record.Error,
		})
	}
	if requeued {
		service.Wake()
	}
	return settled, nil
}

// ExpiredResolution is the service's reading of one expired wait. It mirrors
// repository.ExpiredResolution so the sweeper reasons in engine vocabulary
// and converts at the boundary.
type ExpiredResolution struct {
	Requeue      bool
	ResumeOutput json.RawMessage
	FailCode     string
}

// timerResumeOutput synthesizes the suspending node's output for an expired
// timer wait: its input items pass through unchanged, like the in-process
// wait node does. The checkpoint input is keyed by port; the resume carries
// the main port's items on the node's single output.
func timerResumeOutput(checkpointRaw []byte) (json.RawMessage, error) {
	checkpoint, err := unmarshalCheckpoint(checkpointRaw)
	if err != nil {
		return nil, err
	}
	items := checkpoint.Input["main"]
	if items == nil {
		items = []workflow.Item{}
	}
	return json.Marshal(workflow.NodeOutput{items})
}

// approvalResumeOutput renders a human decision as the suspending node's
// output: one item carrying {approved, respondedAt, decidedBy, note}, matching
// the shape n8n's approval nodes emit so an imported workflow branches the
// same way here. Single-port: adapted to the node's port count at resume time.
func approvalResumeOutput(decision ApprovalDecision) (json.RawMessage, error) {
	return json.Marshal(workflow.NodeOutput{{workflow.Item{JSON: decision.Output()}}})
}

// loadResumeState returns the checkpoint a re-queued run resumes from, or nil
// for a fresh execution. Anything but not-found is a real error: claiming an
// execution whose wait row cannot be read must fail loudly rather than run
// the graph from the start and fire every non-idempotent node twice.
func (service *Service) loadResumeState(ctx context.Context, tenant repository.TenantScope, executionID string) (*repository.Wait, error) {
	wait, err := service.executions.LoadResumeState(ctx, tenant, executionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load resume state: %w", err)
	}
	return &wait, nil
}

// resumeRun continues a re-queued execution from its checkpoint: the
// suspending node completes with the stored resume output, then the same pass
// Run would have taken. Nodes completed before suspension never execute
// again — their outputs arrive in the checkpoint, not from a second run.
func (service *Service) resumeRun(ctx context.Context, record execution.Record, document workflow.Document, stack []string, wait repository.Wait, resume resumeURLs, trace *traceWriter) (Result, error) {
	ir, err := service.compile(record, document)
	if err != nil {
		return Result{}, err
	}
	checkpoint, err := unmarshalCheckpoint(wait.Checkpoint)
	if err != nil {
		return Result{}, err
	}
	output, err := adaptStoredResumeOutput(ir, checkpoint.SuspendNode, wait.ResumeOutput)
	if err != nil {
		return Result{}, err
	}
	request := service.newRequest(record, document, stack, resume, trace)
	// The resumed run starts from the same trigger with the same input: the
	// checkpoint carries the graph state, the record carries the entry.
	item, err := inputItem(record.Input)
	if err != nil {
		return Result{}, err
	}
	request.Input = item
	request.TriggerNodeID = record.TriggerNodeID
	return service.runner.Resume(ctx, ir, request, checkpoint, output)
}

// adaptStoredResumeOutput fits one stored resume output to its node's port
// count. Stored single-port (the shape both the approval surface and the
// timer sweeper write); padded with empty ports when the node declares more.
// Truncation never happens silently: extra ports are refused rather than
// dropped.
func adaptStoredResumeOutput(ir workflow.IR, nodeID string, raw json.RawMessage) (workflow.NodeOutput, error) {
	var output workflow.NodeOutput
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, fmt.Errorf("decode resume output: %w", err)
	}
	want := 1
	for _, node := range ir.Nodes {
		if node.ID == nodeID {
			want = len(node.Definition.Outputs)
			break
		}
	}
	if want < 1 {
		want = 1
	}
	if len(output) == want {
		return output, nil
	}
	if len(output) > want {
		return nil, fmt.Errorf("resume output has %d streams, node %q declares %d", len(output), nodeID, want)
	}
	for len(output) < want {
		output = append(output, []workflow.Item{})
	}
	return output, nil
}

// newRequest builds the runner request runWithStack and resumeRun share,
// including the execution links a workflow may compose before suspending.
func (service *Service) newRequest(record execution.Record, document workflow.Document, stack []string, resume resumeURLs, trace *traceWriter) Request {
	request := Request{
		Execution: ExecutionContext{
			ID: record.ID, Mode: string(record.Trigger),
			TenantID: record.TenantID, WorkflowID: record.WorkflowID,
			ParentID: record.ParentExecutionID, Stack: stack,
			ResumeURL: resume.resumeURL, ApprovalURL: resume.approvalURL,
		},
		Workflow:    service.workflowContext(document),
		Workflows:   service,
		StaticData:  service.staticDataFor(record),
		Env:         service.environment,
		Credentials: &tenantCredentials{store: service.credentials, tenant: repository.TenantScope{ID: record.TenantID}},
		Events: func(event NodeEvent) {
			service.publish(events.Event{
				TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
				NodeID: event.NodeID, Type: events.Type(event.Name), Data: event.Detail,
			})
		},
	}
	// Live progress for a top-level run. A nil writer (a sub-workflow call)
	// leaves the sink off, because a child's trace is written by persistChild
	// once the call returns.
	if trace != nil {
		request.NodeRunSink = trace.sink
		request.NodeStartSink = trace.start
	}
	return request
}

// workflowTimezoneSetting names the document setting holding a workflow's own
// IANA zone. The scheduler package owns the same name for the schedule half of
// it (scheduler.WorkflowTimezoneSetting); the two read one setting, and a
// rename has to move both.
const workflowTimezoneSetting = "timezone"

// workflowContext backs `$workflow` and the clock expressions read.
//
// `$workflow.id`, `.name` and `.active` were empty and false on every run
// because nothing ever filled this field, which 27 parameters across the
// imported corpus read. Active is true for the run in flight: an execution was
// queued from a version of this workflow and is running it now, so a run that
// exists is a run of a workflow in use. The durable flag on the workflow row
// answers a different question — whether something will start the workflow
// next — and reading it here would need a lookup per run.
//
// The zone is left empty when the document names a real one, because the
// runner fills it from the compiled settings and two places writing it would
// eventually disagree. Only the instance default is supplied from here, which
// is the half the document cannot know: n8n's own resolution of an absent zone
// and of the DEFAULT sentinel is the instance's GENERIC_TIMEZONE.
func (service *Service) workflowContext(document workflow.Document) expression.WorkflowContext {
	context := expression.WorkflowContext{ID: document.ID, Name: document.Name, Active: true}
	declared, _ := document.Settings[workflowTimezoneSetting].(string)
	if trimmed := strings.TrimSpace(declared); trimmed != "" && !strings.EqualFold(trimmed, "DEFAULT") {
		return context
	}
	context.Timezone = service.defaultTimezone
	return context
}

// validateSuspend refuses a suspension the service cannot honour. A missing
// mode, a deadline in the past, or a wait past the maximum never parks an
// execution: the run fails loudly instead, so a misconfigured wait is an
// error an author can see rather than a suspension nobody can resume.
func validateSuspend(suspended *SuspendError, now time.Time) (time.Time, error) {
	if suspended == nil {
		return time.Time{}, fmt.Errorf("no suspension to honour")
	}
	mode := suspended.Mode
	if mode == "" && suspended.Ticket != nil {
		mode = suspended.Ticket.Mode
	}
	switch mode {
	case WaitModeApproval, WaitModeInterval, WaitModeUntil, WaitModeWebhook:
	default:
		return time.Time{}, fmt.Errorf("node %q asked to wait without a wait mode", suspended.NodeID)
	}
	expiresAt := suspended.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(DefaultApprovalTTL)
	}
	if !expiresAt.After(now) {
		return time.Time{}, fmt.Errorf("%w: the deadline is in the past", ErrWaitExpired)
	}
	if expiresAt.Sub(now) > MaxWaitTTL {
		return time.Time{}, fmt.Errorf("wait of %s exceeds the maximum of %s",
			expiresAt.Sub(now).Truncate(time.Second), MaxWaitTTL)
	}
	if len(suspended.Checkpoint) == 0 {
		return time.Time{}, fmt.Errorf("node %q suspension carries no checkpoint", suspended.NodeID)
	}
	return expiresAt.UTC(), nil
}

// suspend parks a running execution as waiting: the trace produced so far is
// persisted first (a workflow that waits four hours must show four hours of
// evidence, not an empty inspector), then the wait row is written and the
// lease released in one transaction, so a crash between the two cannot leave
// a wait nobody owns or an execution that looks crashed while its checkpoint
// sits elsewhere.
//
// Webhook-trigger interplay, decided: a run that suspends answers its trigger
// per the binding's response mode, and the approval outcome is always
// delivered by whatever the workflow does after it resumes, never by the
// trigger response. The default immediate mode already answered at queue
// time, so suspension changes nothing there. lastNode/responseNode hold the
// trigger request until a terminal state; waiting is not terminal
// (webhook.terminal), so those answer 504 at ResponseTimeout while the
// execution waits durably.
func (service *Service) suspend(ctx context.Context, tenant repository.TenantScope, record execution.Record, document workflow.Document, result Result, suspended *SuspendError, seqBase int, resume resumeURLs, trace *traceWriter) (bool, error) {
	now := service.clock().UTC()
	expiresAt, err := validateSuspend(suspended, now)
	if err != nil {
		return false, err
	}
	token := resume.token
	if suspended.Ticket != nil && strings.TrimSpace(suspended.Ticket.Token) != "" {
		token = suspended.Ticket.Token
	}
	if strings.TrimSpace(token) == "" {
		return false, fmt.Errorf("node %q suspension carries no resume token", suspended.NodeID)
	}
	nodeTypes := make(map[string]string, len(document.Nodes))
	for _, node := range document.Nodes {
		nodeTypes[node.ID] = node.Type
	}
	for index, run := range result.NodeRuns {
		// Already handed over by the runner and written by the live writer: the
		// row is in the table and its event has been published, so writing it
		// here would publish it a second time.
		if trace.wrote(seqBase + index + 1) {
			continue
		}
		if err := service.persistTraceRow(ctx, tenant, record, nodeTypes, run, seqBase+index+1); err != nil {
			return true, err
		}
		service.publishTraceEvent(record, run, seqBase+index+1)
	}
	mode := suspended.Mode
	if mode == "" && suspended.Ticket != nil {
		mode = suspended.Ticket.Mode
	}
	wait, suspendedRecord, err := service.executions.SuspendExecution(ctx, tenant, repository.SuspendWaitParams{
		ExecutionID: record.ID, LeaseOwner: record.LeaseOwner,
		WorkflowID: record.WorkflowID, NodeID: suspended.NodeID, Mode: mode,
		TokenHash: repository.HashWaitToken(token), ResumeToken: token,
		Checkpoint: append([]byte(nil), suspended.Checkpoint...),
		RunCount:   seqBase + len(result.NodeRuns),
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		return true, fmt.Errorf("park execution as waiting: %w", err)
	}
	// What the half before the wait changed is kept now, as n8n keeps it,
	// so the half that resumes loads it.
	service.saveStaticData(ctx, suspendedRecord, result.StaticData)
	// The row carries the deadline, and this process arms the exact wake-up
	// for it: waiting for the periodic sweep instead made a two-second pause
	// cost up to a minute and made every webhook whose workflow waits answer
	// 504 before the wait ever ended.
	service.armWaitTimer(wait.ExpiresAt)
	data, _ := json.Marshal(map[string]any{
		"nodeId":    wait.NodeID,
		"mode":      wait.Mode,
		"expiresAt": wait.ExpiresAt.UTC().Format(time.RFC3339),
	})
	// The waiting event carries the node and the deadline — never the token
	// and never the payload, both of which would put run data on a live feed.
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		NodeID: wait.NodeID, Type: EventExecutionWaiting, Status: execution.StatusWaiting, Data: data,
	})
	return true, nil
}

// persistTraceRow writes one node-run row of a suspended segment. It mirrors
// the runOnce trace loop without rewriting it: the hot path keeps its shape,
// and the suspend path stays readable beside it.
func (service *Service) persistTraceRow(ctx context.Context, tenant repository.TenantScope, record execution.Record, nodeTypes map[string]string, run NodeRun, sequence int) error {
	input, err := json.Marshal(run.Input)
	if err != nil {
		return fmt.Errorf("marshal node %q input: %w", run.NodeID, err)
	}
	output, err := json.Marshal(run.Output)
	if err != nil {
		return fmt.Errorf("marshal node %q output: %w", run.NodeID, err)
	}
	output = projectTrace(nodeTypes[run.NodeID], output)
	status := execution.StatusSucceeded
	if run.Skipped {
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
	if _, err := service.executions.CreateNodeRun(ctx, tenant, execution.NodeRun{
		TenantID: record.TenantID, ExecutionID: record.ID, NodeID: run.NodeID, Attempt: attemptOf(run), RunIndex: run.RunIndex, Sequence: sequence,
		Status: status, Input: input, Output: output, Error: errorPayload, Response: run.Response, Console: run.Console, StartedAt: now, FinishedAt: &now, LeaseOwner: record.LeaseOwner,
	}); err != nil {
		return fmt.Errorf("persist node %q run: %w", run.NodeID, err)
	}
	return nil
}

// publishTraceEvent emits the node event for one persisted suspend-segment
// row, after it is durable — like the runOnce loop, never before.
func (service *Service) publishTraceEvent(record execution.Record, run NodeRun, sequence int) {
	status := execution.StatusSucceeded
	if run.Skipped {
		status = execution.StatusSkipped
	}
	if run.Error != nil {
		status = execution.StatusFailed
	}
	eventType := events.NodeCompleted
	if status != execution.StatusSucceeded && status != execution.StatusSkipped {
		eventType = events.NodeFailed
	}
	output, _ := json.Marshal(run.Output)
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		NodeID: run.NodeID, Type: eventType, Status: status, Sequence: sequence,
		Data: output,
	})
}

// WaitInfo answers the approval page: who waits, on what, until when. The
// checkpoint never leaves on this surface — it is run data, and this call is
// answered to whoever holds the token link. A caller tenant that does not own
// the wait answers exactly like an unknown token.
func (service *Service) WaitInfo(ctx context.Context, callerTenant, token string) (repository.Wait, error) {
	if strings.TrimSpace(token) == "" {
		return repository.Wait{}, repository.ErrWaitNotFound
	}
	wait, err := service.executions.FindWaitByToken(ctx, repository.HashWaitToken(token))
	if err != nil {
		return repository.Wait{}, err
	}
	if callerTenant != "" && wait.TenantID != callerTenant {
		return repository.Wait{}, repository.ErrWaitNotFound
	}
	return wait, nil
}

// ResumeApproval records a human decision and re-queues its execution.
// Refused to embedded sessions before consuming, so a denied call stays
// resumable; every other refusal mirrors the repository vocabulary.
func (service *Service) ResumeApproval(ctx context.Context, callerTenant, token string, decision ApprovalDecision, viaEmbed bool) (repository.Wait, execution.Record, error) {
	if viaEmbed {
		return repository.Wait{}, execution.Record{}, ErrWaitEmbedDenied
	}
	output, err := approvalResumeOutput(decision)
	if err != nil {
		return repository.Wait{}, execution.Record{}, err
	}
	return service.resumeWithOutput(ctx, callerTenant, token, output)
}

// ResumeCall resumes a webhook-mode wait with the caller's payload as the
// suspending node's output. Embed denial applies here too: resuming someone
// else's wait from inside a host page is the confused-deputy shape the embed
// restriction exists to stop.
func (service *Service) ResumeCall(ctx context.Context, callerTenant, token string, output json.RawMessage, viaEmbed bool) (repository.Wait, execution.Record, error) {
	if viaEmbed {
		return repository.Wait{}, execution.Record{}, ErrWaitEmbedDenied
	}
	if len(output) == 0 {
		output = json.RawMessage(`[[{"json":{}}]]`)
	}
	var decoded workflow.NodeOutput
	if err := json.Unmarshal(output, &decoded); err != nil {
		return repository.Wait{}, execution.Record{}, fmt.Errorf("decode resume output: %w", err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return repository.Wait{}, execution.Record{}, fmt.Errorf("encode resume output: %w", err)
	}
	return service.resumeWithOutput(ctx, callerTenant, token, encoded)
}

func (service *Service) resumeWithOutput(ctx context.Context, callerTenant, token string, output json.RawMessage) (repository.Wait, execution.Record, error) {
	if strings.TrimSpace(token) == "" {
		return repository.Wait{}, execution.Record{}, repository.ErrWaitNotFound
	}
	wait, record, err := service.executions.ResumeWait(ctx, callerTenant, repository.HashWaitToken(token), output, service.clock().UTC())
	if err != nil {
		return repository.Wait{}, execution.Record{}, err
	}
	service.Wake()
	return wait, record, nil
}

// refuseSuspendInChild turns a wait inside a called sub-workflow into a clear
// failure. The child runs inline in its parent's worker: suspending it would
// snapshot the parent at the calling node and re-execute the child's
// completed nodes on resume — exactly the double-execute the checkpoint
// exists to prevent. A wait belongs in the top-level workflow, not in a
// workflow another workflow calls.
func refuseSuspendInChild(target string, suspended *SuspendError) error {
	node := ""
	if suspended != nil {
		node = suspended.NodeID
	}
	return fmt.Errorf("sub-workflow %q cannot wait: node %q asked to suspend inside a called workflow; move the wait into the calling workflow", target, node)
}

// expressionExecution exposes the service's execution links to the expression
// engine. The runner fills NodeOutputs/NodeItems as the graph progresses;
// these three never change mid-run.
func expressionExecution(record execution.Record, resume resumeURLs) expression.ExecutionContext {
	return expression.ExecutionContext{
		ID:          record.ID,
		Mode:        string(record.Trigger),
		ResumeURL:   resume.resumeURL,
		ApprovalURL: resume.approvalURL,
	}
}

// WaitingLinks returns the resume links for a waiting execution: the machine
// resume URL and the human approval page for its active token. It reports
// false when no unconsumed wait holds the execution. The read surface uses
// it to enrich a waiting execution; the checkpoint travels only on the
// resume path, never here.
func (service *Service) WaitingLinks(ctx context.Context, tenant repository.TenantScope, executionID string) (resumeURL, approvalURL string, ok bool) {
	wait, err := service.executions.FindActiveWait(ctx, tenant, executionID)
	if err != nil {
		return "", "", false
	}
	if strings.TrimSpace(wait.ResumeToken) == "" {
		return "", "", false
	}
	return service.ResumeURL(wait.ResumeToken), service.ApprovalURL(wait.ResumeToken), true
}
