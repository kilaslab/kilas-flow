package jsworker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// Options configure a Pool for the whole deployment.
type Options struct {
	// Limits is the deployment's ceiling. A task may tighten it, never raise
	// it. Zero fields take jsrun's defaults.
	Limits jsrun.Limits
	// MaxConcurrent bounds how many scripts run at once, and so how many
	// workers exist. Zero means runtime.GOMAXPROCS(0).
	MaxConcurrent int
	// HeapCeiling is the live heap, in bytes, at which a worker's watchdog
	// stops its script. Zero means jsrun.DefaultHeapCeiling.
	HeapCeiling uint64
	// Command builds the command that starts one worker. The default runs
	// this same binary as a worker. The pool owns its Stdin, Stdout and
	// Stderr; a nil Env is replaced by the worker's minimal environment,
	// never inherited.
	Command func() *exec.Cmd
	// Grace is how long past twice its time limit a job may take before its
	// worker is killed. Zero means DefaultGrace.
	Grace time.Duration
	// IdleTimeout is how long an unused worker is kept. Zero means
	// DefaultIdleTimeout.
	IdleTimeout time.Duration
	// MaxRuns is how many jobs one worker runs before it is replaced. Zero
	// means DefaultMaxRuns.
	MaxRuns int
	// Logger records workers that died or could not start. Nil discards.
	Logger *slog.Logger
}

const (
	// DefaultGrace covers setup and decoding, which the time limit does not
	// charge, and a loaded machine: a worker is killed only when it is
	// plainly stuck in something its own clock cannot stop.
	DefaultGrace = 5 * time.Second
	// DefaultIdleTimeout is how long a worker nobody needs keeps its memory.
	DefaultIdleTimeout = 5 * time.Minute
	// DefaultMaxRuns bounds what one worker's heap can accumulate.
	DefaultMaxRuns = 1000
)

// Pool runs Code-node JavaScript in worker processes. It is a jsrun.Engine,
// safe for concurrent use; one per deployment is the intended shape.
type Pool struct {
	preparer    *jsrun.Runner
	limits      jsrun.Limits
	heapCeiling uint64
	command     func() *exec.Cmd
	grace       time.Duration
	idleTimeout time.Duration
	maxRuns     int
	logger      *slog.Logger
	slots       chan struct{}

	mu     sync.Mutex
	idle   []*worker
	closed bool
}

var _ jsrun.Engine = (*Pool)(nil)

// New builds a pool. It starts no worker until the first job needs one.
func New(options Options) *Pool {
	pool := &Pool{
		heapCeiling: options.HeapCeiling,
		command:     options.Command,
		grace:       options.Grace,
		idleTimeout: options.IdleTimeout,
		maxRuns:     options.MaxRuns,
		logger:      options.Logger,
	}
	if pool.heapCeiling == 0 {
		pool.heapCeiling = jsrun.DefaultHeapCeiling
	}
	if pool.command == nil {
		pool.command = func() *exec.Cmd { return defaultCommand(pool.heapCeiling) }
	}
	if pool.grace <= 0 {
		pool.grace = DefaultGrace
	}
	if pool.idleTimeout <= 0 {
		pool.idleTimeout = DefaultIdleTimeout
	}
	if pool.maxRuns <= 0 {
		pool.maxRuns = DefaultMaxRuns
	}
	if pool.logger == nil {
		pool.logger = slog.New(slog.DiscardHandler)
	}
	concurrent := options.MaxConcurrent
	if concurrent <= 0 {
		concurrent = runtime.GOMAXPROCS(0)
	}
	pool.slots = make(chan struct{}, concurrent)
	// The server's own runner only prepares jobs: it encodes the input and
	// checks the caps. It never compiles or runs code.
	pool.preparer = jsrun.NewRunner(jsrun.Options{Limits: options.Limits, MaxConcurrent: 1, HeapCeiling: pool.heapCeiling})
	pool.limits = pool.preparer.Limits()
	return pool
}

// Limits reports the pool's ceiling, after defaults.
func (pool *Pool) Limits() jsrun.Limits { return pool.limits }

// Run executes one node's code in a worker. It fails as jsrun.Runner.Run
// does, with the same errors, and with the time-limit, memory-limit or
// engine-fault error when the worker had to be killed or died.
func (pool *Pool) Run(ctx context.Context, task jsrun.Task) (jsrun.Result, error) {
	if err := ctx.Err(); err != nil {
		return jsrun.Result{}, err
	}
	job, host, err := pool.preparer.Prepare(task)
	if err != nil {
		return jsrun.Result{}, err
	}
	select {
	case pool.slots <- struct{}{}:
	case <-ctx.Done():
		return jsrun.Result{}, ctx.Err()
	}
	defer func() { <-pool.slots }()
	w, err := pool.take()
	if err != nil {
		pool.logger.Error("a JavaScript worker could not start", "error", err)
		return jsrun.Result{}, jsrun.EngineFaultError("its worker process could not start: " + err.Error())
	}
	result, err := pool.run(ctx, w, job, host)
	pool.put(w)
	return result, err
}

// Close stops every idle worker, and each busy one as its job ends. Runs
// after Close fail.
func (pool *Pool) Close() {
	pool.mu.Lock()
	pool.closed = true
	idle := pool.idle
	pool.idle = nil
	pool.mu.Unlock()
	for _, w := range idle {
		w.idleTimer.Stop()
		w.stop()
	}
}

func (pool *Pool) take() (*worker, error) {
	pool.mu.Lock()
	for len(pool.idle) > 0 {
		w := pool.idle[len(pool.idle)-1]
		pool.idle = pool.idle[:len(pool.idle)-1]
		w.idleTimer.Stop()
		if w.alive() {
			pool.mu.Unlock()
			return w, nil
		}
		// It died while idle, perhaps at the kernel's hand.
		w.stop()
	}
	closed := pool.closed
	pool.mu.Unlock()
	if closed {
		return nil, errors.New("the JavaScript worker pool is closed")
	}
	return pool.start()
}

func (pool *Pool) put(w *worker) {
	if !w.healthy || w.runs >= pool.maxRuns || !w.alive() {
		w.stop()
		return
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed {
		w.stop()
		return
	}
	pool.idle = append(pool.idle, w)
	w.idleTimer = time.AfterFunc(pool.idleTimeout, func() { pool.retire(w) })
}

// retire stops a worker that stayed idle too long, unless a job took it.
func (pool *Pool) retire(w *worker) {
	pool.mu.Lock()
	for index, candidate := range pool.idle {
		if candidate == w {
			pool.idle = append(pool.idle[:index], pool.idle[index+1:]...)
			pool.mu.Unlock()
			w.stop()
			return
		}
	}
	pool.mu.Unlock()
}

// worker is one worker process and the server's ends of its pipes.
type worker struct {
	cmd     *exec.Cmd
	in      *bufio.Writer
	inFile  *os.File
	out     *bufio.Reader
	outFile *os.File
	stderr  *tail
	// exited closes when the process has been reaped; state is set before.
	exited chan struct{}
	state  *os.ProcessState
	// runs counts the jobs it finished; healthy turns false for good when
	// anything went wrong in one.
	runs      int
	healthy   bool
	idleTimer *time.Timer
}

func (pool *Pool) start() (*worker, error) {
	cmd := pool.command()
	if cmd.Env == nil {
		cmd.Env = workerEnvironment(pool.heapCeiling)
	}
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		stdinRead.Close()
		stdinWrite.Close()
		return nil, err
	}
	stderr := &tail{limit: 8 << 10}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinRead, stdoutWrite, stderr
	prepareCommand(cmd)
	err = startCommand(cmd)
	// The child holds its own ends now, or failed to start.
	stdinRead.Close()
	stdoutWrite.Close()
	if err != nil {
		stdinWrite.Close()
		stdoutRead.Close()
		return nil, err
	}
	w := &worker{
		cmd: cmd, in: bufio.NewWriterSize(stdinWrite, 64<<10), inFile: stdinWrite,
		out: bufio.NewReaderSize(stdoutRead, 64<<10), outFile: stdoutRead,
		stderr: stderr, exited: make(chan struct{}), healthy: true,
	}
	go func() {
		_ = cmd.Wait()
		w.state = cmd.ProcessState
		close(w.exited)
	}()
	limits := pool.limits
	hello := message{Type: typeHello, Limits: &limits, HeapCeiling: pool.heapCeiling, AddressSpace: addressSpace(pool.heapCeiling)}
	if err := writeFrame(w.in, hello, ""); err != nil {
		w.stop()
		return nil, err
	}
	return w, nil
}

func (w *worker) alive() bool {
	select {
	case <-w.exited:
		return false
	default:
		return true
	}
}

// stop ends the worker and releases its pipes. The process is killed rather
// than asked: it has nothing to save.
func (w *worker) stop() {
	_ = w.cmd.Process.Kill()
	w.inFile.Close()
	w.outFile.Close()
}

// How one job on a worker ended. Whichever of the job finishing, its
// deadline and its cancellation claims the attempt first decides, so a
// deadline that fires as the result arrives cannot also kill the worker for
// the next job.
const (
	attemptRunning int32 = iota
	attemptFinished
	attemptPastDeadline
	attemptCancelled
	attemptBroken
)

func (pool *Pool) run(ctx context.Context, w *worker, job jsrun.Job, host jsrun.Host) (jsrun.Result, error) {
	var state atomic.Int32
	claim := func(outcome int32) bool { return state.CompareAndSwap(attemptRunning, outcome) }
	kill := func(outcome int32) {
		if claim(outcome) {
			_ = w.cmd.Process.Kill()
		}
	}
	deadline := time.AfterFunc(2*job.Limits.Timeout+pool.grace, func() { kill(attemptPastDeadline) })
	defer deadline.Stop()
	defer context.AfterFunc(ctx, func() { kill(attemptCancelled) })()

	failed := func(cause error) (jsrun.Result, error) {
		w.healthy = false
		claim(attemptBroken)
		_ = w.cmd.Process.Kill()
		<-w.exited
		switch state.Load() {
		case attemptPastDeadline:
			pool.logger.Warn("a JavaScript worker was killed past its job's deadline", "timeLimit", job.Limits.Timeout)
			return jsrun.Result{}, jsrun.TimeLimitError(job.Limits.Timeout)
		case attemptCancelled:
			return jsrun.Result{}, context.Cause(ctx)
		}
		status, stderr := w.state.String(), w.stderr.String()
		pool.logger.Warn("a JavaScript worker died while running code", "status", status, "cause", cause, "stderr", stderr)
		if ranOutOfMemory(status, stderr) {
			return jsrun.Result{}, jsrun.MemoryLimitError(pool.heapCeiling)
		}
		return jsrun.Result{}, jsrun.EngineFaultError("its worker process " + status)
	}

	if err := writeFrame(w.in, message{Type: typeRun, Job: &job}, job.Input); err != nil {
		return failed(err)
	}
	// What a worker writes is bounded by the job's own output caps; anything
	// larger is a broken worker, not a result.
	headerLimit := 4*job.Limits.MaxOutputBytes + 8*job.Limits.MaxConsoleBytes + 16<<20
	for {
		m, _, err := readFrame(w.out, headerLimit, 0)
		if err != nil {
			return failed(err)
		}
		switch m.Type {
		case typeCall:
			reply, blob := pool.answer(host, m)
			if err := writeFrame(w.in, reply, blob); err != nil {
				return failed(err)
			}
		case typeDone:
			w.runs++
			if !claim(attemptFinished) {
				// Killed as the result arrived; the result is still whole.
				w.healthy = false
			}
			var result jsrun.Result
			if m.Result != nil {
				result = *m.Result
			}
			runErr := m.Error.Decode()
			// A heap that hit its ceiling, or an engine that faulted, starts
			// the next job in a fresh process.
			if errors.Is(runErr, jsrun.ErrMemoryLimit) || errors.Is(runErr, jsrun.ErrEngineFault) {
				w.healthy = false
			}
			return result, runErr
		default:
			return failed(fmt.Errorf("jsworker: unexpected %q frame", m.Type))
		}
	}
}

// answer asks the engine what the code asked of it. A panic there is the
// server's fault; the code sees no answer, and the server goes on.
func (pool *Pool) answer(host jsrun.Host, question message) (reply message, blob string) {
	reply = message{Type: typeReply}
	defer func() {
		if recovered := recover(); recovered != nil {
			pool.logger.Error("answering a JavaScript worker panicked", "method", question.Method, "panic", recovered)
			reply, blob = message{Type: typeReply, Index: -1, Reason: "item lineage is not available here"}, ""
		}
	}()
	switch question.Method {
	case "node":
		if host.Node != nil {
			if view, ok := host.Node(question.Name); ok {
				reply.Found, blob = true, view
			}
		}
	case "pair":
		reply.Index, reply.Reason = -1, "item lineage is not available here"
		if host.Pair != nil {
			reply.Index, reply.Reason = host.Pair(question.Name, question.Index)
		}
	}
	return reply, blob
}

// ranOutOfMemory reads a dead worker's last words: Go's own out-of-memory
// failure, or a SIGKILL the pool did not send, which on a server is the
// kernel's OOM killer choosing the worker, as oom_score_adj asks it to.
func ranOutOfMemory(status, stderr string) bool {
	return strings.Contains(stderr, "out of memory") || strings.Contains(stderr, "cannot allocate memory") ||
		strings.Contains(status, "signal: killed")
}

// addressSpace is a worker's address-space limit: generous next to its heap
// ceiling, since the watchdog stops scripts that grow step by step, and there
// only for the single allocation no watchdog can stop.
func addressSpace(heapCeiling uint64) uint64 { return 4*heapCeiling + 1<<30 }

// defaultCommand runs this binary as a worker.
func defaultCommand(heapCeiling uint64) *exec.Cmd {
	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
	}
	cmd := exec.Command(executable)
	cmd.Env = workerEnvironment(heapCeiling)
	return cmd
}

// workerEnvironment is everything a worker is told about its surroundings:
// the marker that makes it one, how hard its runtime may work, and the time
// zone settings that decide what `new Date()` shows, as they do in the
// server. None of the server's configuration or secrets.
func workerEnvironment(heapCeiling uint64) []string {
	env := []string{
		markerVariable + "=1",
		// One script and its garbage collector.
		"GOMAXPROCS=2",
		fmt.Sprintf("GOMEMLIMIT=%d", heapCeiling+heapCeiling/2),
	}
	for _, name := range []string{"TZ", "ZONEINFO"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// tail keeps the end of what a worker wrote to stderr.
type tail struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

var _ io.Writer = (*tail)(nil)

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if extra := len(t.data) - t.limit; extra > 0 {
		t.data = append(t.data[:0], t.data[extra:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.data))
}
