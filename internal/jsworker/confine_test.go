package jsworker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

// A worker user that could not take anything away is refused before any
// worker starts, and every run after: the server's own user, or a number the
// kernel would not read as the one configured (past 32 bits it would wrap,
// to root).
func TestAWorkerUserThatTakesNothingAwayIsRefused(t *testing.T) {
	// Built at run time, so a 32-bit build compiles the test at all.
	wide := int64(1) << 32
	cases := map[string]Options{
		"past 32 bits":     {UID: int(wide + 4242), GID: 4343},
		"the invalid user": {UID: int(wide - 1), GID: 4343},
	}
	if os.Geteuid() != 0 {
		cases["the server's own"] = Options{UID: os.Geteuid(), GID: 4343}
	}
	for name, options := range cases {
		pool := New(options)
		t.Cleanup(pool.Close)
		err := pool.Start()
		if err == nil {
			t.Errorf("%s: Start() = nil, want the user refused", name)
			continue
		}
		if runtime.GOOS == "linux" && !strings.Contains(err.Error(), "worker user") {
			t.Errorf("%s: Start() = %v, want it to name the worker user", name, err)
		}
		if _, runErr := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); !errors.Is(runErr, jsrun.ErrEngineFault) {
			t.Errorf("%s: Run() = %v, want every run refused as well", name, runErr)
		}
	}
}

// Start starts a worker at once, and keeps it for the first job.
func TestStartStartsAWorkerForTheFirstJob(t *testing.T) {
	pool := newTestPool(t, Options{})
	if err := pool.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if started := pool.started(); started != 1 {
		t.Fatalf("%d workers started, want the one Start started reused", started)
	}
}

// reprobing puts pool on its weaker profile after an earlier refusal, due to
// ask for strongest again at its next start.
func reprobing(pool *testPool, strongest spawnProfile) {
	weakest := pool.profiles[len(pool.profiles)-1]
	pool.confineMu.Lock()
	defer pool.confineMu.Unlock()
	pool.profiles = []spawnProfile{strongest, weakest}
	pool.profileAt, pool.refusal, pool.refusedAt = 1, "the kernel refused it earlier", time.Time{}
	pool.reprobeAfter = time.Millisecond
}

// Asking again for a stronger profile never costs a run: whatever the
// stronger one meets, a worker that never says it is ready or a start that
// fails for any reason, the run starts with the profile that works, which
// stays the pool's.
func TestAReprobeThatFailsCostsNoRun(t *testing.T) {
	for name, apply := range map[string]func(*exec.Cmd){
		"never ready": func(cmd *exec.Cmd) { cmd.Env = append(cmd.Env, testModeVariable+"=silent") },
		"not started": func(cmd *exec.Cmd) { cmd.Path = "/nonexistent/kilasflow" },
	} {
		t.Run(name, func(t *testing.T) {
			var logs lockedBuffer
			pool := newTestPool(t, Options{MaxRuns: 1, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			pool.readyTimeout = 200 * time.Millisecond
			reprobing(pool, spawnProfile{asks: "more", apply: apply, gives: confinement{Active: []string{layerNetwork}}, stepsDown: true})
			for attempt := range 2 {
				time.Sleep(2 * time.Millisecond)
				if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
					t.Fatalf("run %d: Run() = %v, want it on the profile that works", attempt, err)
				}
			}
			if _, index := pool.profile(); index != 1 {
				t.Errorf("the pool starts workers with profile %d, want the one that works kept", index)
			}
			if pool.refusal != "the kernel refused it earlier" {
				t.Errorf("refusal = %q, want the earlier one kept", pool.refusal)
			}
		})
	}
}
