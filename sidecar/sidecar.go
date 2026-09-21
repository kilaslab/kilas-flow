package sidecar

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Named failure codes. Every one fails the node run carrying it, never the
// execution around it and never the host process.
const (
	CodeNoSidecar         = "no-sidecar"
	CodeSpawnFailed       = "spawn-failed"
	CodeTenantRequired    = "tenant-required"
	CodeSidecarCrash      = "sidecar-crash"
	CodeSidecarTimeout    = "sidecar-timeout"
	CodeFrameTooLarge     = "frame-too-large"
	CodeOutputTooLarge    = "output-too-large"
	CodeHostCallDenied    = "host-call-denied"
	CodeProtocolViolation = "protocol-violation"
	CodeMemoryLimit       = "sidecar-memory-limit"
	CodeCancelled         = "sidecar-cancelled"
	CodeBusy              = "sidecar-busy"
)

// CallError is a failed sidecar node run. Code is machine-readable; Detail is
// the operator-facing diagnostic. Tenant and Node say whose run failed.
// Cause, when set, is the context error that ended the run, so a caller can
// still ask errors.Is(err, context.Canceled).
type CallError struct {
	Code         string
	Tenant       string
	Node         string
	Detail       string
	ChildCode    string
	ChildMessage string
	Cause        error
}

func (err *CallError) Error() string {
	if err.Node != "" {
		return fmt.Sprintf("sidecar node %s failed (%s): %s", err.Node, err.Code, err.Detail)
	}
	return fmt.Sprintf("sidecar call failed (%s): %s", err.Code, err.Detail)
}

// Unwrap returns the context error that ended the run, if any.
func (err *CallError) Unwrap() error { return err.Cause }

// Limits bound one sidecar node run and the processes behind it. Every field
// is enforced on the host side: the child is untrusted JavaScript and is
// never asked to police itself.
type Limits struct {
	// Timeout bounds one execute round-trip on a warm process: writing the
	// request, the child running, reading the answer.
	Timeout time.Duration
	// MaxFrameBytes caps one NDJSON line in either direction. Past it, the
	// run fails instead of the host buffering unboundedly. NewPool raises it
	// to sit above MaxOutputBytes, or the output bound would be unreachable.
	MaxFrameBytes int
	// MaxOutputBytes caps the decoded result payload. A child that answers
	// within the frame limit but past this one still fails the run.
	MaxOutputBytes int64
	// SpawnTimeout bounds a cold start: listen, fork, and the child's dial-back.
	SpawnTimeout time.Duration
	// NodeMaxHeapMB reaches the child as node's --max-old-space-size. It is
	// a heap ceiling, not an RSS cap: Buffer and external allocations live
	// outside V8's old space, which is why MaxRSSMB exists as a second bound
	// and the container remains the last one.
	NodeMaxHeapMB int
	// MaxRSSMB is the resident-set ceiling the host watchdog enforces; a
	// breach SIGKILLs the process group and fails the run as a memory limit.
	// Zero disables the watchdog.
	MaxRSSMB int
	// IdleTimeout is how long a tenant's process sits warm between runs
	// before CloseIdle reaps it.
	IdleTimeout time.Duration
	// MaxProcesses caps how many tenant processes the pool holds at once.
	// Zero means unlimited. At the cap the pool evicts a least-recently-used
	// idle process, and if every process is busy it waits for a slot before
	// naming sidecar.max_processes.
	MaxProcesses int
	// MaxHostCalls caps the host calls one run may make. Zero means the
	// default.
	MaxHostCalls int
	// MaxCatalogueBytes caps the catalogue a describe run may return.
	MaxCatalogueBytes int
}

// DefaultLimits are conservative because the code across the socket is third
// party. A deployment may tighten them per call; loosening the wall clock
// past the execution's own deadline only buys a wait that never pays off.
func DefaultLimits() Limits {
	return Limits{
		Timeout:           30 * time.Second,
		MaxFrameBytes:     1 << 20,
		MaxOutputBytes:    4 << 20,
		SpawnTimeout:      15 * time.Second,
		NodeMaxHeapMB:     256,
		MaxRSSMB:          512,
		IdleTimeout:       5 * time.Minute,
		MaxProcesses:      16,
		MaxHostCalls:      256,
		MaxCatalogueBytes: 16 << 20,
	}
}

func normalizeLimits(limits Limits) Limits {
	defaults := DefaultLimits()
	if limits.Timeout <= 0 {
		limits.Timeout = defaults.Timeout
	}
	if limits.MaxFrameBytes <= 0 {
		limits.MaxFrameBytes = defaults.MaxFrameBytes
	}
	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if limits.SpawnTimeout <= 0 {
		limits.SpawnTimeout = defaults.SpawnTimeout
	}
	if limits.MaxHostCalls <= 0 {
		limits.MaxHostCalls = defaults.MaxHostCalls
	}
	if limits.MaxCatalogueBytes <= 0 {
		limits.MaxCatalogueBytes = defaults.MaxCatalogueBytes
	}
	// The output bound is what the ticket cares about; a frame bound below it
	// makes the output bound unreachable and the failure unenforceable.
	if limits.MaxFrameBytes <= int(limits.MaxOutputBytes) {
		limits.MaxFrameBytes = int(limits.MaxOutputBytes) + (64 << 10)
	}
	return limits
}

// Request is one node execution to run across the socket.
type Request struct {
	// ID correlates the answer; empty means the pool mints one.
	ID string
	// Tenant selects the process. Empty fails closed: without a tenant there
	// is no process the secrets in this request are allowed to enter.
	Tenant string
	Node   string
	Params map[string]any
	Items  []Item
	// Secrets are decrypted credentials, sent only into the calling tenant's
	// own process. Omitted on the wire when empty.
	Secrets map[string]string
	// NodeVersion is the node type version the run targets.
	NodeVersion float64
	// ParamsByItem carries per-item parameters for onError modes that run the
	// node once per item.
	ParamsByItem []map[string]any
	// Credentials maps credential type to decrypted fields, for packages that
	// need more than one credential type.
	Credentials map[string]map[string]string
	// Context is the workflow context the node may read.
	Context *RunContext
	// Host serves host calls the child makes. Nil denies every host call.
	Host HostHandler
}

// Result is one successful run. Items is the first output port, kept for the
// single-output callers; Outputs carries every port.
type Result struct {
	Items   []Item
	Outputs [][]Item
}

// Child is one running sidecar process as the pool sees it: a connected
// protocol socket and a way to kill it. Stdout and stderr are already
// connected to the host log by whoever spawned it — they are diagnostics,
// never parsed, so they do not appear here.
type Child struct {
	Conn net.Conn
	Kill func() error
	// PID is the child's process id, for logs and tests.
	PID int
	// SocketDir is the directory the spawner created for this child's socket,
	// if it created one, so the pool can remove it on eviction.
	SocketDir string
	// Diagnose reports how a dead child died, waiting up to the given
	// duration for the reaper. It reads the wait status, never stderr.
	Diagnose func(wait time.Duration) (code, detail string)
}

// SpawnFunc starts the sidecar process for a tenant and returns once its
// dial-back is connected. socketPath is the Unix socket the child receives as
// an argument; empty means the spawner chooses and creates the socket dir.
type SpawnFunc func(ctx context.Context, tenant, socketPath string) (*Child, error)

type proc struct {
	tenant   string
	child    *Child
	lastUsed atomic.Int64
	dead     atomic.Bool
	// sem is a one-slot semaphore: executes against one process serialise so
	// answers cannot pair with the wrong request, and a queued run can honour
	// its own context instead of waiting on a mutex.
	sem chan struct{}
	// starting, ready and startErr coordinate a cold start. They are guarded
	// by Pool.mu; ready is closed when the start finishes, and startErr is
	// set before the close so waiters read it safely.
	starting bool
	ready    chan struct{}
	startErr error
}

func (process *proc) release() { <-process.sem }

func (process *proc) busy() bool { return len(process.sem) == 1 }

func (process *proc) touch() { process.lastUsed.Store(time.Now().UnixNano()) }

// Pool serves sidecar executes out of one child process per tenant. It is
// safe for concurrent use; executes against one tenant's process serialise so
// answers cannot be paired with the wrong request.
type Pool struct {
	mu      sync.Mutex
	procs   map[string]*proc
	spawn   SpawnFunc
	limits  Limits
	diagLog io.Writer
}

// NewPool builds a pool around spawn. A nil spawn means the deployment
// declined the sidecar: executes fail with CodeNoSidecar naming what the
// operator must install, and nothing else changes. A nil diagLog discards
// child diagnostics.
func NewPool(spawn SpawnFunc, limits Limits) *Pool {
	return &Pool{procs: map[string]*proc{}, spawn: spawn, limits: normalizeLimits(limits)}
}

// SetDiagLog directs child stdout/stderr transcripts. It is a setter rather
// than a constructor argument so tests can capture the banner that proves
// diagnostics never enter the protocol stream.
func (pool *Pool) SetDiagLog(writer io.Writer) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.diagLog = writer
}

// Execute runs one node in the calling tenant's process. The returned error,
// when non-nil, is always a *CallError: a failed node run, never an engine
// failure. The host process is intact on every path below.
func (pool *Pool) Execute(ctx context.Context, req Request) (Result, error) {
	if req.Tenant == "" {
		return Result{}, &CallError{Code: CodeTenantRequired, Node: req.Node, Detail: "a sidecar run without a tenant has no process its secrets may enter"}
	}
	if pool.spawn == nil {
		return Result{}, &CallError{Code: CodeNoSidecar, Tenant: req.Tenant, Node: req.Node,
			Detail: "this deployment has no JavaScript sidecar: install Node 24 LTS and the node's package, then point the deployment at it"}
	}
	if req.ID == "" {
		req.ID = newCallID()
	}

	process, err := pool.resolveForRun(ctx, req.Tenant)
	if err != nil {
		return Result{}, err
	}
	defer process.release()
	process.touch()
	defer process.touch()

	deadline := time.Now().Add(pool.limits.Timeout)
	ctxBounded := false
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
		ctxBounded = true
	}
	if err := process.child.Conn.SetDeadline(deadline); err != nil {
		pool.evictProc(process)
		return Result{}, &CallError{Code: CodeSidecarCrash, Tenant: req.Tenant, Node: req.Node, Detail: "sidecar socket refused a deadline"}
	}
	// A context that ends mid-run is a cancel or a deadline, never a silent
	// timeout: nudging the socket deadline unblocks the read immediately and
	// lets fail() name the right code.
	stopWatchContext := context.AfterFunc(ctx, func() {
		_ = process.child.Conn.SetDeadline(time.Now())
	})
	defer stopWatchContext()

	frame := executeFrame{
		Type: frameExecute, ID: req.ID, Tenant: req.Tenant,
		Node: req.Node, Params: req.Params, Items: req.Items, Secrets: req.Secrets,
		NodeVersion: req.NodeVersion, ParamsByItem: req.ParamsByItem,
		Credentials: req.Credentials, Context: req.Context,
	}
	if frame.Items == nil {
		frame.Items = []Item{}
	}
	if err := writeFrame(process.child.Conn, frame); err != nil {
		return pool.fail(ctx, process, req, err, ctxBounded)
	}

	reader := newFrameReader(process.child.Conn, pool.limits.MaxFrameBytes)
	hostCalls := 0
	for {
		line, err := reader.next()
		if err != nil {
			return pool.fail(ctx, process, req, err, ctxBounded)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			pool.evictProc(process)
			return Result{}, &CallError{Code: CodeProtocolViolation, Tenant: req.Tenant, Node: req.Node, Detail: "sidecar answered with a message that is not JSON"}
		}
		switch envelope.Type {
		case frameHTTPRequest:
			if req.Host == nil {
				pool.evictProc(process)
				return Result{}, &CallError{Code: CodeHostCallDenied, Tenant: req.Tenant, Node: req.Node,
					Detail: "sidecar asked the host for an outbound HTTP call, which this deployment does not serve"}
			}
			hostCalls++
			if hostCalls > pool.limits.MaxHostCalls {
				pool.evictProc(process)
				return Result{}, &CallError{Code: CodeHostCallDenied, Tenant: req.Tenant, Node: req.Node,
					Detail: "sidecar made too many host calls"}
			}
			if err := pool.serveHostCall(ctx, process, req.Host, line); err != nil {
				return pool.fail(ctx, process, req, err, ctxBounded)
			}
			continue
		case frameResult, frameError:
			answer, err := decodeTerminal(line)
			if err != nil {
				return pool.fail(ctx, process, req, err, ctxBounded)
			}
			if answer.ID != req.ID {
				pool.evictProc(process)
				return Result{}, &CallError{Code: CodeProtocolViolation, Tenant: req.Tenant, Node: req.Node,
					Detail: "sidecar answered a run that was never sent"}
			}
			if answer.Type == frameError {
				message := truncate(answer.Message, maxChildMessage)
				return Result{}, &CallError{Code: CodeSidecarCrash, Tenant: req.Tenant, Node: req.Node,
					Detail:    "sidecar node reported failure: " + message,
					ChildCode: truncate(answer.Code, maxFrameType), ChildMessage: message}
			}
			if payloadBytes(line) > pool.limits.MaxOutputBytes {
				pool.evictProc(process)
				return Result{}, &CallError{Code: CodeOutputTooLarge, Tenant: req.Tenant, Node: req.Node,
					Detail: "sidecar node produced more output than the limit allows"}
			}
			outputs := answer.Outputs
			if len(outputs) == 0 {
				items := answer.Items
				if items == nil {
					items = []Item{}
				}
				outputs = [][]Item{items}
			}
			return Result{Items: outputs[0], Outputs: outputs}, nil
		default:
			pool.evictProc(process)
			return Result{}, &CallError{Code: CodeHostCallDenied, Tenant: req.Tenant, Node: req.Node,
				Detail: fmt.Sprintf("sidecar asked the host for %q, which this deployment does not serve", truncate(envelope.Type, maxFrameType))}
		}
	}
}

// serveHostCall runs one host call the child asked for and writes the answer
// back. Calls are served sequentially inside the caller's read loop, bounded
// by the run deadline: a package using Promise.all still gets one call at a
// time, which the docs say out loud.
func (pool *Pool) serveHostCall(ctx context.Context, process *proc, handler HostHandler, line []byte) error {
	var frame httpRequestFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		return &CallError{Code: CodeProtocolViolation, Detail: "sidecar sent a host call that is not readable"}
	}
	body, err := base64.StdEncoding.DecodeString(frame.Body64)
	if err != nil {
		return pool.writeHTTPError(process, frame.ID, frame.Call, "bad-request", "the host call body is not valid base64")
	}
	timeout := time.Duration(frame.TimeoutMs) * time.Millisecond
	response, err := handler.HTTP(ctx, HTTPRequest{
		Method: frame.Method, URL: frame.URL, Headers: frame.Headers,
		Body: body, Timeout: timeout,
	})
	if err != nil {
		// A handler that knows why it refused says so with a code the child
		// can act on; every other failure is one generic code.
		code, message := "http-error", err.Error()
		var coded HostCallCoder
		if errors.As(err, &coded) {
			if named, detail := coded.HostCallCode(); named != "" {
				code, message = named, detail
			}
		}
		return pool.writeHTTPError(process, frame.ID, frame.Call, truncate(code, maxFrameType), truncate(message, maxChildMessage))
	}
	answer := httpResponseFrame{
		Type: frameHTTPResponse, ID: frame.ID, Call: frame.Call,
		Status: response.Status, StatusText: response.StatusText, Headers: response.Headers,
	}
	if len(response.Body) > 0 {
		answer.Body64 = base64.StdEncoding.EncodeToString(response.Body)
	}
	return writeFrame(process.child.Conn, answer)
}

func (pool *Pool) writeHTTPError(process *proc, id, call, code, message string) error {
	return writeFrame(process.child.Conn, httpErrorFrame{Type: frameHTTPError, ID: id, Call: call, Code: code, Message: message})
}

// resolveForRun returns the tenant's process with its one-slot semaphore
// held. A process that died between being handed out and acquiring the
// semaphore is evicted by identity and resolved once more: nothing has been
// sent yet, so a fresh cold start is the right answer, not a failed run.
func (pool *Pool) resolveForRun(ctx context.Context, tenant string) (*proc, error) {
	for attempt := 0; attempt < 2; attempt++ {
		process, err := pool.procFor(ctx, tenant)
		if err != nil {
			return nil, err
		}
		select {
		case process.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, contextError(ctx, tenant)
		}
		if !process.dead.Load() {
			return process, nil
		}
		process.release()
		pool.evictProc(process)
	}
	return nil, &CallError{Code: CodeSidecarCrash, Tenant: tenant, Detail: "sidecar process died before the run started"}
}

// fail maps a transport or protocol failure onto a named error, evicting the
// process first: a child that crashed, hung, or spoke out of turn is never
// trusted with the next run.
func (pool *Pool) fail(ctx context.Context, process *proc, req Request, err error, ctxBounded bool) (Result, error) {
	pool.evictProc(process)
	if callErr, ok := err.(*CallError); ok {
		callErr.Tenant = req.Tenant
		callErr.Node = req.Node
		return Result{}, callErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		code := CodeCancelled
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			code = CodeSidecarTimeout
		}
		return Result{}, &CallError{Code: code, Tenant: req.Tenant, Node: req.Node,
			Detail: "sidecar run was cancelled", Cause: ctxErr}
	}
	if isTimeout(err) {
		// The deadline the socket was given was the context's own, so the
		// context is the cause even if the read lost the race with the
		// context timer by a few microseconds.
		if ctxBounded {
			return Result{}, &CallError{Code: CodeSidecarTimeout, Tenant: req.Tenant, Node: req.Node,
				Detail: "sidecar run exceeded its deadline", Cause: context.DeadlineExceeded}
		}
		return Result{}, &CallError{Code: CodeSidecarTimeout, Tenant: req.Tenant, Node: req.Node,
			Detail: "sidecar node exceeded its time limit"}
	}
	code, detail := CodeSidecarCrash, "sidecar process died during the run"
	if process.child != nil && process.child.Diagnose != nil {
		if diagnosed, reason := process.child.Diagnose(time.Second); diagnosed != "" {
			code, detail = diagnosed, reason
		}
	}
	return Result{}, &CallError{Code: code, Tenant: req.Tenant, Node: req.Node, Detail: detail}
}

func isTimeout(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return os.IsTimeout(err)
}

// contextError maps a context that ended while a run was queued onto the same
// named codes a run cancelled mid-flight gets.
func contextError(ctx context.Context, tenant string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &CallError{Code: CodeSidecarTimeout, Tenant: tenant, Detail: "the sidecar run's deadline passed while it waited its turn", Cause: ctx.Err()}
	}
	return &CallError{Code: CodeCancelled, Tenant: tenant, Detail: "the sidecar run was cancelled while it waited its turn", Cause: ctx.Err()}
}

// payloadBytes sizes the result payload, which is what MaxOutputBytes bounds.
// Both shapes are charged: a multi-output answer would otherwise bypass the
// limit by moving its bytes from items into outputs. The envelope around them
// is the host's own framing and is not charged to the child.
func payloadBytes(line []byte) int64 {
	var frame struct {
		Items   json.RawMessage `json:"items"`
		Outputs json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(line, &frame); err != nil || (frame.Items == nil && frame.Outputs == nil) {
		return int64(len(line))
	}
	total := int64(0)
	if frame.Items != nil {
		total += int64(len(frame.Items))
	}
	if frame.Outputs != nil {
		total += int64(len(frame.Outputs))
	}
	return total
}

// procFor returns the calling tenant's process, starting it on a cold path.
// The map key is the tenant, so a process started for A is unreachable from
// a request for B by construction — the isolation test pins this.
//
// Only one cold start per tenant can be in flight: the first caller inserts a
// starting entry with a ready channel, and every later caller waits on that
// channel or on its own context instead of spawning a spare.
func (pool *Pool) procFor(ctx context.Context, tenant string) (*proc, error) {
	for {
		pool.mu.Lock()
		if existing, ok := pool.procs[tenant]; ok {
			if existing.starting {
				ready := existing.ready
				pool.mu.Unlock()
				select {
				case <-ready:
					if existing.startErr != nil {
						return nil, existing.startErr
					}
					continue
				case <-ctx.Done():
					return nil, contextError(ctx, tenant)
				}
			}
			if !existing.dead.Load() {
				pool.mu.Unlock()
				return existing, nil
			}
			delete(pool.procs, tenant)
		}
		starter := &proc{tenant: tenant, ready: make(chan struct{}), starting: true, sem: make(chan struct{}, 1)}
		pool.procs[tenant] = starter
		pool.mu.Unlock()

		child, err := pool.startProcess(ctx, tenant)
		pool.mu.Lock()
		if err != nil {
			if pool.procs[tenant] == starter {
				delete(pool.procs, tenant)
			}
			starter.startErr = err
			starter.starting = false
			close(starter.ready)
			pool.mu.Unlock()
			return nil, err
		}
		starter.child = child
		starter.touch()
		starter.starting = false
		close(starter.ready)
		pool.mu.Unlock()
		return starter, nil
	}
}

// startProcess acquires a slot, then spawns under the spawn timeout.
func (pool *Pool) startProcess(ctx context.Context, tenant string) (*Child, error) {
	if err := pool.acquireSlot(ctx, tenant); err != nil {
		return nil, err
	}
	spawnCtx, cancel := context.WithTimeout(ctx, pool.limits.SpawnTimeout)
	defer cancel()
	child, err := pool.spawn(spawnCtx, tenant, "")
	if err != nil {
		if callErr, ok := err.(*CallError); ok {
			callErr.Tenant = tenant
			return nil, callErr
		}
		return nil, &CallError{Code: CodeSpawnFailed, Tenant: tenant, Detail: "sidecar process failed to start: " + err.Error()}
	}
	return child, nil
}

// acquireSlot enforces MaxProcesses. At the cap it evicts the least recently
// used idle process; when every process is busy it waits for a slot up to
// min(ctx, SpawnTimeout) before naming sidecar.max_processes, because an
// immediate refusal would make a burst of tenants fail each other.
func (pool *Pool) acquireSlot(ctx context.Context, tenant string) error {
	deadline := time.Now().Add(pool.limits.SpawnTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for {
		pool.mu.Lock()
		held := 0
		var lru *proc
		for _, candidate := range pool.procs {
			if candidate.starting && candidate.tenant == tenant {
				// The pending start for this tenant is not a process yet; it
				// must not count against the cap that gates its own spawn.
				continue
			}
			held++
			if candidate.starting || candidate.dead.Load() || candidate.busy() {
				continue
			}
			if lru == nil || candidate.lastUsed.Load() < lru.lastUsed.Load() {
				lru = candidate
			}
		}
		if pool.limits.MaxProcesses == 0 || held < pool.limits.MaxProcesses {
			pool.mu.Unlock()
			return nil
		}
		pool.mu.Unlock()
		if lru != nil {
			pool.evictProc(lru)
			continue
		}
		if time.Now().After(deadline) {
			return &CallError{Code: CodeBusy, Tenant: tenant,
				Detail: fmt.Sprintf("every sidecar process is busy and the limit of %d (sidecar.max_processes) is reached", pool.limits.MaxProcesses)}
		}
		select {
		case <-ctx.Done():
			return contextError(ctx, tenant)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// evictProc kills and forgets one process, but only if the map still points
// at it: a stale run holding a dead P1 must never remove a healthy P2 that
// replaced it.
func (pool *Pool) evictProc(process *proc) {
	if process == nil {
		return
	}
	pool.mu.Lock()
	if current, ok := pool.procs[process.tenant]; ok && current == process {
		delete(pool.procs, process.tenant)
	}
	pool.mu.Unlock()
	if !process.dead.CompareAndSwap(false, true) {
		return
	}
	if process.child != nil {
		if process.child.Kill != nil {
			_ = process.child.Kill()
		}
		removeDir(process.child.SocketDir)
	}
}

// evict kills and forgets a tenant's current process. It is idempotent:
// double eviction from a failed run and a concurrent timeout reports once and
// leaks nothing.
func (pool *Pool) evict(tenant string) {
	pool.mu.Lock()
	process := pool.procs[tenant]
	pool.mu.Unlock()
	pool.evictProc(process)
}

// Evict drops one tenant's process without failing anything. The next run
// for that tenant cold-starts.
func (pool *Pool) Evict(tenant string) { pool.evict(tenant) }

// CloseIdle reaps processes idle past the limit. The operator ticks this on
// a timer; the pool itself runs no background goroutines.
//
// Candidates are chosen under the pool lock and killed outside it. A running
// process holds its semaphore and has a fresh lastUsed, so it is skipped:
// reaping it would fail the run it is serving.
func (pool *Pool) CloseIdle(now time.Time) {
	pool.mu.Lock()
	var idle []*proc
	for _, process := range pool.procs {
		if process.starting || process.dead.Load() || process.busy() {
			continue
		}
		if now.Sub(time.Unix(0, process.lastUsed.Load())) > pool.limits.IdleTimeout {
			idle = append(idle, process)
		}
	}
	pool.mu.Unlock()
	for _, process := range idle {
		pool.evictProc(process)
	}
}

// Close drops every process. The pool is usable afterwards; the next run
// cold-starts.
func (pool *Pool) Close() {
	pool.mu.Lock()
	processes := make([]*proc, 0, len(pool.procs))
	for _, process := range pool.procs {
		processes = append(processes, process)
	}
	pool.mu.Unlock()
	for _, process := range processes {
		pool.evictProc(process)
	}
}

// Live reports how many tenant processes are currently held, for tests and
// operator introspection.
func (pool *Pool) Live() int {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return len(pool.procs)
}

func newCallID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "call-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "call-" + hex.EncodeToString(random[:])
}
