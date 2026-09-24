package jsworker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// markerVariable is the environment variable that makes the kilasflow binary
// a worker. It is set only in a worker's own environment, by the pool.
const markerVariable = "KILASFLOW_JS_WORKER"

// IsWorker reports whether this process was started as a worker, which
// cmd/kilasflow asks before anything else.
func IsWorker() bool { return os.Getenv(markerVariable) == "1" }

// A worker trusts the server, so these limits only keep a corrupt stream from
// allocating without end. A blob may be larger when the deployment's input
// cap is.
const (
	maxServerHeader = 256 << 20
	maxServerBlob   = 1 << 30
)

// Serve is a worker's whole life: it reads the server's hello, then runs one
// job at a time from in and writes each result to out, until in closes. It
// returns the process's exit code: 0 when the server closed the pipe, 2 when
// the stream broke.
func Serve(in io.Reader, out io.Writer) int { return serve(in, out, os.Stderr) }

// serve is Serve reporting a broken stream to stderr, which the pool logs.
func serve(in io.Reader, out io.Writer, stderr io.Writer) int {
	// A stop signal is the server's to act on. systemd, a terminal's Ctrl-C
	// and a process group send it to the workers too, and a worker that died
	// of it would fail the run the server is still draining. A worker ends
	// when the server closes its stdin, and on Linux when the server dies.
	signal.Ignore(os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	reader := bufio.NewReaderSize(in, 64<<10)
	writer := bufio.NewWriterSize(out, 64<<10)
	hello, _, err := readFrame(reader, maxServerHeader, maxServerBlob)
	if err != nil || hello.Type != typeHello || hello.Limits == nil {
		return broken(stderr, "no hello from the server", err)
	}
	limitSelf(hello.AddressSpace)
	runner := jsrun.NewRunner(jsrun.Options{Limits: *hello.Limits, MaxConcurrent: 1, HeapCeiling: hello.HeapCeiling})
	blobLimit := max(int64(maxServerBlob), 2*runner.Limits().MaxInputBytes)
	server := newLink(writer)
	go server.read(reader, blobLimit)
	for {
		request, ok := <-server.runs
		if !ok {
			if err := server.failure(); err != nil {
				return broken(stderr, "the stream from the server broke", err)
			}
			return 0
		}
		if request.m.Job == nil {
			return broken(stderr, "expected a job", errors.New("a run frame with no job"))
		}
		job := *request.m.Job
		job.Input = string(request.blob)
		executed, runErr := runner.Execute(context.Background(), job, server.host(request.m.Nonce))
		server.finish()
		if err := server.failure(); err != nil {
			return broken(stderr, "the server stopped answering", err)
		}
		sizes := make([]int, len(executed.Outputs))
		for index, text := range executed.Outputs {
			sizes[index] = len(text)
		}
		done := message{Type: typeDone, Nonce: request.m.Nonce, Executed: &executed, OutputSizes: sizes, Error: jsrun.EncodeError(runErr)}
		if err := server.write(done, strings.Join(executed.Outputs, "")); err != nil {
			return broken(stderr, "writing a result", err)
		}
	}
}

// broken reports why the stream failed on stderr, which the pool logs.
func broken(stderr io.Writer, what string, err error) int {
	fmt.Fprintf(stderr, "kilasflow js worker: %s: %v\n", what, err)
	return 2
}

// frame is one frame as it was read.
type frame struct {
	m    message
	blob []byte
}

// link is the worker's end of the pipes. One goroutine reads every frame the
// server writes and routes it: a job to the loop that runs jobs, a reply to
// the call waiting for it. So a reply can arrive while the code runs on, and
// several calls can be outstanding at once. The server is trusted, but a
// frame that fits no call, or a job while one runs, is a broken stream all
// the same, and ends the worker.
type link struct {
	writeMu sync.Mutex
	writer  *bufio.Writer

	// runs carries each job to the loop, and closes when the stream ends.
	runs chan frame

	mu sync.Mutex
	// current is the running job's nonce, "" between jobs; ended is the job
	// that just finished, whose late replies are dropped.
	current, ended string
	lastID         int64
	waiting        map[int64]chan frame
	// err is why the stream broke; broke closes when it is set.
	err   error
	broke chan struct{}
}

func newLink(writer *bufio.Writer) *link {
	return &link{writer: writer, runs: make(chan frame, 1), waiting: map[int64]chan frame{}, broke: make(chan struct{})}
}

// read routes the server's frames until the stream ends.
func (l *link) read(reader *bufio.Reader, blobLimit int64) {
	defer close(l.runs)
	for {
		m, blob, err := readFrame(reader, maxServerHeader, blobLimit)
		if err != nil {
			// The server closing the pipe between jobs is how a worker is
			// told to stop; anywhere else it broke off.
			if !errors.Is(err, io.EOF) || l.busy() {
				l.fail(err)
			}
			return
		}
		switch m.Type {
		case typeRun:
			if err := l.start(m.Nonce); err != nil {
				l.fail(err)
				return
			}
			l.runs <- frame{m: m, blob: blob}
		case typeReply:
			if err := l.deliver(frame{m: m, blob: blob}); err != nil {
				l.fail(err)
				return
			}
		default:
			l.fail(protocolViolation("a %q frame from the server", m.Type))
			return
		}
	}
}

func (l *link) busy() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current != ""
}

func (l *link) start(nonce string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current != "" {
		return protocolViolation("a job while another was running")
	}
	l.current, l.ended = nonce, ""
	return nil
}

// deliver hands a reply to the call waiting for it. A reply the finished job
// stopped waiting for is dropped: the server may have written it as the job
// ended.
func (l *link) deliver(reply frame) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.current != "" && reply.m.Nonce == l.current:
		waiting, ok := l.waiting[reply.m.ID]
		if !ok {
			return protocolViolation("a reply to call %d, which is not waiting", reply.m.ID)
		}
		delete(l.waiting, reply.m.ID)
		waiting <- reply
		return nil
	case l.ended != "" && reply.m.Nonce == l.ended:
		return nil
	}
	return protocolViolation("a reply for another job")
}

// finish ends the running job. Calls still outstanding are abandoned: their
// callers stopped waiting when the job's VM closed. It holds the write lock,
// so no call of the job can be half sent, or sent after it, where the server
// would read it as the next job's.
func (l *link) finish() {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ended, l.current = l.current, ""
	l.waiting = map[int64]chan frame{}
}

func (l *link) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		l.err = err
		close(l.broke)
	}
}

func (l *link) failure() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *link) write(m message, blob string) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	return writeFrame(l.writer, m, blob)
}

// errJobOver refuses a call made after its job ended, by a goroutine the
// code left behind.
var errJobOver = errors.New("the job this call belongs to is over")

// ask sends one call of job nonce and waits for its reply, until ctx ends or
// the stream breaks. It is safe from any goroutine. A call whose job is over,
// or whose context already ended, is never sent.
func (l *link) ask(ctx context.Context, nonce string, question message, blob string) (frame, error) {
	l.writeMu.Lock()
	l.mu.Lock()
	switch {
	case l.err != nil:
		err := l.err
		l.mu.Unlock()
		l.writeMu.Unlock()
		return frame{}, err
	case l.current != nonce || ctx.Err() != nil:
		l.mu.Unlock()
		l.writeMu.Unlock()
		return frame{}, errJobOver
	}
	l.lastID++
	id := l.lastID
	answered := make(chan frame, 1)
	l.waiting[id] = answered
	question.Type, question.Nonce, question.ID = typeCall, nonce, id
	l.mu.Unlock()
	err := writeFrame(l.writer, question, blob)
	l.writeMu.Unlock()
	if err != nil {
		l.fail(err)
		return frame{}, err
	}
	select {
	case reply := <-answered:
		return reply, nil
	case <-l.broke:
		return frame{}, l.failure()
	case <-ctx.Done():
		// The call stays registered: its reply may still arrive before the
		// job ends, and is then taken by the buffered channel and dropped.
		return frame{}, ctx.Err()
	}
}

// host answers the code's questions for job nonce by asking the server.
func (l *link) host(nonce string) jsrun.Host {
	return jsrun.Host{
		Node: func(name string) (string, bool) {
			reply, err := l.ask(context.Background(), nonce, message{Method: methodNode, Name: name}, "")
			if err != nil || !reply.m.Found {
				return "", false
			}
			return string(reply.blob), true
		},
		Pair: func(name string, index int) (int, string) {
			reply, err := l.ask(context.Background(), nonce, message{Method: methodPair, Name: name, Index: index}, "")
			if err != nil {
				return -1, "item lineage is not available here"
			}
			return reply.m.Index, reply.m.Reason
		},
		StaticData: func(kind string) (string, error) {
			reply, err := l.ask(context.Background(), nonce, message{Method: methodStatic, Name: kind}, "")
			if err != nil {
				return "", errors.New("the workflow static data is not available: the server stopped answering")
			}
			if reply.m.Reason != "" {
				return "", errors.New(reply.m.Reason)
			}
			return string(reply.blob), nil
		},
		Call: func(ctx context.Context, request jsrun.HostRequest) jsrun.HostAnswer {
			reply, err := l.ask(ctx, nonce, message{Method: methodHelper, Request: &request}, string(request.Data))
			if err != nil {
				return jsrun.HostAnswer{Failure: "the server stopped answering: " + err.Error()}
			}
			if reply.m.Answer == nil {
				return jsrun.HostAnswer{Failure: "the server's answer was empty"}
			}
			answer := *reply.m.Answer
			answer.Data = reply.blob
			return answer
		},
	}
}
