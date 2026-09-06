package sidecar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
)

// CallError is a failed sidecar node run. Code is machine-readable; Detail is
// the operator-facing diagnostic. Tenant and Node say whose run failed.
type CallError struct {
	Code         string
	Tenant       string
	Node         string
	Detail       string
	ChildCode    string
	ChildMessage string
}

func (err *CallError) Error() string {
	if err.Node != "" {
		return fmt.Sprintf("sidecar node %s failed (%s): %s", err.Node, err.Code, err.Detail)
	}
	return fmt.Sprintf("sidecar call failed (%s): %s", err.Code, err.Detail)
}

// Limits bound one sidecar node run and the processes behind it. Every field
// is enforced on the host side: the child is untrusted JavaScript and is
// never asked to police itself.
type Limits struct {
	// Timeout bounds one execute round-trip on a warm process: writing the
	// request, the child running, reading the answer.
	Timeout time.Duration
	// MaxFrameBytes caps one NDJSON line in either direction. Past it, the
	// run fails instead of the host buffering unboundedly.
	MaxFrameBytes int
	// MaxOutputBytes caps the decoded result payload. A child that answers
	// within the frame limit but past this one still fails the run.
	MaxOutputBytes int64
	// SpawnTimeout bounds a cold start: listen, fork, and the child's dial-back.
	SpawnTimeout time.Duration
	// NodeMaxHeapMB reaches the child as node's --max-old-space-size. It is
	// a heap ceiling, not an RSS cap: the hard memory boundary for a sidecar
	// is the container around it, and the deployment notes say so.
	NodeMaxHeapMB int
	// IdleTimeout is how long a tenant's process sits warm between runs
	// before CloseIdle reaps it.
	IdleTimeout time.Duration
}

// DefaultLimits are conservative because the code across the socket is third
// party. A deployment may tighten them per call; loosening the wall clock
// past the execution's own deadline only buys a wait that never pays off.
func DefaultLimits() Limits {
	return Limits{
		Timeout:        30 * time.Second,
		MaxFrameBytes:  1 << 20,
		MaxOutputBytes: 4 << 20,
		SpawnTimeout:   15 * time.Second,
		NodeMaxHeapMB:  256,
		IdleTimeout:    5 * time.Minute,
	}
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
}

// Result is one successful run.
type Result struct {
	Items []Item
}

// Child is one running sidecar process as the pool sees it: a connected
// protocol socket and a way to kill it. Stdout and stderr are already
// connected to the host log by whoever spawned it — they are diagnostics,
// never parsed, so they do not appear here.
type Child struct {
	Conn net.Conn
	Kill func() error
}

// SpawnFunc starts the sidecar process for a tenant and returns once its
// dial-back is connected. socketPath is a fresh Unix socket path the child
// receives as an argument.
type SpawnFunc func(ctx context.Context, tenant, socketPath string) (*Child, error)

type proc struct {
	// mu serialises executes on one process so answers cannot pair with the
	// wrong request, and guards lastUsed. It is never held across evict:
	// eviction only flips dead and kills, both lock-free, so a failing run
	// can evict the process it holds without deadlocking on itself.
	mu       sync.Mutex
	tenant   string
	child    *Child
	sockDir  string
	lastUsed time.Time
	dead     atomic.Bool
}

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
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}
	if limits.MaxFrameBytes <= 0 {
		limits.MaxFrameBytes = DefaultLimits().MaxFrameBytes
	}
	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = DefaultLimits().MaxOutputBytes
	}
	if limits.SpawnTimeout <= 0 {
		limits.SpawnTimeout = DefaultLimits().SpawnTimeout
	}
	return &Pool{procs: map[string]*proc{}, spawn: spawn, limits: limits}
}

// SetDiagLog directs child stdout/stderr transcripts. It is a setter rather
// than a constructor argument so tests can capture the banner that proves
// diagnostics never enter the protocol stream.
func (pool *Pool) SetDiagLog(writer io.Writer) {
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

	process, err := pool.procFor(ctx, req.Tenant)
	if err != nil {
		return Result{}, err
	}
	process.mu.Lock()
	defer process.mu.Unlock()

	if process.dead.Load() {
		pool.evict(req.Tenant)
		return Result{}, &CallError{Code: CodeSidecarCrash, Tenant: req.Tenant, Node: req.Node, Detail: "sidecar process died before the run started"}
	}

	deadline := time.Now().Add(pool.limits.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := process.child.Conn.SetDeadline(deadline); err != nil {
		pool.evict(req.Tenant)
		return Result{}, &CallError{Code: CodeSidecarCrash, Tenant: req.Tenant, Node: req.Node, Detail: "sidecar socket refused a deadline"}
	}

	frame := executeFrame{
		Type: frameExecute, ID: req.ID, Tenant: req.Tenant,
		Node: req.Node, Params: req.Params, Items: req.Items, Secrets: req.Secrets,
	}
	if frame.Items == nil {
		frame.Items = []Item{}
	}
	if err := writeFrame(process.child.Conn, frame); err != nil {
		pool.evict(req.Tenant)
		return Result{}, &CallError{Code: CodeSidecarCrash, Tenant: req.Tenant, Node: req.Node, Detail: "sidecar process died while receiving the run"}
	}

	reader := newFrameReader(process.child.Conn, pool.limits.MaxFrameBytes)
	for {
		line, err := reader.next()
		if err != nil {
			return pool.fail(req, err)
		}
		answer, err := decodeTerminal(line)
		if err != nil {
			return pool.fail(req, err)
		}
		if answer.ID != req.ID {
			pool.evict(req.Tenant)
			return Result{}, &CallError{Code: CodeProtocolViolation, Tenant: req.Tenant, Node: req.Node,
				Detail: "sidecar answered a run that was never sent"}
		}
		if answer.Type == frameError {
			return Result{}, &CallError{Code: CodeSidecarCrash, Tenant: req.Tenant, Node: req.Node,
				Detail:    "sidecar node reported failure: " + answer.Message,
				ChildCode: answer.Code, ChildMessage: answer.Message}
		}
		if payloadBytes(line) > pool.limits.MaxOutputBytes {
			pool.evict(req.Tenant)
			return Result{}, &CallError{Code: CodeOutputTooLarge, Tenant: req.Tenant, Node: req.Node,
				Detail: "sidecar node produced more output than the limit allows"}
		}
		process.lastUsed = time.Now()
		items := answer.Items
		if items == nil {
			items = []Item{}
		}
		return Result{Items: items}, nil
	}
}

// fail maps a transport or protocol failure onto a named error, evicting the
// process first: a child that crashed, hung, or spoke out of turn is never
// trusted with the next run.
func (pool *Pool) fail(req Request, err error) (Result, error) {
	pool.evict(req.Tenant)
	if callErr, ok := err.(*CallError); ok {
		callErr.Tenant = req.Tenant
		callErr.Node = req.Node
		return Result{}, callErr
	}
	if isTimeout(err) {
		// A hung child is killed with its eviction: Kill runs inside evict,
		// so by the time this returns the process is gone, not lingering.
		return Result{}, &CallError{Code: CodeSidecarTimeout, Tenant: req.Tenant, Node: req.Node,
			Detail: "sidecar node exceeded its time limit"}
	}
	return Result{}, &CallError{Code: CodeSidecarCrash, Tenant: req.Tenant, Node: req.Node,
		Detail: "sidecar process died during the run"}
}

func isTimeout(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return os.IsTimeout(err)
}

// payloadBytes sizes the result frame's items payload, which is what
// MaxOutputBytes bounds. The envelope around it is the host's own framing
// and is not charged to the child.
func payloadBytes(line []byte) int64 {
	var frame struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(line, &frame); err != nil || frame.Items == nil {
		return int64(len(line))
	}
	return int64(len(frame.Items))
}

// procFor returns the calling tenant's process, starting it on a cold path.
// The map key is the tenant, so a process started for A is unreachable from
// a request for B by construction — the isolation test pins this.
func (pool *Pool) procFor(ctx context.Context, tenant string) (*proc, error) {
	pool.mu.Lock()
	if process, ok := pool.procs[tenant]; ok && !process.dead.Load() {
		pool.mu.Unlock()
		return process, nil
	}
	pool.mu.Unlock()

	sockDir, err := socketDir()
	if err != nil {
		return nil, &CallError{Code: CodeSpawnFailed, Tenant: tenant, Detail: "no directory for the sidecar socket: " + err.Error()}
	}
	socketPath := filepath.Join(sockDir, "sidecar.sock")
	spawnCtx, cancel := context.WithTimeout(ctx, pool.limits.SpawnTimeout)
	defer cancel()
	child, err := pool.spawn(spawnCtx, tenant, socketPath)
	if err != nil {
		os.RemoveAll(sockDir)
		if callErr, ok := err.(*CallError); ok {
			callErr.Tenant = tenant
			return nil, callErr
		}
		return nil, &CallError{Code: CodeSpawnFailed, Tenant: tenant, Detail: "sidecar process failed to start: " + err.Error()}
	}

	process := &proc{tenant: tenant, child: child, sockDir: sockDir, lastUsed: time.Now()}
	pool.mu.Lock()
	// A concurrent cold start for the same tenant may have won the race while
	// this one spawned. The loser kills its spare rather than leaking it —
	// and never serves from it, so tenancy still holds.
	if existing, ok := pool.procs[tenant]; ok && !existing.dead.Load() {
		pool.mu.Unlock()
		_ = child.Kill()
		os.RemoveAll(sockDir)
		return existing, nil
	}
	pool.procs[tenant] = process
	pool.mu.Unlock()
	return process, nil
}

// evict kills and forgets a tenant's process. It is idempotent: double
// eviction from a failed run and a concurrent timeout reports once and leaks
// nothing.
func (pool *Pool) evict(tenant string) {
	pool.mu.Lock()
	process, ok := pool.procs[tenant]
	if ok {
		delete(pool.procs, tenant)
	}
	pool.mu.Unlock()
	if !ok {
		return
	}
	// No process lock here: kill and sockDir are immutable after procFor, and
	// dead is atomic, so evict is safe to call while holding process.mu —
	// which every failing Execute does.
	process.dead.Store(true)
	if kill := process.child.Kill; kill != nil {
		_ = kill()
	}
	os.RemoveAll(process.sockDir)
}

// Evict drops one tenant's process without failing anything. The next run
// for that tenant cold-starts.
func (pool *Pool) Evict(tenant string) { pool.evict(tenant) }

// CloseIdle reaps processes idle past the limit. The operator ticks this on
// a timer; the pool itself runs no background goroutines.
func (pool *Pool) CloseIdle(now time.Time) {
	pool.mu.Lock()
	var idle []string
	for tenant, process := range pool.procs {
		process.mu.Lock()
		since := now.Sub(process.lastUsed)
		process.mu.Unlock()
		if since > pool.limits.IdleTimeout {
			idle = append(idle, tenant)
		}
	}
	pool.mu.Unlock()
	for _, tenant := range idle {
		pool.evict(tenant)
	}
}

// Close drops every process. The pool is usable afterwards; the next run
// cold-starts.
func (pool *Pool) Close() {
	pool.mu.Lock()
	tenants := make([]string, 0, len(pool.procs))
	for tenant := range pool.procs {
		tenants = append(tenants, tenant)
	}
	pool.mu.Unlock()
	for _, tenant := range tenants {
		pool.evict(tenant)
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

// socketDir makes the directory holding one process's socket. Unix socket
// paths are capped near a hundred bytes and the platform temp dir can already
// be long, so an overlong path falls back to /tmp rather than failing every
// spawn on machines with deep home directories.
func socketDir() (string, error) {
	dir, err := os.MkdirTemp("", "kflow-sidecar-*")
	if err != nil {
		return "", err
	}
	if len(filepath.Join(dir, "sidecar.sock")) > 90 {
		os.RemoveAll(dir)
		dir, err = os.MkdirTemp("/tmp", "kflow-sidecar-*")
		if err != nil {
			return "", err
		}
	}
	return dir, nil
}

// DefaultSpawn starts `node script tenant socketPath` with the child's
// stdout and stderr connected to diagLog as diagnostics only. heapMB sets
// --max-old-space-size; zero leaves node's default. A missing node binary
// fails with CodeNoSidecar, the same diagnostic a deployment without any
// sidecar gets: from the node's point of view those are the same thing.
func DefaultSpawn(script string, heapMB int, diagLog io.Writer) SpawnFunc {
	return func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		node, err := exec.LookPath("node")
		if err != nil {
			return nil, &CallError{Code: CodeNoSidecar, Detail: "node is not on PATH: install Node 24 LTS for the JavaScript sidecar"}
		}
		args := []string{}
		if heapMB > 0 {
			args = append(args, "--max-old-space-size="+strconv.Itoa(heapMB))
		}
		args = append(args, script, tenant, socketPath)
		cmd := exec.Command(node, args...)
		if diagLog != nil {
			cmd.Stdout = diagLog
			cmd.Stderr = diagLog
		}
		if err := cmd.Start(); err != nil {
			return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar process failed to start: " + err.Error()}
		}
		// Reap the child on exit. The goroutine lives exactly as long as the
		// process and holds nothing else.
		go func() { _ = cmd.Wait() }()

		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			_ = cmd.Process.Kill()
			return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar socket failed to listen: " + err.Error()}
		}
		defer listener.Close()
		accepted := make(chan net.Conn, 1)
		failed := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				failed <- err
				return
			}
			accepted <- conn
		}()
		select {
		case conn := <-accepted:
			return &Child{Conn: conn, Kill: func() error {
				_ = conn.Close()
				return cmd.Process.Kill()
			}}, nil
		case err := <-failed:
			_ = cmd.Process.Kill()
			return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar socket accept failed: " + err.Error()}
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar process never dialled back"}
		}
	}
}
