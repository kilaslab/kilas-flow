package sidecar

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// countingHandler records how much the logger was asked to write, so the
// writer's budget can be checked without a buffer that would itself be the
// thing under test.
type countingHandler struct {
	mu       sync.Mutex
	records  int
	written  int
	messages []string
}

func (handler *countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *countingHandler) Handle(_ context.Context, record slog.Record) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.records++
	handler.written += len(record.Message)
	handler.messages = append(handler.messages, record.Message)
	return nil
}

func (handler *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *countingHandler) WithGroup(string) slog.Handler      { return handler }

// A child that floods the log must never block or error the host's Write, and
// the host must stop storing the flood after the budget.
func TestLogWriterBoundsLinesAndVolume(t *testing.T) {
	handler := &countingHandler{}
	writer := NewLogWriter(slog.New(handler), "stdout")

	line := strings.Repeat("x", 4<<10) + "\n"
	for i := 0; i < 400; i++ { // 1.6 MiB, past the 1 MiB budget
		written, err := writer.Write([]byte(line))
		if err != nil {
			t.Fatalf("Write() error = %v; the writer must always drain", err)
		}
		if written != len(line) {
			t.Fatalf("Write() = %d, want %d bytes accepted", written, len(line))
		}
	}
	// A partial line past the per-line bound must not grow the buffer.
	if _, err := writer.Write([]byte(strings.Repeat("y", 8<<10))); err != nil {
		t.Fatalf("Write() of a long partial line error = %v", err)
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.written > logBudget+maxChildMessage {
		t.Errorf("logger stored %d bytes, want at most the %d budget", handler.written, logBudget)
	}
	truncated := false
	for _, message := range handler.messages {
		if strings.Contains(message, "truncated") {
			truncated = true
		}
		if len(message) > maxChildMessage+len("…(truncated)") {
			t.Errorf("logged a %d-byte line, want each line bounded at %d", len(message), maxChildMessage)
		}
	}
	if !truncated {
		t.Error("the writer never said its output was truncated")
	}
}

// A writer with no logger still accepts every byte.
func TestLogWriterWithoutLoggerDrains(t *testing.T) {
	writer := NewLogWriter(nil, "stderr")
	if written, err := writer.Write([]byte("hello\n")); err != nil || written != 6 {
		t.Errorf("Write() = (%d, %v), want (6, nil)", written, err)
	}
}

// A pool hands one writer to every child it spawns, and os/exec copies each
// child's streams on its own goroutine, so Write is called concurrently. This
// pins the race the pool would otherwise hit: two tenants' children sharing
// one line buffer. Run with -race, it is the regression test for that.
func TestLogWriterIsSafeForConcurrentChildren(t *testing.T) {
	handler := &countingHandler{}
	writer := NewLogWriter(slog.New(handler), "sidecar")

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 200 {
				if _, err := writer.Write([]byte("child output line\n")); err != nil {
					t.Errorf("Write() error = %v", err)
					return
				}
			}
		}()
	}
	wait.Wait()

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.written == 0 {
		t.Error("the logger wrote nothing; the concurrent writers produced no output")
	}
}
