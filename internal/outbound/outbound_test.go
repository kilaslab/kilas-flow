package outbound_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/outbound"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const testSecret = "whsec-test-secret"

type catalog map[string]workflow.NodeDefinition

func (c catalog) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	definition, found := c[nodeType]
	return definition, found && definition.Version.Compare(version) == 0
}

var manualCatalog = catalog{"kilasflow.manual": {
	Type: "kilasflow.manual", Version: workflow.V(1),
	Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
}}

func openDB(t *testing.T) *database.DB {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"),
	}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// enqueue commits count workflow.activated events bound for endpoint, one
// publish each, and returns their event ids.
func enqueue(t *testing.T, db *database.DB, tenantID, endpoint string, count int) []string {
	t.Helper()
	ctx := context.Background()
	tenant := repository.TenantScope{ID: tenantID}
	workflows := repository.NewWorkflowStore(db.DB).WithOutbox(func(tenant, _ string, _ []byte) (string, bool) {
		return endpoint, tenant == tenantID
	})
	for index := range count {
		id := "wf_" + tenantID + "_" + string(rune('a'+index))
		if _, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion, ID: id, Name: "Outbound fixture",
			Nodes:       []workflow.Node{{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}},
			Connections: []workflow.Connection{}, Settings: map[string]any{},
		}); err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, id, manualCatalog); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}
	}
	rows := logOf(t, db, tenantID)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if len(ids) != count {
		t.Fatalf("enqueued %d events, want %d", len(ids), count)
	}
	return ids
}

func logOf(t *testing.T, db *database.DB, tenantID string) []repository.OutboundEvent {
	t.Helper()
	rows, err := repository.NewOutboxStore(db.DB).List(context.Background(), repository.TenantScope{ID: tenantID}, repository.OutboundFilter{Limit: repository.MaxOutboundPageSize})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return rows
}

// receiver is a test endpoint that records every delivery and answers with
// the status the script gives for that attempt.
type receiver struct {
	server *httptest.Server
	mu     sync.Mutex
	got    []http.Header
	bodies [][]byte
	byID   map[string]int
	script func(attempt int) int
}

func newReceiver(t *testing.T, script func(attempt int) int) *receiver {
	t.Helper()
	recv := &receiver{byID: map[string]int{}, script: script}
	recv.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		recv.mu.Lock()
		recv.got = append(recv.got, request.Header.Clone())
		recv.bodies = append(recv.bodies, body)
		recv.byID[request.Header.Get(outbound.HeaderID)]++
		attempt := len(recv.got)
		recv.mu.Unlock()
		writer.WriteHeader(recv.script(attempt))
	}))
	t.Cleanup(recv.server.Close)
	return recv
}

func (recv *receiver) count() int {
	recv.mu.Lock()
	defer recv.mu.Unlock()
	return len(recv.got)
}

// policyFor admits exactly the receiver's loopback host and port, the way an
// operator admits a sidecar, with the private-network guard left on.
func policyFor(t *testing.T, endpoint string) safehttp.Policy {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse %q: %v", endpoint, err)
	}
	policy := safehttp.DefaultPolicy()
	policy.Timeout = 5 * time.Second
	policy.AllowedHosts = []string{parsed.Hostname()}
	policy.AllowedPrivateEndpoints = []string{parsed.Host}
	return policy
}

// clock is a settable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

func newDispatcher(t *testing.T, db *database.DB, policy safehttp.Policy, now *clock, workerID string, maxAttempts int) *outbound.Dispatcher {
	t.Helper()
	dispatcher, err := outbound.New(outbound.Options{
		Store:       repository.NewOutboxStore(db.DB),
		Secret:      func(string) (string, error) { return testSecret, nil },
		Policy:      policy,
		WorkerID:    workerID,
		MaxAttempts: maxAttempts,
		BaseDelay:   time.Minute,
		MaxDelay:    10 * time.Minute,
		Clock:       now.Now,
		Jitter:      func() float64 { return 0.5 },
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return dispatcher
}

func startClock() *clock {
	return &clock{now: time.Now().UTC().Add(time.Second)}
}

// A delivery is verified by the very verifier a KilasFlow webhook trigger with
// the standardWebhooks preset runs, and a tampered body is not.
func TestSignedDeliveryVerifiesWithTheInboundStandardWebhooksScheme(t *testing.T) {
	body := []byte(`{"id":"evt_1","type":"execution.failed"}`)
	headers := outbound.Sign(testSecret, "evt_1", time.Now(), body)
	if headers.Get(outbound.HeaderID) != "evt_1" || headers.Get(outbound.HeaderTimestamp) == "" || !strings.HasPrefix(headers.Get(outbound.HeaderSignature), "v1,") {
		t.Fatalf("headers = %v", headers)
	}
	verify := webhook.HMACSchemeVerifier(webhook.HMACScheme{
		Header: "webhook-signature", Algorithm: "sha256", Encoding: "base64", Prefix: "v1,",
		SigningString: "{{ .timestamp }}.{{ .body }}", TimestampHeader: "webhook-timestamp", Tolerance: 5 * time.Minute,
	}, func(webhook.Delivery) (string, error) { return testSecret, nil })
	delivery := func(raw []byte) webhook.Delivery {
		request := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(string(raw)))
		request.Header = headers.Clone()
		return webhook.Delivery{Request: request, RawBody: raw, ContentType: "application/json"}
	}
	if err := verify(delivery(body)); err != nil {
		t.Fatalf("the inbound verifier refused an outbound delivery: %v", err)
	}
	if err := verify(delivery([]byte(`{"id":"evt_1","type":"execution.completed"}`))); err == nil {
		t.Fatal("the inbound verifier accepted a tampered body")
	}
}

// A 5xx is retried after the backoff with the same event id and the same
// bytes, and the second attempt's 2xx marks the row delivered; the log shows
// both attempts.
func TestDispatcherRetriesAfterA5xxAndDeliversTheSameEvent(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	recv := newReceiver(t, func(attempt int) int {
		if attempt == 1 {
			return http.StatusServiceUnavailable
		}
		return http.StatusNoContent
	})
	ids := enqueue(t, db, "tenant-retry", recv.server.URL+"/hooks", 1)
	now := startClock()
	dispatcher := newDispatcher(t, db, policyFor(t, recv.server.URL), now, "dispatcher-1", 5)

	if done, err := dispatcher.DispatchOne(ctx); err != nil || !done {
		t.Fatalf("first DispatchOne() = (%v, %v)", done, err)
	}
	row := logOf(t, db, "tenant-retry")[0]
	if row.Status != repository.OutboundPending || row.Attempts != 1 || row.LastStatusCode != http.StatusServiceUnavailable || row.NextAttemptAt == nil {
		t.Fatalf("log after a 503 = %#v", row)
	}
	// The first retry waits BaseDelay jittered into its upper half: with the
	// jitter pinned at 0.5 that is three quarters of a minute.
	if wait := row.NextAttemptAt.Sub(now.Now()); wait < 44*time.Second || wait > 46*time.Second {
		t.Fatalf("first retry is due in %s, want 45s", wait)
	}
	if done, err := dispatcher.DispatchOne(ctx); err != nil || done {
		t.Fatalf("DispatchOne() before the retry is due = (%v, %v), want nothing due", done, err)
	}
	now.Advance(time.Minute)
	if done, err := dispatcher.DispatchOne(ctx); err != nil || !done {
		t.Fatalf("second DispatchOne() = (%v, %v)", done, err)
	}
	row = logOf(t, db, "tenant-retry")[0]
	if row.Status != repository.OutboundDelivered || row.Attempts != 2 || row.LastStatusCode != http.StatusNoContent || row.DeliveredAt == nil || row.LastError != "" {
		t.Fatalf("log after delivery = %#v", row)
	}
	if recv.count() != 2 || recv.byID[ids[0]] != 2 {
		t.Fatalf("receiver saw %d requests, %d for %s; want both attempts under the one event id", recv.count(), recv.byID[ids[0]], ids[0])
	}
	if string(recv.bodies[0]) != string(recv.bodies[1]) {
		t.Fatalf("the retry sent different bytes:\n%s\n%s", recv.bodies[0], recv.bodies[1])
	}
	var envelope struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recv.bodies[1], &envelope); err != nil || envelope.ID != ids[0] || envelope.Type != repository.OutboundWorkflowActivated {
		t.Fatalf("delivered body = %s (%v)", recv.bodies[1], err)
	}
	if recv.got[1].Get("Content-Type") != "application/json" || recv.got[1].Get(outbound.HeaderSignature) == "" {
		t.Fatalf("delivered headers = %v", recv.got[1])
	}
}

// A row that keeps failing is dead after MaxAttempts and is never sent again.
func TestDispatcherDeadLettersAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	recv := newReceiver(t, func(int) int { return http.StatusInternalServerError })
	enqueue(t, db, "tenant-dead", recv.server.URL+"/hooks", 1)
	now := startClock()
	dispatcher := newDispatcher(t, db, policyFor(t, recv.server.URL), now, "dispatcher-1", 3)
	for attempt := 1; attempt <= 3; attempt++ {
		if done, err := dispatcher.DispatchOne(ctx); err != nil || !done {
			t.Fatalf("attempt %d: DispatchOne() = (%v, %v)", attempt, done, err)
		}
		now.Advance(time.Hour)
	}
	row := logOf(t, db, "tenant-dead")[0]
	if row.Status != repository.OutboundDead || row.Attempts != 3 || row.LastStatusCode != http.StatusInternalServerError || row.NextAttemptAt != nil || !strings.Contains(row.LastError, "500") {
		t.Fatalf("log after the last attempt = %#v", row)
	}
	now.Advance(24 * time.Hour)
	if done, err := dispatcher.DispatchOne(ctx); err != nil || done {
		t.Fatalf("DispatchOne() on a dead row = (%v, %v), want nothing due", done, err)
	}
	if recv.count() != 3 {
		t.Fatalf("receiver saw %d requests, want 3", recv.count())
	}
}

// The state change committed and the process died before anything was sent,
// or died holding the lease mid-delivery: either way a dispatcher started
// afterwards delivers the event once its lease, if any, has expired.
func TestDispatcherDeliversAnEventLeftByACrash(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	recv := newReceiver(t, func(int) int { return http.StatusOK })
	ids := enqueue(t, db, "tenant-crash", recv.server.URL+"/hooks", 2)
	now := startClock()

	// The first event: committed, then the process died. A dispatcher that
	// starts afterwards has nothing but the row to go on.
	// The second: a dispatcher claimed it and died before recording anything.
	crashed, found, err := repository.NewOutboxStore(db.DB).ClaimDue(ctx, now.Now(), "survivor", time.Minute)
	if err != nil || !found {
		t.Fatalf("ClaimDue(crashed) = (%v, %v)", found, err)
	}

	survivor := newDispatcher(t, db, policyFor(t, recv.server.URL), now, "survivor", 5)
	if attempted, err := survivor.Drain(ctx); err != nil || attempted != 1 {
		t.Fatalf("Drain() under the crashed lease = (%d, %v), want only the unleased event", attempted, err)
	}
	now.Advance(2 * time.Hour)
	if attempted, err := survivor.Drain(ctx); err != nil || attempted != 1 {
		t.Fatalf("Drain() after the lease expired = (%d, %v), want the abandoned event", attempted, err)
	}
	for _, id := range ids {
		if recv.byID[id] != 1 {
			t.Errorf("event %s delivered %d times, want once", id, recv.byID[id])
		}
	}
	for _, row := range logOf(t, db, "tenant-crash") {
		if row.Status != repository.OutboundDelivered || row.Attempts != 1 {
			t.Errorf("row %s = %s after %d attempts, want delivered after 1", row.ID, row.Status, row.Attempts)
		}
	}
	if recv.byID[crashed.ID] != 1 {
		t.Fatalf("the abandoned event %s was delivered %d times", crashed.ID, recv.byID[crashed.ID])
	}
}

// Several dispatchers draining the same backlog at once deliver every event
// exactly once: the lease, not luck, keeps two of them off one row.
func TestConcurrentDispatchersNeverDeliverTheSameRowTwice(t *testing.T) {
	dispatchConcurrently(t, openDB(t), "tenant-race")
}

// The same race on PostgreSQL, where SKIP LOCKED is real and the dispatchers
// land on different rows instead of contending for one.
func TestConcurrentDispatchersNeverDeliverTheSameRowTwiceOnPostgres(t *testing.T) {
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the PostgreSQL half")
	}
	quiet := quietLogger()
	db, err := database.Open(context.Background(), config.Database{Driver: "postgres", DSN: dsn}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	const tenantID = "drv-outbound-race"
	cleanup := func() {
		for _, table := range []string{"outbound_events", "workflow_publish_events", "workflow_versions", "workflows"} {
			db.Exec("DELETE FROM "+table+" WHERE tenant_id = ?", tenantID)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	dispatchConcurrently(t, db, tenantID)
}

// dispatchConcurrently has four dispatchers drain twelve events at once and
// requires every event delivered exactly once.
func dispatchConcurrently(t *testing.T, db *database.DB, tenantID string) {
	t.Helper()
	ctx := context.Background()
	release := make(chan struct{})
	recv := newReceiver(t, func(int) int {
		// Hold each delivery open until every dispatcher has started, so the
		// claims genuinely overlap rather than running one after another.
		<-release
		return http.StatusOK
	})
	const events, dispatchers = 12, 4
	ids := enqueue(t, db, tenantID, recv.server.URL+"/hooks", events)
	now := startClock()
	var wg sync.WaitGroup
	errs := make(chan error, dispatchers)
	for index := range dispatchers {
		dispatcher := newDispatcher(t, db, policyFor(t, recv.server.URL), now, "dispatcher-"+string(rune('a'+index)), 5)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := dispatcher.Drain(ctx); err != nil {
				errs <- err
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Drain() error = %v", err)
	}
	if recv.count() != events {
		t.Fatalf("receiver saw %d deliveries for %d events", recv.count(), events)
	}
	for _, id := range ids {
		if recv.byID[id] != 1 {
			t.Errorf("event %s delivered %d times, want exactly once", id, recv.byID[id])
		}
	}
	for _, row := range logOf(t, db, tenantID) {
		if row.Status != repository.OutboundDelivered {
			t.Errorf("row %s is %s, want delivered", row.ID, row.Status)
		}
	}
}

// Deliveries go through the egress policy: an endpoint on a loopback address
// the operator did not admit is refused before a connection is made, and the
// refusal is what the log records.
func TestDispatcherRefusesAnEndpointTheEgressPolicyBlocks(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	admitted := newReceiver(t, func(int) int { return http.StatusOK })
	blocked := newReceiver(t, func(int) int { return http.StatusOK })
	enqueue(t, db, "tenant-egress", blocked.server.URL+"/hooks", 1)
	now := startClock()
	// The policy admits the other receiver's port only: the host matches the
	// allowlist, the address is loopback, and the guard stays on.
	dispatcher := newDispatcher(t, db, policyFor(t, admitted.server.URL), now, "dispatcher-1", 5)
	if done, err := dispatcher.DispatchOne(ctx); err != nil || !done {
		t.Fatalf("DispatchOne() = (%v, %v)", done, err)
	}
	if blocked.count() != 0 {
		t.Fatalf("a blocked endpoint received %d requests", blocked.count())
	}
	row := logOf(t, db, "tenant-egress")[0]
	if row.Status != repository.OutboundPending || row.Attempts != 1 || row.LastStatusCode != 0 || row.LastError != "the egress policy refused the endpoint" {
		t.Fatalf("log after a blocked attempt = %#v", row)
	}
}

// An event is never sent unsigned, and a redirect is not followed: both are
// failed attempts the log explains.
func TestDispatcherRefusesToSendUnsignedAndDoesNotFollowRedirects(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	recv := newReceiver(t, func(int) int { return http.StatusFound })
	enqueue(t, db, "tenant-unsigned", recv.server.URL+"/hooks", 1)
	now := startClock()
	unsigned, err := outbound.New(outbound.Options{
		Store: repository.NewOutboxStore(db.DB), Secret: func(string) (string, error) { return "", nil },
		Policy: policyFor(t, recv.server.URL), WorkerID: "unsigned", Clock: now.Now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if done, err := unsigned.DispatchOne(ctx); err != nil || !done {
		t.Fatalf("DispatchOne(unsigned) = (%v, %v)", done, err)
	}
	if recv.count() != 0 {
		t.Fatalf("an unsigned event reached the receiver")
	}
	if row := logOf(t, db, "tenant-unsigned")[0]; !strings.Contains(row.LastError, "unsigned") {
		t.Fatalf("log after the unsigned attempt = %#v", row)
	}

	now.Advance(time.Hour)
	signed := newDispatcher(t, db, policyFor(t, recv.server.URL), now, "signed", 5)
	if done, err := signed.DispatchOne(ctx); err != nil || !done {
		t.Fatalf("DispatchOne(signed) = (%v, %v)", done, err)
	}
	if recv.count() != 1 {
		t.Fatalf("receiver saw %d requests, want the one redirect answer", recv.count())
	}
	row := logOf(t, db, "tenant-unsigned")[0]
	if row.Status != repository.OutboundPending || row.LastStatusCode != http.StatusFound {
		t.Fatalf("log after a redirect = %#v, want a failed attempt with status 302", row)
	}
}

// The backoff doubles per attempt, stops at the cap, and stays inside the
// upper half of each step whatever the jitter draws.
func TestBackoffIsExponentialCappedAndJittered(t *testing.T) {
	db := openDB(t)
	for _, jitter := range []float64{0, 0.999} {
		dispatcher, err := outbound.New(outbound.Options{
			Store: repository.NewOutboxStore(db.DB), Secret: func(string) (string, error) { return testSecret, nil },
			WorkerID: "backoff", BaseDelay: time.Second, MaxDelay: 8 * time.Second,
			Jitter: func() float64 { return jitter },
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		for attempt, ceiling := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second, 5: 8 * time.Second, 60: 8 * time.Second} {
			got := dispatcher.Backoff(attempt)
			if got < ceiling/2 || got > ceiling {
				t.Errorf("jitter %v: Backoff(%d) = %s, want within [%s, %s]", jitter, attempt, got, ceiling/2, ceiling)
			}
		}
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
