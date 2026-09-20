package main

// The datastore fleet boot proof: the composition root migrates the fleet
// after database.Migrate and refuses boot when it cannot, exactly as a failed
// schema migration does, then repeats the pass on a slow tick so a datastore
// an older peer created behind clears itself instead of pinning /api/v1/ready
// at 503 until a restart.
//
// The refusal tests re-execute this test binary's real main() against a
// seeded SQLite file: the assertion is the whole refusal sentence, so a child
// that died earlier (config, database open) cannot satisfy it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// errFleetTestSentinel is the failure a fake migrator reports.
var errFleetTestSentinel = errors.New("fleet test sentinel")

// syncBuffer collects output written from another goroutine (a child process
// pipe or the resume tick) without racing the test's reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// fleetTestLog returns a logger that keeps its output in the buffer.
func fleetTestLog(buf *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

// fleetTestEngine opens a migrated SQLite database and builds the engine over
// it, the way run() does.
func fleetTestEngine(t *testing.T, path string) (*database.DB, *datastore.Engine) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{Driver: "sqlite", DSN: path}, log)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	engine, err := datastore.NewEngine(db, "")
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return db, engine
}

// stubFleetMigrator stands in for the engine so the retry loop and the error
// wrapping can be exercised without a live database.
type stubFleetMigrator struct {
	migrated int
	err      error
	calls    int32
}

func (s *stubFleetMigrator) MigrateFleet(context.Context) (int, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.migrated, s.err
}

func TestMigrateDatastoreFleetIsANoOpOnACurrentFleet(t *testing.T) {
	_, engine := fleetTestEngine(t, filepath.Join(t.TempDir(), "kilasflow.db"))
	if _, err := engine.Create(context.Background(), "tenant-1", "contacts",
		[]datastore.ColumnInput{{Name: "title", Type: "string"}}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var buf syncBuffer
	log := fleetTestLog(&buf)
	for pass := 1; pass <= 2; pass++ {
		if err := migrateDatastoreFleet(context.Background(), engine, log); err != nil {
			t.Fatalf("pass %d error = %v, want nil", pass, err)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("a fleet already current logged %q, want silence", buf.String())
	}
}

func TestMigrateDatastoreFleetNamesTheDatastoreAheadOfTheBuild(t *testing.T) {
	db, engine := fleetTestEngine(t, filepath.Join(t.TempDir(), "kilasflow.db"))
	ds, err := engine.Create(context.Background(), "tenant-1", "contacts",
		[]datastore.ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := db.Exec("UPDATE datastores SET schema_version = ? WHERE id = ?", 99, ds.ID).Error; err != nil {
		t.Fatalf("push the datastore ahead: %v", err)
	}

	var buf syncBuffer
	err = migrateDatastoreFleet(context.Background(), engine, fleetTestLog(&buf))
	want := fmt.Sprintf("migrate the datastore fleet: datastore: %s is at schema version 99 but this build knows version %d",
		ds.ID, datastore.CurrentSchemaVersion)
	if err == nil {
		t.Fatalf("migrateDatastoreFleet() error = nil, want %q", want)
	}
	if err.Error() != want {
		t.Errorf("migrateDatastoreFleet() error = %q, want %q", err.Error(), want)
	}
}

func TestMigrateDatastoreFleetLogsWhatItMigrated(t *testing.T) {
	fake := &stubFleetMigrator{migrated: 3}
	var buf syncBuffer
	if err := migrateDatastoreFleet(context.Background(), fake, fleetTestLog(&buf)); err != nil {
		t.Fatalf("migrateDatastoreFleet() error = %v, want nil", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "migrated datastores to the current schema version") {
		t.Errorf("log = %q, want the migration message", logged)
	}
	if !strings.Contains(logged, "datastores=3") {
		t.Errorf("log = %q, want datastores=3", logged)
	}
}

func TestMigrateDatastoreFleetWrapsTheFailure(t *testing.T) {
	fake := &stubFleetMigrator{err: errFleetTestSentinel}
	var buf syncBuffer
	err := migrateDatastoreFleet(context.Background(), fake, fleetTestLog(&buf))
	if !errors.Is(err, errFleetTestSentinel) {
		t.Fatalf("migrateDatastoreFleet() error = %v, want it to wrap the sentinel", err)
	}
	if !strings.HasPrefix(err.Error(), "migrate the datastore fleet: ") {
		t.Errorf("migrateDatastoreFleet() error = %q, want the boot prefix", err.Error())
	}
}

func TestFleetResumerRetriesAndStops(t *testing.T) {
	fake := &stubFleetMigrator{err: errFleetTestSentinel}
	var buf syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runFleetResumer(ctx, fake, 5*time.Millisecond, fleetTestLog(&buf))
	}()

	// The loop must survive its errors: three calls proves it retried past at
	// least two failures rather than stopping at the first.
	if !waitForCalls(fake, 3, 5*time.Second) {
		cancel()
		t.Fatalf("resumer made %d calls in 5s, want at least 3", atomic.LoadInt32(&fake.calls))
	}
	if logged := buf.String(); !strings.Contains(logged, errFleetTestSentinel.Error()) {
		t.Errorf("resumer log = %q, want it to name the failure", logged)
	}

	before := atomic.LoadInt32(&fake.calls)
	if !waitForCalls(fake, before+1, 5*time.Second) {
		cancel()
		t.Fatalf("resumer stopped after %d calls, want it to keep going", before)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runFleetResumer did not return within 2s of the context ending")
	}
	stopped := atomic.LoadInt32(&fake.calls)
	time.Sleep(30 * time.Millisecond)
	if after := atomic.LoadInt32(&fake.calls); after != stopped {
		t.Errorf("resumer called MigrateFleet %d more times after it returned", after-stopped)
	}
}

// waitForCalls polls until the fake has been called at least want times.
func waitForCalls(fake *stubFleetMigrator, want int32, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if atomic.LoadInt32(&fake.calls) >= want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// fleetTestFreePort reserves a real port for the child to bind. Port 0 is not
// an option: config.Validate refuses server.port < 1, so the child would die at
// config load with a message that never names the datastore.
func fleetTestFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// bootHelperEnv is the child's environment: the developer's shell must not
// change the boot, so every KILASFLOW_* entry is dropped before the ones this
// test needs are added.
func bootHelperEnv(dsn string, port int) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, config.EnvPrefix) {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"KILASFLOW_TEST_BOOT_HELPER=1",
		"KILASFLOW_DATABASE_DSN="+dsn,
		"KILASFLOW_SERVER_HOST=127.0.0.1",
		fmt.Sprintf("KILASFLOW_SERVER_PORT=%d", port),
	)
}

// seedFleetBootDatabase creates a migrated SQLite database holding one
// datastore, forces its catalogue version, and closes the file so the child
// can open it. It returns the datastore id.
func seedFleetBootDatabase(t *testing.T, path string, version int) string {
	t.Helper()
	db, engine := fleetTestEngine(t, path)
	ds, err := engine.Create(context.Background(), "tenant-1", "contacts",
		[]datastore.ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := db.Exec("UPDATE datastores SET schema_version = ? WHERE id = ?", version, ds.ID).Error; err != nil {
		t.Fatalf("set schema_version = %d: %v", version, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close the seeded database: %v", err)
	}
	return ds.ID
}

// runFleetBootChild boots the real main() in a child process against the
// seeded database and returns its stderr and exit error.
func runFleetBootChild(t *testing.T, dsn string, port int) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBootHelperProcess$")
	cmd.Dir = t.TempDir()
	cmd.Env = bootHelperEnv(dsn, port)
	var stdout, stderr syncBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("boot helper timed out; stderr: %s", stderr.String())
	}
	return stderr.String(), err
}

// TestBootHelperProcess is not a test: it is the child half of the boot
// tests, selected by -test.run and told to run by its environment.
func TestBootHelperProcess(t *testing.T) {
	if os.Getenv("KILASFLOW_TEST_BOOT_HELPER") != "1" {
		t.Skip("helper process for the boot tests")
	}
	// An explicit empty path means environment-only configuration, the
	// spelling scripts/smoke-sqlite.sh uses.
	os.Args = []string{"kilasflow", "-config", ""}
	main()
	t.Fatal("boot did not refuse")
}

func TestBootRefusesADatastoreAheadOfTheBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ahead.db")
	id := seedFleetBootDatabase(t, path, 99)

	stderr, err := runFleetBootChild(t, path, fleetTestFreePort(t))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("boot helper error = %v, want an exit status; stderr: %s", err, stderr)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("boot exit code = %d, want 1; stderr: %s", exitErr.ExitCode(), stderr)
	}
	want := fmt.Sprintf("kilasflow: migrate the datastore fleet: datastore: %s is at schema version 99 but this build knows version %d",
		id, datastore.CurrentSchemaVersion)
	if !strings.Contains(stderr, want) {
		t.Errorf("boot stderr = %q, want it to contain %q", stderr, want)
	}
}

func TestBootRefusesADatastoreTheBinaryHasNoStepFor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "behind.db")
	id := seedFleetBootDatabase(t, path, 0)

	stderr, err := runFleetBootChild(t, path, fleetTestFreePort(t))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("boot helper error = %v, want an exit status; stderr: %s", err, stderr)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("boot exit code = %d, want 1; stderr: %s", exitErr.ExitCode(), stderr)
	}
	want := fmt.Sprintf("kilasflow: migrate the datastore fleet: datastore: %s is at schema version 0 but this build knows no step from version 0 to %d",
		id, datastore.CurrentSchemaVersion)
	if !strings.Contains(stderr, want) {
		t.Errorf("boot stderr = %q, want it to contain %q", stderr, want)
	}
}

// TestBootRunsTheFleetPassAfterTheSchemaMigration automates the ordering
// proof: a pass placed before database.Migrate would fail on a missing table
// and the child would exit 1 before answering readiness.
func TestBootRunsTheFleetPassAfterTheSchemaMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a real server process")
	}
	path := filepath.Join(t.TempDir(), "fresh.db")
	port := fleetTestFreePort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBootHelperProcess$")
	cmd.Dir = t.TempDir()
	cmd.Env = bootHelperEnv(path, port)
	var stderr syncBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the boot helper: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		<-exited
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/ready", port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case err := <-exited:
			t.Fatalf("boot helper exited before readiness (%v); stderr: %s", err, stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server did not answer /api/v1/ready with 200 within 30s; stderr: %s", stderr.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}
