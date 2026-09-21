package sidecar

import (
	"bytes"
	"io"
	"log/slog"
	"sync"
)

// logBudget is the per-process volume of child output the host will write to
// its log. Past it the writer keeps draining and discards, logging one line to
// say so.
const logBudget = 1 << 20

// NewLogWriter adapts a child's stdout or stderr to the host logger. It
// splits lines, truncates each line to the same 2 KiB bound as a child
// message, and after the per-process budget logs one "output truncated" line.
//
// The important property is the one that is easy to get wrong: Write always
// accepts every byte and returns (len(p), nil). A child writing logs to a
// full pipe blocks forever if the host stops reading, so the drain must never
// stop and must never return an error.
//
// One writer may be shared by every child of a pool: the pool hands the same
// Diag to each spawn, and os/exec copies a child's streams on its own
// goroutine, so Write is called concurrently. The mutex in logWriter is what
// makes that safe; without it, two tenants' children race on the line buffer.
func NewLogWriter(log *slog.Logger, stream string) io.Writer {
	return &logWriter{log: log, stream: stream, budget: logBudget}
}

type logWriter struct {
	mu        sync.Mutex
	log       *slog.Logger
	stream    string
	buffer    []byte
	budget    int
	truncated bool
}

func (writer *logWriter) Write(p []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.buffer = append(writer.buffer, p...)
	for {
		index := bytes.IndexByte(writer.buffer, '\n')
		if index < 0 {
			break
		}
		writer.emit(string(writer.buffer[:index]))
		writer.buffer = writer.buffer[index+1:]
	}
	// A line longer than the per-line bound is emitted early rather than
	// buffered without limit: a package that writes one endless line must not
	// grow the host's buffer.
	if len(writer.buffer) >= maxChildMessage {
		writer.emit(string(writer.buffer))
		writer.buffer = writer.buffer[:0]
	}
	return len(p), nil
}

func (writer *logWriter) emit(line string) {
	if writer.log == nil {
		return
	}
	if writer.budget <= 0 {
		if !writer.truncated {
			writer.truncated = true
			writer.log.Info("sidecar output truncated", "stream", writer.stream)
		}
		return
	}
	line = truncate(line, maxChildMessage)
	writer.budget -= len(line)
	writer.log.Info(line, "stream", writer.stream)
}
