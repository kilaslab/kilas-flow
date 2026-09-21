package idempotency_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// wib is the zone every injected clock reads in. This tree runs in UTC+7, and
// the SQLite driver stores a time.Time as text in the value's own zone, so a
// service that forwards the caller's reading instead of the instant is off by
// seven hours here and correct on a UTC machine. The whole suite uses it.
var wib = time.FixedZone("WIB", 7*3600)

// anchor is the instant the injected clocks start at.
func anchor() time.Time { return time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC) }

// testClock is a hand-cranked clock. Retention and leases are measured in
// hours and this suite must not sleep through them.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock { return &testClock{now: now} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.In(wib)
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// openDrivers runs a test against SQLite and, when one is configured, against
// a live PostgreSQL, and hands it a function that opens another handle on the
// same database — which is how a replay is proven to be durable rather than
// remembered by one process.
func openDrivers(t *testing.T, run func(t *testing.T, open func() *database.DB)) {
	t.Helper()

	t.Run("sqlite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kilasflow.db")
		run(t, opener(t, config.Database{Driver: "sqlite", DSN: path}))
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the PostgreSQL half")
		}
		open := opener(t, config.Database{Driver: "postgres", DSN: dsn})
		// The server is shared, so this package's rows are removed rather than
		// the tables dropped. The handle is opened before the cleanup is
		// registered so the delete runs while a connection is still there.
		db := open()
		t.Cleanup(func() {
			db.Exec(`DELETE FROM idempotency_keys WHERE tenant_id LIKE 'drv-idem-%'`)
		})
		run(t, open)
	})
}

func opener(t *testing.T, cfg config.Database) func() *database.DB {
	t.Helper()
	var (
		mu      sync.Mutex
		handles []*database.DB
	)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, db := range handles {
			_ = db.Close()
		}
	})
	return func() *database.DB {
		db, err := database.Open(context.Background(), cfg, quietLogger())
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		if err := database.Migrate(db, quietLogger()); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		mu.Lock()
		handles = append(handles, db)
		mu.Unlock()
		return db
	}
}

// newService builds the service under test. A retention of zero means "an
// hour", because the retention is otherwise a per-test decision.
func newService(t *testing.T, db *database.DB, opts idempotency.Options) *idempotency.Service {
	t.Helper()
	return newServiceOver(t, repository.NewIdempotencyStore(db.DB), opts)
}

func newServiceOver(t *testing.T, repo repository.IdempotencyRepository, opts idempotency.Options) *idempotency.Service {
	t.Helper()
	if opts.Retention == 0 {
		opts.Retention = time.Hour
	}
	if opts.Log == nil {
		opts.Log = quietLogger()
	}
	service, err := idempotency.NewService(repo, opts)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// The tenants in this package begin drv-idem-, which is what the PostgreSQL
// cleanup deletes, and every key carries a per-run suffix so a row a crashed
// run left on a shared server cannot collide with this one.
func idemTenant(name string) string { return "drv-idem-" + name }

func idemKey(name string) string { return fmt.Sprintf("%s-%d", name, time.Now().UnixNano()) }

func runRequest(body any) idempotency.Request {
	return idempotency.Request{Operation: "run-workflow", Target: "wf_1", Body: body}
}

func createdResponse(id string) idempotency.Response {
	return idempotency.Response{Status: 201, Body: map[string]any{"id": id}}
}

// decodeOutcome is the caller's read of a replayed outcome.
func decodeOutcome(t *testing.T, outcome idempotency.Outcome, v any) {
	t.Helper()
	if err := outcome.Decode(v); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
}

// The point of the whole feature: the second request with a key gets the first
// request's answer and the side effect does not happen again.
func TestDoRunsTheSideEffectOnceAndReplaysTheOutcomeOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("once")
		key := idemKey("once")
		body := map[string]any{"input": "hello"}

		var calls atomic.Int32
		fn := func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		}

		first, err := service.Do(ctx, tenant, key, runRequest(body), fn)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if first.Replayed {
			t.Error("the first request reported itself as a replay")
		}
		if first.Outcome.Status != 201 {
			t.Errorf("Outcome.Status = %d, want 201", first.Outcome.Status)
		}
		var created struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, first.Outcome, &created)
		if created.ID != "exec_1" {
			t.Errorf("Outcome id = %q, want exec_1", created.ID)
		}

		second, err := service.Do(ctx, tenant, key, runRequest(body), fn)
		if err != nil {
			t.Fatalf("second Do() error = %v", err)
		}
		if !second.Replayed {
			t.Error("the retry was not reported as a replay")
		}
		if second.Outcome.Status != 201 {
			t.Errorf("replayed status = %d, want 201", second.Outcome.Status)
		}
		var replayed struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, second.Outcome, &replayed)
		if replayed.ID != "exec_1" {
			t.Errorf("replayed id = %q, want exec_1", replayed.ID)
		}
		if calls.Load() != 1 {
			t.Errorf("the side effect ran %d times, want 1", calls.Load())
		}
	})
}

// Reusing a key for a different request is a client mistake, not a retry, and
// answering it with the first outcome would silently discard the second
// request.
func TestDoRefusesAKeyReusedForADifferentRequestOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("reused")
		key := idemKey("reused")

		var calls atomic.Int32
		fn := func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		}
		if _, err := service.Do(ctx, tenant, key, runRequest(map[string]any{"input": "A"}), fn); err != nil {
			t.Fatalf("Do() error = %v", err)
		}

		differentBody := runRequest(map[string]any{"input": "B"})
		differentTarget := runRequest(map[string]any{"input": "A"})
		differentTarget.Target = "wf_2"
		differentOperation := runRequest(map[string]any{"input": "A"})
		differentOperation.Operation = "insert-row"

		for name, request := range map[string]idempotency.Request{
			"body":      differentBody,
			"target":    differentTarget,
			"operation": differentOperation,
		} {
			t.Run(name, func(t *testing.T) {
				_, err := service.Do(ctx, tenant, key, request, fn)
				if !errors.Is(err, idempotency.ErrKeyReused) {
					t.Fatalf("Do() error = %v, want ErrKeyReused", err)
				}
			})
		}
		if calls.Load() != 1 {
			t.Errorf("the side effect ran %d times, want 1: a refused reuse must not run", calls.Load())
		}
	})
}

// Two requests with one key that overlap: the second is told to come back, and
// is not made to wait on the server.
func TestDoAnswersInFlightWhileTheFirstRequestIsRunningOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("inflight")
		key := idemKey("inflight")
		body := map[string]any{"input": "hello"}

		entered := make(chan struct{})
		release := make(chan struct{})
		type outcome struct {
			result idempotency.Result
			err    error
		}
		first := make(chan outcome, 1)
		go func() {
			result, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
				close(entered)
				<-release
				return createdResponse("exec_1"), nil
			})
			first <- outcome{result, err}
		}()
		<-entered

		_, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			t.Error("the in-flight request ran the side effect a second time")
			return createdResponse("exec_2"), nil
		})
		var inFlight *idempotency.InFlightError
		if !errors.As(err, &inFlight) {
			t.Fatalf("the concurrent duplicate answered %v, want *InFlightError", err)
		}
		if inFlight.RetryAfter < time.Second || inFlight.RetryAfter > 5*time.Second {
			t.Errorf("RetryAfter = %s, want it inside [1s, 5s]", inFlight.RetryAfter)
		}

		close(release)
		done := <-first
		if done.err != nil {
			t.Fatalf("the first Do() error = %v", done.err)
		}
		if done.result.Replayed {
			t.Error("the first request reported itself as a replay")
		}

		third, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			t.Error("the key was not recorded, so the retry ran again")
			return createdResponse("exec_3"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		if !third.Replayed {
			t.Error("the retry after the first request finished was not a replay")
		}
	})
}

// A failed request must not burn its key: the client's retry is the same
// request, and refusing it would leave the work undone.
func TestDoReleasesTheKeyWhenTheSideEffectFailsOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("failed")
		key := idemKey("failed")
		body := map[string]any{"input": "hello"}

		boom := errors.New("the workflow could not be queued")
		var calls atomic.Int32
		_, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return idempotency.Response{}, boom
		})
		if err == nil {
			t.Fatal("Do() = nil error for a failed side effect")
		}
		if !errors.Is(err, boom) {
			t.Errorf("Do() error = %v, want the handler's own error", err)
		}
		// The idempotency layer's own failures are wrapped in *StoreError so a
		// handler can tell them from the work's; the work's are not.
		var storeErr *idempotency.StoreError
		if errors.As(err, &storeErr) {
			t.Errorf("Do() wrapped the handler's error as %v", err)
		}

		retry, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		if retry.Replayed {
			t.Error("the retry after a failure was answered from the record")
		}
		if calls.Load() != 2 {
			t.Errorf("the side effect ran %d times, want 2: the failed attempt then the retry", calls.Load())
		}
	})
}

// A panic is a failure too. Without the deferred release the key would be held
// for the whole in-flight window by a request that will never finish.
func TestDoReleasesTheKeyWhenTheSideEffectPanicsOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("panic")
		key := idemKey("panic")
		body := map[string]any{"input": "hello"}

		var calls atomic.Int32
		func() {
			defer func() {
				if recover() == nil {
					t.Error("Do() swallowed the panic")
				}
			}()
			_, _ = service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
				calls.Add(1)
				panic("the node blew up")
			})
		}()

		retry, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		if retry.Replayed {
			t.Error("the retry after a panic was answered from the record")
		}
		if calls.Load() != 2 {
			t.Errorf("the side effect ran %d times, want 2", calls.Load())
		}
	})
}

// A handler may refuse in the response rather than in the error — the row
// handlers answer a bad value with a 422 and no error — and a refusal is not an
// outcome worth replaying: the client corrects its request and sends it again,
// and that request must run rather than be answered from the attempt that
// failed. The caller's own refusal is still what it receives.
func TestDoReleasesTheKeyWhenTheWorkRefusesWithANonSuccessStatusOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("refusal")
		key := idemKey("refusal")
		body := map[string]any{"input": "hello"}

		var calls atomic.Int32
		refusal := idempotency.Response{Status: 422, Body: map[string]any{"detail": "the row is not valid"}}
		first, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return refusal, nil
		})
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if first.Replayed {
			t.Error("the first request reported itself as a replay")
		}
		if first.Outcome.Status != refusal.Status {
			t.Errorf("the caller's outcome status = %d, want %d", first.Outcome.Status, refusal.Status)
		}
		var refused struct {
			Detail string `json:"detail"`
		}
		decodeOutcome(t, first.Outcome, &refused)
		if refused.Detail != "the row is not valid" {
			t.Errorf("the caller's outcome body = %q, want the handler's own body", refused.Detail)
		}

		// The claim was given back, so the corrected request runs: not a
		// replay of the refusal, and not refused as in flight either.
		retry, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v, want the corrected request to run", err)
		}
		if retry.Replayed {
			t.Error("the retry was answered from a record: a refusal was stored")
		}
		if calls.Load() != 2 {
			t.Errorf("the side effect ran %d times, want 2: the refusal then the corrected request", calls.Load())
		}
	})
}

// A client that gave up must not cost the next client its replay: the outcome
// is recorded with a context of its own.
func TestDoRecordsTheOutcomeEvenWhenTheRequestContextIsCancelledOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("cancelled")
		key := idemKey("cancelled")
		body := map[string]any{"input": "hello"}

		first, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			cancel()
			return idempotency.Response{Status: 202, Body: map[string]any{"id": "exec_1"}}, nil
		})
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if first.Replayed {
			t.Error("the first request reported itself as a replay")
		}

		second, err := service.Do(context.Background(), tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			t.Error("the outcome was not recorded, so the retry ran again")
			return createdResponse("exec_2"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		if !second.Replayed || second.Outcome.Status != 202 {
			t.Errorf("the retry = (%+v), want a replayed 202", second)
		}
	})
}

// The recorded form may differ from the form this caller receives: it is what
// the next request with the same key is answered with.
func TestDoRecordsTheRecordFormWhenTheHandlerSuppliesOneOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("record")
		key := idemKey("record")
		body := map[string]any{"input": "hello"}

		first, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			return idempotency.Response{
				Status: 201,
				Body:   map[string]any{"id": "exec_1", "input": map[string]any{"input": "hello"}},
				Record: map[string]any{"id": "exec_1"},
			}, nil
		})
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		var echoed struct {
			ID    string         `json:"id"`
			Input map[string]any `json:"input"`
		}
		decodeOutcome(t, first.Outcome, &echoed)
		if echoed.Input == nil {
			t.Error("the first caller did not receive its own body")
		}

		second, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			t.Error("the retry ran the side effect")
			return createdResponse("exec_2"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		var recorded struct {
			ID    string         `json:"id"`
			Input map[string]any `json:"input"`
		}
		decodeOutcome(t, second.Outcome, &recorded)
		if recorded.ID != "exec_1" {
			t.Errorf("replayed id = %q, want exec_1", recorded.ID)
		}
		if recorded.Input != nil {
			t.Error("the replay carried the input echo that the handler asked to drop")
		}
	})
}

// An outcome can legitimately exceed the cap — an insert echoes the row it
// wrote — and releasing the key over it would re-open the duplicate the
// feature exists to prevent. The compact form is recorded instead.
func TestDoRecordsTheCompactFormWhenTheOutcomeExceedsTheCapOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("compact")
		key := idemKey("compact")
		body := map[string]any{"input": "hello"}

		huge := strings.Repeat("x", int(idempotency.MaxOutcomeBytes)+64*1024)
		var calls atomic.Int32
		first, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return idempotency.Response{
				Status:  201,
				Body:    map[string]any{"id": "row_1", "data": huge},
				Compact: map[string]any{"id": "row_1"},
			}, nil
		})
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		var full struct {
			ID   string `json:"id"`
			Data string `json:"data"`
		}
		decodeOutcome(t, first.Outcome, &full)
		if len(full.Data) != len(huge) {
			t.Errorf("the first caller received %d bytes of data, want the full %d", len(full.Data), len(huge))
		}

		second, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("row_2"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		if !second.Replayed {
			t.Fatal("the retry was not a replay: the oversize outcome released the key")
		}
		var compact struct {
			ID   string `json:"id"`
			Data string `json:"data"`
		}
		decodeOutcome(t, second.Outcome, &compact)
		if compact.ID != "row_1" {
			t.Errorf("replayed id = %q, want row_1", compact.ID)
		}
		if compact.Data != "" {
			t.Error("the replay carried data the handler asked to compact away")
		}
		if calls.Load() != 1 {
			t.Errorf("the side effect ran %d times, want 1", calls.Load())
		}
	})
}

// A handler that supplies no compact form and produces an outcome above the
// cap has nothing that fits, so the key is freed and the caller is warned. The
// warning must not name the key: it is a credential of sorts, and logs travel.
func TestDoReleasesTheKeyWhenNoFormFitsTheCapOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		sink := &logSink{}
		service := newService(t, open(), idempotency.Options{
			Now: clock.Now,
			Log: slog.New(slog.NewTextHandler(sink, nil)),
		})
		tenant := idemTenant("oversize")
		key := idemKey("oversize")
		body := map[string]any{"input": "hello"}

		huge := strings.Repeat("x", int(idempotency.MaxOutcomeBytes)+64*1024)
		var calls atomic.Int32
		first, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return idempotency.Response{Status: 201, Body: map[string]any{"id": "row_1", "data": huge}}, nil
		})
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if first.Replayed {
			t.Error("the first request reported itself as a replay")
		}

		second, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("row_2"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		if second.Replayed {
			t.Error("the key was not released, so the retry replayed an unrecorded key")
		}
		if calls.Load() != 2 {
			t.Errorf("the side effect ran %d times, want 2: nothing was recorded, so the key is free", calls.Load())
		}
		logged := sink.String()
		if !strings.Contains(logged, "compact") {
			t.Errorf("the warning about the unrecorded outcome is missing from the log: %q", logged)
		}
		if strings.Contains(logged, key) {
			t.Errorf("the log named the idempotency key: %q", logged)
		}
	})
}

// The layer's own failures must be distinguishable from the work's, because
// the datastore handlers answer anything unrecognised with 422 and internal
// text.
func TestDoWrapsItsOwnFailuresInAStoreErrorOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		cause := errors.New("the database is gone")
		service := newServiceOver(t, claimFailer{
			GORMIdempotencyStore: repository.NewIdempotencyStore(open().DB),
			err:                  cause,
		}, idempotency.Options{Now: clock.Now})

		var calls atomic.Int32
		_, err := service.Do(ctx, idemTenant("norepo"), idemKey("norepo"), runRequest(map[string]any{"input": "hello"}),
			func(context.Context) (idempotency.Response, error) {
				calls.Add(1)
				return createdResponse("exec_1"), nil
			})
		var storeErr *idempotency.StoreError
		if !errors.As(err, &storeErr) {
			t.Fatalf("Do() error = %v, want a *StoreError", err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("Do() error = %v, want it to wrap the store's cause", err)
		}
		if calls.Load() != 0 {
			t.Errorf("the side effect ran %d times, want 0", calls.Load())
		}
	})
}

// The claim is a lease, not a lock: a request whose process died must not hold
// its key for ever.
func TestDoTakesOverAClaimAbandonedPastTheInFlightWindowOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("takeover")
		key := idemKey("takeover")
		body := map[string]any{"input": "hello"}

		entered := make(chan struct{})
		release := make(chan struct{})
		type outcome struct {
			result idempotency.Result
			err    error
		}
		abandoned := make(chan outcome, 1)
		go func() {
			result, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
				close(entered)
				<-release
				return createdResponse("exec_1"), nil
			})
			abandoned <- outcome{result, err}
		}()
		<-entered

		clock.Advance(idempotency.DefaultInFlightWindow + time.Minute)
		var calls atomic.Int32
		taken, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_2"), nil
		})
		if err != nil {
			t.Fatalf("the takeover error = %v", err)
		}
		if taken.Replayed {
			t.Fatal("the request after the in-flight window was answered from the abandoned claim")
		}
		var created struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, taken.Outcome, &created)
		if created.ID != "exec_2" {
			t.Errorf("the takeover produced %q, want exec_2", created.ID)
		}

		close(release)
		done := <-abandoned
		if done.err != nil {
			t.Fatalf("the abandoned request's error = %v", done.err)
		}
		if calls.Load() != 1 {
			t.Errorf("the new owner's side effect ran %d times, want 1", calls.Load())
		}

		third, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			t.Error("the takeover's outcome was overwritten by the abandoned claim")
			return createdResponse("exec_3"), nil
		})
		if err != nil {
			t.Fatalf("the retry error = %v", err)
		}
		if !third.Replayed {
			t.Fatal("the retry was not a replay")
		}
		var replayed struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, third.Outcome, &replayed)
		if replayed.ID != "exec_2" {
			t.Errorf("the replay produced %q, want the new owner's exec_2", replayed.ID)
		}
	})
}

// Retention is the promise, and it has two sides: inside it the key replays,
// past it the key is forgotten and the request runs again.
func TestDoForgetsAKeyAfterRetentionOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Retention: time.Hour, Now: clock.Now})
		tenant := idemTenant("retention")
		key := idemKey("retention")
		body := map[string]any{"input": "hello"}

		var calls atomic.Int32
		fn := func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse(fmt.Sprintf("exec_%d", calls.Load())), nil
		}
		if _, err := service.Do(ctx, tenant, key, runRequest(body), fn); err != nil {
			t.Fatalf("Do() error = %v", err)
		}

		clock.Advance(59 * time.Minute)
		inside, err := service.Do(ctx, tenant, key, runRequest(body), fn)
		if err != nil {
			t.Fatalf("the retry inside the window error = %v", err)
		}
		if !inside.Replayed {
			t.Error("a retry inside the retention window was not a replay")
		}

		clock.Advance(2 * time.Minute)
		past, err := service.Do(ctx, tenant, key, runRequest(body), fn)
		if err != nil {
			t.Fatalf("the retry past the window error = %v", err)
		}
		if past.Replayed {
			t.Error("a retry past the retention window was answered from the record")
		}
		if calls.Load() != 2 {
			t.Errorf("the side effect ran %d times, want 2", calls.Load())
		}
	})
}

// A key is one tenant's. Answering another tenant's request from it would leak
// both the outcome and the fact that the key exists.
func TestKeysAreScopedByTenantOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenantA := idemTenant("scope-a")
		tenantB := idemTenant("scope-b")
		key := idemKey("scope")

		var calls atomic.Int32
		fn := func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse(fmt.Sprintf("exec_%d", calls.Load())), nil
		}

		first, err := service.Do(ctx, tenantA, key, runRequest(map[string]any{"input": "a"}), fn)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		var createdA struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, first.Outcome, &createdA)

		// A different body under the same key is the other tenant's business:
		// it neither replays nor conflicts with tenant A's request.
		other, err := service.Do(ctx, tenantB, key, runRequest(map[string]any{"input": "b"}), fn)
		if err != nil {
			t.Fatalf("the other tenant's Do() error = %v", err)
		}
		if other.Replayed {
			t.Error("another tenant was answered from this tenant's key")
		}
		var createdB struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, other.Outcome, &createdB)
		if createdB.ID == createdA.ID {
			t.Errorf("both tenants got %q, want one execution each", createdB.ID)
		}

		again, err := service.Do(ctx, tenantA, key, runRequest(map[string]any{"input": "a"}), fn)
		if err != nil {
			t.Fatalf("the first tenant's retry error = %v", err)
		}
		if !again.Replayed {
			t.Error("the first tenant's retry was not a replay")
		}
		var replayedA struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, again.Outcome, &replayedA)
		if replayedA.ID != createdA.ID {
			t.Errorf("the first tenant replayed %q, want its own %q", replayedA.ID, createdA.ID)
		}
		if calls.Load() != 2 {
			t.Errorf("the side effect ran %d times, want 2", calls.Load())
		}
	})
}

// The property the feature is bought for, under load: N concurrent requests
// with one key produce one execution, and the rest are told what happened.
func TestExactlyOneOfManyConcurrentRequestsExecutesOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("concurrent")
		key := idemKey("concurrent")
		body := map[string]any{"input": "hello"}

		const requests = 16
		var calls atomic.Int32
		fn := func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			time.Sleep(20 * time.Millisecond)
			return createdResponse("exec_1"), nil
		}

		var (
			wg      sync.WaitGroup
			start   = make(chan struct{})
			results = make([]idempotency.Result, requests)
			errs    = make([]error, requests)
		)
		for i := range requests {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i], errs[i] = service.Do(ctx, tenant, key, runRequest(body), fn)
			}()
		}
		close(start)
		wg.Wait()

		var executed, replayed, inFlight int
		for i := range requests {
			switch {
			case errs[i] == nil && !results[i].Replayed:
				executed++
			case errs[i] == nil && results[i].Replayed:
				replayed++
			default:
				var refusal *idempotency.InFlightError
				if !errors.As(errs[i], &refusal) {
					t.Errorf("request %d answered %v, want a replay or an in-flight refusal", i, errs[i])
					continue
				}
				if refusal.RetryAfter < time.Second || refusal.RetryAfter > 5*time.Second {
					t.Errorf("request %d RetryAfter = %s, want it inside [1s, 5s]", i, refusal.RetryAfter)
				}
				inFlight++
			}
		}
		if executed != 1 {
			t.Errorf("%d of %d requests executed the side effect, want exactly 1", executed, requests)
		}
		if calls.Load() != 1 {
			t.Errorf("the side effect ran %d times, want 1", calls.Load())
		}
		if replayed+inFlight != requests-1 {
			t.Errorf("%d replays and %d refusals for %d requests, want them to account for the rest", replayed, inFlight, requests)
		}

		follow, err := service.Do(ctx, tenant, key, runRequest(body), fn)
		if err != nil {
			t.Fatalf("the follow-up error = %v", err)
		}
		if !follow.Replayed {
			t.Error("the follow-up was not a replay")
		}
	})
}

// Two replicas share nothing but the database, so a retry that lands on the
// other one must still be a replay.
func TestAKeyRecordedByOneReplicaReplaysOnAnotherOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		options := idempotency.Options{Now: clock.Now}
		first := newService(t, open(), options)
		second := newService(t, open(), options)
		tenant := idemTenant("replica")
		key := idemKey("replica")
		body := map[string]any{"input": "hello"}

		var calls atomic.Int32
		if _, err := first.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		}); err != nil {
			t.Fatalf("the first replica's Do() error = %v", err)
		}

		replayed, err := second.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_2"), nil
		})
		if err != nil {
			t.Fatalf("the second replica's Do() error = %v", err)
		}
		if !replayed.Replayed {
			t.Fatal("the second replica did not see the first one's key")
		}
		var created struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, replayed.Outcome, &created)
		if created.ID != "exec_1" {
			t.Errorf("the second replica replayed %q, want exec_1", created.ID)
		}
		if calls.Load() != 1 {
			t.Errorf("the side effect ran %d times, want 1", calls.Load())
		}
	})
}

// Durable means durable: closing the database and opening it again is what a
// restart is, and the key must still be there.
func TestAKeyOutlivesARestartOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		options := idempotency.Options{Now: clock.Now}
		db := open()
		service := newService(t, db, options)
		tenant := idemTenant("restart")
		key := idemKey("restart")
		body := map[string]any{"input": "hello"}

		var calls atomic.Int32
		if _, err := service.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		}); err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		restarted := newService(t, open(), options)
		replayed, err := restarted.Do(ctx, tenant, key, runRequest(body), func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_2"), nil
		})
		if err != nil {
			t.Fatalf("the restarted Do() error = %v", err)
		}
		if !replayed.Replayed {
			t.Fatal("the key did not survive the restart")
		}
		var created struct {
			ID string `json:"id"`
		}
		decodeOutcome(t, replayed.Outcome, &created)
		if created.ID != "exec_1" {
			t.Errorf("after the restart the key replayed %q, want exec_1", created.ID)
		}
		if calls.Load() != 1 {
			t.Errorf("the side effect ran %d times, want 1", calls.Load())
		}
	})
}

func TestDoRejectsAnInvalidKeyAndAnEmptyTenant(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx := context.Background()
		clock := newTestClock(anchor())
		service := newService(t, open(), idempotency.Options{Now: clock.Now})
		body := map[string]any{"input": "hello"}

		var calls atomic.Int32
		fn := func(context.Context) (idempotency.Response, error) {
			calls.Add(1)
			return createdResponse("exec_1"), nil
		}

		for name, key := range map[string]string{
			"empty":     "",
			"too long":  strings.Repeat("a", idempotency.MaxKeyLength+1),
			"space":     "a b",
			"newline":   "a\nb",
			"not ascii": "ključ",
		} {
			t.Run("refuses "+name, func(t *testing.T) {
				_, err := service.Do(ctx, idemTenant("invalid"), key, runRequest(body), fn)
				if !errors.Is(err, idempotency.ErrInvalidKey) {
					t.Fatalf("Do() error = %v, want ErrInvalidKey", err)
				}
			})
		}
		if _, err := service.Do(ctx, "", idemKey("notenant"), runRequest(body), fn); err == nil {
			t.Error("Do() accepted an empty tenant")
		} else {
			var storeErr *idempotency.StoreError
			if !errors.As(err, &storeErr) {
				t.Errorf("Do() error = %v, want a *StoreError", err)
			}
		}
		if calls.Load() != 0 {
			t.Errorf("the side effect ran %d times, want 0", calls.Load())
		}
	})
}

// claimFailer is a store whose claim fails. The idempotency layer's own
// failures are what it exists to wrap.
type claimFailer struct {
	*repository.GORMIdempotencyStore
	err error
}

func (f claimFailer) Claim(context.Context, repository.IdempotencyClaim) (repository.IdempotencyClaimResult, error) {
	return repository.IdempotencyClaimResult{}, f.err
}

// logSink collects a log so a test can assert what was and was not written.
type logSink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// jsonBody is a body the hash tests can spell exactly.
func jsonBody(raw string) idempotency.Request {
	return idempotency.Request{Operation: "run-workflow", Target: "wf_1", Body: json.RawMessage(raw)}
}
