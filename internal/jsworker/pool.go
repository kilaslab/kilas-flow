package jsworker

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	protectServer()
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
	stderr  *stderrLog
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
	if cmd.Dir == "" {
		// Nothing a worker does is relative to the server's directory.
		cmd.Dir = "/"
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
	stderr := &stderrLog{limit: 4 << 10}
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
// deadline, its cancellation and its breaking claims the attempt first
// decides, so a deadline that fires as the result arrives cannot also kill
// the worker for the next job.
const (
	attemptRunning int32 = iota
	attemptFinished
	attemptPastDeadline
	attemptCancelled
	attemptBroken
)

// maxNodeQuestions bounds the distinct nodes one job may ask about. The
// runtime asks once per name, and a workflow has far fewer nodes; a worker
// asking more is not running the runtime.
const maxNodeQuestions = 4096

// maxStaticQuestions bounds a job's questions about static data: the runtime
// asks once per kind, and there are two.
const maxStaticQuestions = 2

func (pool *Pool) run(ctx context.Context, w *worker, job jsrun.Job, host jsrun.Host) (jsrun.Result, error) {
	var state atomic.Int32
	claim := func(outcome int32) bool { return state.CompareAndSwap(attemptRunning, outcome) }
	kill := func(outcome int32) {
		if claim(outcome) {
			_ = w.cmd.Process.Kill()
		}
	}
	deadline := newDeadline(2*job.Limits.Timeout+pool.grace, func() { kill(attemptPastDeadline) })
	defer deadline.stop()
	defer context.AfterFunc(ctx, func() { kill(attemptCancelled) })()
	nonce := newNonce()
	// Helper calls are answered on goroutines of their own, so the server
	// writes to the worker from more than one; and once the job is over,
	// nothing more is written for it. The helpers' work stops with the job.
	pipe := jobPipe{w: w}
	defer pipe.close()
	calls, stopCalls := context.WithCancel(ctx)
	defer stopCalls()

	if err := pipe.write(message{Type: typeRun, Nonce: nonce, Job: &job}, job.Input); err != nil {
		return pool.failed(ctx, w, job, &state, err)
	}
	// What a worker may write is bounded by the job's own caps: its results
	// travel as the blob, exactly as large as the output cap measured, and
	// the header holds what it printed, the items that failed and the static
	// data; a helper call's blob is a file or a request body.
	headerLimit := 2*job.Limits.MaxOutputBytes + 16*job.Limits.MaxConsoleBytes + 16*jsrun.MaxStaticDataBytes + 1<<20
	blobLimit := max(job.Limits.MaxOutputBytes, jsrun.MaxFileBytes)
	views := map[string]answer{}
	helperCalls, staticQuestions := 0, 0
	// The runtime gives each item the whole budget in per-item mode, so the
	// most a job may make is one budget per item there.
	maxHelperCalls := job.Limits.MaxHostCalls
	if job.Mode == jsrun.ModeEachItem {
		maxHelperCalls *= max(job.Count, 1)
	}
	for {
		m, blob, err := readFrame(w.out, headerLimit, blobLimit)
		if err == nil && m.Nonce != nonce {
			err = protocolViolation("a %s frame for another job", m.Type)
		}
		if err != nil {
			return pool.failed(ctx, w, job, &state, err)
		}
		switch m.Type {
		case typeCall:
			switch m.Method {
			case methodHelper:
				if helperCalls++; helperCalls > maxHelperCalls {
					return pool.failed(ctx, w, job, &state, protocolViolation("more than its %d helper calls", maxHelperCalls))
				}
				request, err := helperRequest(m, blob)
				if err != nil {
					return pool.failed(ctx, w, job, &state, err)
				}
				// The worker waits on the server now, and that is not time
				// its deadline measures. The trade-off: while any helper
				// call is in flight, a worker stuck in a built-in is not
				// killed either. That window is bounded by the execution's
				// timeout, and by the policy's HTTP timeout times the calls
				// the job may make.
				deadline.hold()
				go func(id int64) {
					defer deadline.release()
					answer := pool.help(calls, host, request)
					_ = pipe.write(message{Type: typeReply, Nonce: nonce, ID: id, Answer: &answer}, string(answer.Data))
				}(m.ID)
				continue
			case methodStatic:
				if staticQuestions++; staticQuestions > maxStaticQuestions {
					return pool.failed(ctx, w, job, &state, protocolViolation("more than %d questions about static data", maxStaticQuestions))
				}
			}
			if len(blob) > 0 {
				return pool.failed(ctx, w, job, &state, protocolViolation("a %s question carrying data", m.Method))
			}
			reply, view, err := pool.answer(host, m, views)
			if err == nil {
				err = pipe.write(reply, view)
			}
			if err != nil {
				return pool.failed(ctx, w, job, &state, err)
			}
		case typeDone:
			if int64(len(blob)) > job.Limits.MaxOutputBytes {
				return pool.failed(ctx, w, job, &state, protocolViolation("results larger than the %d-byte output cap", job.Limits.MaxOutputBytes))
			}
			pipe.close()
			executed, err := outputsOf(m, blob)
			if err != nil {
				return pool.failed(ctx, w, job, &state, err)
			}
			w.runs++
			if !claim(attemptFinished) || w.out.Buffered() > 0 {
				// Killed as the result arrived, or it wrote past its result:
				// the result is whole, the worker is not to be trusted again.
				w.healthy = false
			}
			runErr := m.Error.Decode()
			result, err := job.Finish(executed, runErr)
			// A heap that hit its ceiling, an engine that faulted or a result
			// that did not add up starts the next job in a fresh process.
			if errors.Is(err, jsrun.ErrMemoryLimit) || errors.Is(err, jsrun.ErrEngineFault) {
				w.healthy = false
			}
			return result, err
		default:
			return pool.failed(ctx, w, job, &state, protocolViolation("a %q frame during a job", m.Type))
		}
	}
}

// outputsOf cuts a done frame's blob into the results it carries.
func outputsOf(m message, blob []byte) (jsrun.Executed, error) {
	if m.Executed == nil {
		return jsrun.Executed{}, protocolViolation("a done frame with no result")
	}
	executed := *m.Executed
	executed.Outputs = make([]string, 0, len(m.OutputSizes))
	rest := blob
	for _, size := range m.OutputSizes {
		if size < 0 || size > len(rest) {
			return jsrun.Executed{}, protocolViolation("results that do not add up to what was sent")
		}
		executed.Outputs = append(executed.Outputs, string(rest[:size]))
		rest = rest[size:]
	}
	if len(rest) > 0 {
		return jsrun.Executed{}, protocolViolation("results that do not add up to what was sent")
	}
	return executed, nil
}

// failed ends a job whose worker broke off, and says why in the words the
// runtime uses: the time limit when the deadline killed it, the cancellation
// when the execution was cancelled, the memory limit when it ran out of
// memory, and an engine fault otherwise, naming a broken protocol or the
// worker's exit.
func (pool *Pool) failed(ctx context.Context, w *worker, job jsrun.Job, state *atomic.Int32, cause error) (jsrun.Result, error) {
	w.healthy = false
	var violation *protocolError
	protocol := errors.As(cause, &violation)
	stoppedByUs := false
	if state.CompareAndSwap(attemptRunning, attemptBroken) {
		if !protocol {
			// It closed its end, which it does by exiting: let it finish, so
			// its exit status says why.
			select {
			case <-w.exited:
			case <-time.After(2 * time.Second):
			}
		}
		if w.alive() {
			stoppedByUs = true
			_ = w.cmd.Process.Kill()
		}
	}
	<-w.exited
	switch state.Load() {
	case attemptPastDeadline:
		pool.logger.Warn("a JavaScript worker was killed past its job's deadline", "timeLimit", job.Limits.Timeout)
		return jsrun.Result{}, jsrun.TimeLimitError(job.Limits.Timeout)
	case attemptCancelled:
		return jsrun.Result{}, context.Cause(ctx)
	}
	status, stderr := w.state.String(), w.stderr.String()
	pool.logger.Warn("a JavaScript worker broke off a job", "status", status, "cause", cause, "stderr", stderr)
	switch {
	case protocol:
		return jsrun.Result{}, jsrun.EngineFaultError("its worker process broke the protocol: " + violation.reason)
	case stoppedByUs:
		return jsrun.Result{}, jsrun.EngineFaultError("its worker process stopped answering")
	case ranOutOfMemory(status, stderr):
		return jsrun.Result{}, jsrun.MemoryLimitError(pool.heapCeiling)
	}
	return jsrun.Result{}, jsrun.EngineFaultError("its worker process " + status)
}

// answer is what the server told a job about one node.
type answer struct {
	view  string
	found bool
}

// answer asks the engine what the code asked of it. A node's view is built
// once per job, however often it is asked for. A panic in the engine is the
// server's fault; the code sees no answer, and the server goes on.
func (pool *Pool) answer(host jsrun.Host, question message, views map[string]answer) (reply message, blob string, err error) {
	reply = message{Type: typeReply, Nonce: question.Nonce, ID: question.ID}
	defer func() {
		if recovered := recover(); recovered != nil {
			pool.logger.Error("answering a JavaScript worker panicked", "method", question.Method, "panic", recovered)
			reply, blob = message{Type: typeReply, Nonce: question.Nonce, ID: question.ID, Index: -1, Reason: "this is not available here"}, ""
		}
	}()
	switch question.Method {
	case methodStatic:
		blob = "{}"
		if host.StaticData != nil {
			text, err := host.StaticData(question.Name)
			if err != nil {
				reply.Reason, text = err.Error(), ""
			}
			blob = text
		}
	case methodNode:
		known, asked := views[question.Name]
		if !asked {
			if len(views) >= maxNodeQuestions {
				return reply, "", protocolViolation("questions about more than %d nodes", maxNodeQuestions)
			}
			if host.Node != nil {
				known.view, known.found = host.Node(question.Name)
			}
			views[question.Name] = known
		}
		reply.Found, blob = known.found, known.view
	case methodPair:
		reply.Index, reply.Reason = -1, "item lineage is not available here"
		if host.Pair != nil {
			reply.Index, reply.Reason = host.Pair(question.Name, question.Index)
		}
	default:
		return reply, "", protocolViolation("a question %q", question.Method)
	}
	return reply, blob, nil
}

// helperRequest reads a helper call: a helper the runtime has, carrying no
// more than one call may move.
func helperRequest(m message, blob []byte) (jsrun.HostRequest, error) {
	if m.Request == nil {
		return jsrun.HostRequest{}, protocolViolation("a helper call with no request")
	}
	switch m.Request.Method {
	case jsrun.HelperHTTPRequest, jsrun.HelperReadFile, jsrun.HelperWriteFile:
	default:
		return jsrun.HostRequest{}, protocolViolation("a helper %q", m.Request.Method)
	}
	if len(blob) > jsrun.MaxFileBytes {
		return jsrun.HostRequest{}, protocolViolation("a helper call carrying %d bytes, past the %d one call may move", len(blob), jsrun.MaxFileBytes)
	}
	request := *m.Request
	request.Data = blob
	return request, nil
}

// help answers one helper call, on its own goroutine. A panic in the server's
// helpers fails the call, never the server.
func (pool *Pool) help(ctx context.Context, host jsrun.Host, request jsrun.HostRequest) (answer jsrun.HostAnswer) {
	defer func() {
		if recovered := recover(); recovered != nil {
			pool.logger.Error("a JavaScript helper panicked", "helper", request.Method, "panic", recovered)
			answer = jsrun.HostAnswer{Failure: fmt.Sprintf("the helper failed (%v); this is a fault in the server, not in the code", recovered)}
		}
	}()
	if host.Call == nil {
		return jsrun.HostAnswer{Failure: "this.helpers." + request.Method + " is not available here"}
	}
	return host.Call(ctx, request)
}

// jobPipe is the server's writing end for one job. Once the job is over it
// writes nothing more, so a helper answered late cannot reach the next job.
type jobPipe struct {
	w      *worker
	mu     sync.Mutex
	closed bool
}

func (pipe *jobPipe) write(m message, blob string) error {
	pipe.mu.Lock()
	defer pipe.mu.Unlock()
	if pipe.closed {
		return nil
	}
	return writeFrame(pipe.w.in, m, blob)
}

func (pipe *jobPipe) close() {
	pipe.mu.Lock()
	defer pipe.mu.Unlock()
	pipe.closed = true
}

// deadline kills a worker that runs past its job's allowance. It is held
// while the server answers a helper call: the worker is waiting on the
// server then, and the wait is bounded by the execution's context and the
// helper's own timeout instead.
type deadline struct {
	mu      sync.Mutex
	left    time.Duration
	since   time.Time
	timer   *time.Timer
	held    int
	stopped bool
	fire    func()
}

func newDeadline(allowance time.Duration, fire func()) *deadline {
	return &deadline{left: allowance, since: time.Now(), timer: time.AfterFunc(allowance, fire), fire: fire}
}

func (d *deadline) hold() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held++
	if d.held > 1 || d.timer == nil {
		return
	}
	if !d.timer.Stop() {
		// It fired; there is nothing left to hold.
		d.timer = nil
		return
	}
	d.left -= time.Since(d.since)
}

func (d *deadline) release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held--
	if d.held > 0 || d.timer == nil || d.stopped {
		return
	}
	d.since = time.Now()
	d.timer = time.AfterFunc(max(d.left, 0), d.fire)
}

func (d *deadline) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
	}
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
// only for the single allocation no watchdog can stop. An idle worker already
// reserves well over a gigabyte of address space, hence the baseline.
func addressSpace(heapCeiling uint64) uint64 { return 4*heapCeiling + 3<<30 }

// defaultCommand runs this binary as a worker.
func defaultCommand(heapCeiling uint64) *exec.Cmd {
	cmd := exec.Command(selfExecutable())
	cmd.Env = workerEnvironment(heapCeiling)
	return cmd
}

// workerEnvironment is everything a worker is told about its surroundings:
// the marker that makes it one, how hard its runtime may work, that a crash
// prints its reason without a dump of every goroutine, and the time zone
// settings that decide what `new Date()` shows, as they do in the server.
// None of the server's configuration or secrets.
func workerEnvironment(heapCeiling uint64) []string {
	env := []string{
		markerVariable + "=1",
		// One script and its garbage collector.
		"GOMAXPROCS=2",
		fmt.Sprintf("GOMEMLIMIT=%d", heapCeiling+heapCeiling/2),
		"GOTRACEBACK=none",
	}
	for _, name := range []string{"TZ", "ZONEINFO"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func newNonce() string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	return hex.EncodeToString(nonce[:])
}

// stderrLog keeps the start and the end of what a worker wrote to stderr:
// Go writes why it died first, and a stack after it.
type stderrLog struct {
	mu    sync.Mutex
	limit int
	head  []byte
	tail  []byte
	cut   bool
}

var _ io.Writer = (*stderrLog)(nil)

func (log *stderrLog) Write(p []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	written := len(p)
	if room := log.limit - len(log.head); room > 0 {
		take := min(room, len(p))
		log.head = append(log.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		log.tail = append(log.tail, p...)
		if extra := len(log.tail) - log.limit; extra > 0 {
			log.tail = append(log.tail[:0], log.tail[extra:]...)
			log.cut = true
		}
	}
	return written, nil
}

func (log *stderrLog) String() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	separator := ""
	if log.cut {
		separator = "\n…\n"
	}
	return strings.TrimSpace(string(log.head) + separator + string(log.tail))
}
