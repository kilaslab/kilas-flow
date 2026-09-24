package jsworker

import (
	"bytes"
	"context"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// lockedBuffer is a log destination the pool's goroutines may share.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// confinementLines are the log records that say how the workers are
// confined.
func confinementLines(logs string) []string {
	var lines []string
	for line := range strings.Lines(logs) {
		if strings.Contains(line, "msg=\"JavaScript workers ") && strings.Contains(line, "confined") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

// An operator reads once how the workers are confined, what is in place and
// what the platform would not grant, not a line per worker.
func TestHowTheWorkersAreConfinedIsLoggedOnce(t *testing.T) {
	var logs lockedBuffer
	pool := newTestPool(t, Options{MaxRuns: 1, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	for range 3 {
		if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("a")}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if starts := pool.started(); starts != 3 {
		t.Fatalf("%d workers started, want one per job", starts)
	}
	lines := confinementLines(logs.String())
	if len(lines) != 1 {
		t.Fatalf("confinement logged %d times, want once:\n%s", len(lines), logs.String())
	}
	if runtime.GOOS != "linux" {
		if !strings.Contains(lines[0], "not confined") || !strings.Contains(lines[0], "Linux only") {
			t.Fatalf("log = %s, want it to say the workers are not confined off Linux", lines[0])
		}
		return
	}
	for _, layer := range []string{layerLandlock, layerUndumpable} {
		if !strings.Contains(logAttr(lines[0], "active"), layer) {
			t.Errorf("log = %s, want %s active", lines[0], layer)
		}
	}
}

// logAttr is one attribute's value in a text-handler log line.
func logAttr(line, key string) string {
	_, rest, found := strings.Cut(line, " "+key+"=")
	if !found {
		return ""
	}
	if value, err := strconv.QuotedPrefix(rest); err == nil {
		unquoted, _ := strconv.Unquote(value)
		return unquoted
	}
	value, _, _ := strings.Cut(rest, " ")
	return value
}

// A confined worker opens no file, but a script's time zones still resolve
// as they do in the server: the zone database is the one thing it may read,
// and the server's local zone is loaded before anything is taken away.
func TestTimeZonesResolveInAConfinedWorker(t *testing.T) {
	t.Setenv("TZ", "Asia/Jakarta")
	pool := newTestPool(t, Options{})
	result, err := pool.Run(context.Background(), jsrun.Task{
		Source: "return [{ json: {\n" +
			"  local: new Date(0).getHours(),\n" +
			"  newYork: new Intl.DateTimeFormat('en-US', { timeZone: 'America/New_York', hour: 'numeric', hour12: false }).format(new Date(0)),\n" +
			"} }]",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Items[0].JSON; got["local"] != float64(7) || got["newYork"] != "19" {
		t.Fatalf("zones = %v, want Jakarta's 7 and New York's 19", got)
	}
}

// The server's own command, the running binary started again, starts a
// worker however it is confined: on Linux through /proc/self/exe, which a
// worker in namespaces of its own still reaches.
func TestTheDefaultCommandStartsAWorker(t *testing.T) {
	pool := New(Options{})
	t.Cleanup(pool.Close)
	result, err := pool.Run(context.Background(), jsrun.Task{Source: "return [{ json: { ok: true } }]"})
	if err != nil || len(result.Items) != 1 || result.Items[0].JSON["ok"] != true {
		t.Fatalf("Run() = %#v, %v", result.Items, err)
	}
}

// BenchmarkAJobOnAFreshWorker measures a worker's cold start, confinement
// included: every job runs on a worker started for it. Keeping workers per
// tenant would cost this each time a worker changed hands.
func BenchmarkAJobOnAFreshWorker(b *testing.B) {
	pool := New(Options{MaxRuns: 1})
	b.Cleanup(pool.Close)
	for b.Loop() {
		if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAJobOnAWarmWorker is the same job on a worker that is reused.
func BenchmarkAJobOnAWarmWorker(b *testing.B) {
	pool := New(Options{})
	b.Cleanup(pool.Close)
	for b.Loop() {
		if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
			b.Fatal(err)
		}
	}
}
