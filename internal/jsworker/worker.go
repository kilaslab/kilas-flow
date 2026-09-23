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
func Serve(in io.Reader, out io.Writer) int {
	// A stop signal is the server's to act on. systemd, a terminal's Ctrl-C
	// and a process group send it to the workers too, and a worker that died
	// of it would fail the run the server is still draining. A worker ends
	// when the server closes its stdin, and on Linux when the server dies.
	signal.Ignore(os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	reader := bufio.NewReaderSize(in, 64<<10)
	writer := bufio.NewWriterSize(out, 64<<10)
	hello, _, err := readFrame(reader, maxServerHeader, maxServerBlob)
	if err != nil || hello.Type != typeHello || hello.Limits == nil {
		return broken("no hello from the server", err)
	}
	limitSelf(hello.AddressSpace)
	runner := jsrun.NewRunner(jsrun.Options{Limits: *hello.Limits, MaxConcurrent: 1, HeapCeiling: hello.HeapCeiling})
	blobLimit := max(int64(maxServerBlob), 2*runner.Limits().MaxInputBytes)
	for {
		request, input, err := readFrame(reader, maxServerHeader, blobLimit)
		if errors.Is(err, io.EOF) {
			return 0
		}
		if err != nil || request.Type != typeRun || request.Job == nil {
			return broken("expected a job", err)
		}
		job := *request.Job
		job.Input = string(input)
		ask := asker{reader: reader, writer: writer, nonce: request.Nonce, blobLimit: blobLimit}
		executed, runErr := runner.Execute(context.Background(), job, ask.host())
		if ask.err != nil {
			return broken("the server stopped answering", ask.err)
		}
		sizes := make([]int, len(executed.Outputs))
		for index, text := range executed.Outputs {
			sizes[index] = len(text)
		}
		done := message{Type: typeDone, Nonce: request.Nonce, Executed: &executed, OutputSizes: sizes, Error: jsrun.EncodeError(runErr)}
		if err := writeFrame(writer, done, strings.Join(executed.Outputs, "")); err != nil {
			return broken("writing a result", err)
		}
	}
}

// broken reports why the stream failed on stderr, which the pool logs.
func broken(what string, err error) int {
	fmt.Fprintf(os.Stderr, "kilasflow js worker: %s: %v\n", what, err)
	return 2
}

// asker answers the code's questions by asking the server. It is used only
// on the VM's goroutine, one question at a time, so the pipes need no lock.
type asker struct {
	reader    *bufio.Reader
	writer    *bufio.Writer
	nonce     string
	blobLimit int64
	// err is the first failure to reach the server. The job still finishes,
	// seeing no answer, and the worker exits after it.
	err error
}

func (a *asker) ask(question message) (message, []byte, bool) {
	if a.err != nil {
		return message{}, nil, false
	}
	question.Type, question.Nonce = typeCall, a.nonce
	if err := writeFrame(a.writer, question, ""); err != nil {
		a.err = err
		return message{}, nil, false
	}
	reply, blob, err := readFrame(a.reader, maxServerHeader, a.blobLimit)
	if err == nil && reply.Type != typeReply {
		err = fmt.Errorf("expected a reply, got %q", reply.Type)
	}
	if err != nil {
		a.err = err
		return message{}, nil, false
	}
	return reply, blob, true
}

func (a *asker) host() jsrun.Host {
	return jsrun.Host{
		Node: func(name string) (string, bool) {
			reply, view, ok := a.ask(message{Method: "node", Name: name})
			if !ok || !reply.Found {
				return "", false
			}
			return string(view), true
		},
		Pair: func(name string, index int) (int, string) {
			reply, _, ok := a.ask(message{Method: "pair", Name: name, Index: index})
			if !ok {
				return -1, "item lineage is not available here"
			}
			return reply.Index, reply.Reason
		},
	}
}
