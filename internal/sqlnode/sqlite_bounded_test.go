package sqlnode_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// These tests replace the driver's connect with one that blocks, so they swap
// package state and must not run in parallel with each other or with the
// parallel tests (which Go only starts once the sequential ones are done).

// blockingConnect stands in for the driver when it blocks inside its open,
// which only statements can be interrupted out of: the connect ignores every
// context it is given. Paths in blocked hang until release is called.
type blockingConnect struct {
	mu      sync.Mutex
	blocked map[string]chan struct{}
}

func newBlockingConnect(paths ...string) *blockingConnect {
	connect := &blockingConnect{blocked: map[string]chan struct{}{}}
	for _, path := range paths {
		connect.blocked[path] = make(chan struct{})
	}
	return connect
}

func (connect *blockingConnect) open(path string) (*sql.DB, error) {
	connect.mu.Lock()
	gate, blocked := connect.blocked[filepath.Base(path)]
	connect.mu.Unlock()
	if blocked {
		<-gate
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (connect *blockingConnect) release(path string) {
	connect.mu.Lock()
	defer connect.mu.Unlock()
	if gate, found := connect.blocked[path]; found {
		close(gate)
		delete(connect.blocked, path)
	}
}

func (connect *blockingConnect) releaseAll() {
	connect.mu.Lock()
	defer connect.mu.Unlock()
	for path, gate := range connect.blocked {
		close(gate)
		delete(connect.blocked, path)
	}
}

func waitForNoAbandonedOpens(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for sqlnode.AbandonedSQLiteOpensForTest() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d abandoned opens never finished", sqlnode.AbandonedSQLiteOpensForTest())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Live, a credential test of a file the driver blocked on never returned: the
// test's in-flight claim was never released and every later test answered 409.
// Open must return at the caller's deadline whatever the driver does.
func TestASQLiteOpenThatBlocksReturnsAtTheDeadline(t *testing.T) {
	connect := newBlockingConnect("stuck.db")
	defer sqlnode.SetSQLiteConnectForTest(connect.open)()
	defer waitForNoAbandonedOpens(t)
	defer connect.releaseAll()

	_, guard := confinedGuard(t, "acme")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := sqlnode.Open(ctx, sqlnode.DriverSQLite, map[string]string{"path": "stuck.db"}, guard)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an open the driver never finished reported success")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Errorf("Open returned after %s, want about the 200ms deadline", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open is still blocked 5s after a 200ms deadline")
	}

	// sqlnode.Test is what the credential endpoint calls; it must return too.
	testCtx, testCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer testCancel()
	testDone := make(chan error, 1)
	go func() {
		testDone <- sqlnode.Test(testCtx, sqlnode.DriverSQLite, map[string]string{"path": "stuck.db"}, guard)
	}()
	select {
	case err := <-testDone:
		if err == nil {
			t.Fatal("a test of a blocked file reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Test is still blocked 5s after a 200ms deadline")
	}
}

// A node run's context may carry no deadline at all. The connect has its own
// bound, so a blocked file cannot hold an engine worker for ever.
func TestASQLiteOpenWithoutADeadlineIsStillBounded(t *testing.T) {
	connect := newBlockingConnect("stuck.db")
	defer sqlnode.SetSQLiteConnectForTest(connect.open)()
	defer sqlnode.SetSQLiteOpenTimeoutForTest(200 * time.Millisecond)()
	defer waitForNoAbandonedOpens(t)
	defer connect.releaseAll()

	_, guard := confinedGuard(t, "acme")
	done := make(chan error, 1)
	go func() {
		_, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite, map[string]string{"path": "stuck.db"}, guard)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an open the driver never finished reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open without a deadline is still blocked 5s after the 200ms open bound")
	}
}

// The goroutine left behind by an abandoned open cannot be killed, so opens
// must not pile up behind it: the same file is refused at once until the
// stuck open returns, and the file is usable again once it does.
func TestAnAbandonedSQLiteOpenBlocksOnlyItsOwnFileUntilItReturns(t *testing.T) {
	connect := newBlockingConnect("stuck.db")
	defer sqlnode.SetSQLiteConnectForTest(connect.open)()
	defer waitForNoAbandonedOpens(t)
	defer connect.releaseAll()

	_, guard := confinedGuard(t, "acme")
	open := func(path string, timeout time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		connection, err := sqlnode.Open(ctx, sqlnode.DriverSQLite, map[string]string{"path": path}, guard)
		if err == nil {
			_ = connection.Close()
		}
		return err
	}

	if err := open("stuck.db", 100*time.Millisecond); err == nil {
		t.Fatal("a blocked open reported success")
	}
	if got := sqlnode.AbandonedSQLiteOpensForTest(); got != 1 {
		t.Fatalf("abandoned opens = %d, want 1", got)
	}

	started := time.Now()
	err := open("stuck.db", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "has not returned") {
		t.Fatalf("a second open of the stuck file = %v, want an immediate refusal", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("the refusal took %s, want it immediate rather than another wait", elapsed)
	}

	if err := open("other.db", 5*time.Second); err != nil {
		t.Fatalf("a different file was refused while another was stuck: %v", err)
	}

	connect.release("stuck.db")
	waitForNoAbandonedOpens(t)
	if err := open("stuck.db", 5*time.Second); err != nil {
		t.Fatalf("the file stayed refused after its stuck open returned: %v", err)
	}
}

// However many different files block, the number of goroutines left waiting
// on the driver is capped.
func TestAbandonedSQLiteOpensAreCapped(t *testing.T) {
	limit := sqlnode.MaxAbandonedSQLiteOpens
	paths := make([]string, 0, limit+1)
	for index := 0; index <= limit; index++ {
		paths = append(paths, fmt.Sprintf("stuck-%d.db", index))
	}
	connect := newBlockingConnect(paths...)
	defer sqlnode.SetSQLiteConnectForTest(connect.open)()
	defer waitForNoAbandonedOpens(t)
	defer connect.releaseAll()

	_, guard := confinedGuard(t, "acme")
	for _, path := range paths[:limit] {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, err := sqlnode.Open(ctx, sqlnode.DriverSQLite, map[string]string{"path": path}, guard)
		cancel()
		if err == nil {
			t.Fatalf("blocked open of %s reported success", path)
		}
	}
	if got := sqlnode.AbandonedSQLiteOpensForTest(); got != limit {
		t.Fatalf("abandoned opens = %d, want %d", got, limit)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := sqlnode.Open(ctx, sqlnode.DriverSQLite, map[string]string{"path": paths[limit]}, guard)
	if err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("an open past the cap = %v, want an immediate refusal naming the stuck opens", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("the refusal took %s, want it immediate", elapsed)
	}
}
