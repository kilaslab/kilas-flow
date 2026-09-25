//go:build unix

package sqlnode_test

import (
	"bufio"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// A FIFO or a device is not a database, and opening one can block the driver
// inside open(2) where no context reaches it.
func TestSQLiteRefusesAFIFOAndADevice(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	tenantDir := filepath.Join(root, "acme")
	if err := os.MkdirAll(tenantDir, 0o700); err != nil {
		t.Fatalf("create the tenant directory: %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(tenantDir, "pipe.db"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	_, err := openSQLitePath(guard, "pipe.db")
	assertForbidden(t, err, "regular file")

	unconfined := sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Unconfined: true}}
	for _, path := range []string{filepath.Join(tenantDir, "pipe.db"), "/dev/null"} {
		_, err := openSQLitePath(unconfined, path)
		assertForbidden(t, err, "regular file")
	}
}

// TestHelperHoldsAnSQLiteFileLocked is not a test: it is the second process
// the lock test below needs, because POSIX record locks are per process and a
// lock taken by the test's own process would never conflict with its driver.
func TestHelperHoldsAnSQLiteFileLocked(t *testing.T) {
	path := os.Getenv("KILASFLOW_SQLNODE_LOCK_HELPER")
	if path == "" {
		t.Skip("helper process only")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Len 0 reaches past the end of the file, so the range covers the lock
	// bytes SQLite takes at the one-gigabyte offset.
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &lock); err != nil {
		t.Fatalf("lock: %v", err)
	}
	_, _ = os.Stdout.WriteString("LOCKED\n")
	time.Sleep(time.Minute)
}

// A file another process holds locked still answers a credential test within
// the test's deadline.
func TestASQLiteFileLockedByAnotherProcessAnswersWithinTheDeadline(t *testing.T) {
	t.Parallel()

	root, guard := confinedGuard(t, "acme")
	tenantDir := filepath.Join(root, "acme")
	if err := os.MkdirAll(tenantDir, 0o700); err != nil {
		t.Fatalf("create the tenant directory: %v", err)
	}
	path := filepath.Join(tenantDir, "locked.db")
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := seed.Exec(`CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = seed.Close()

	helper := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsAnSQLiteFileLocked$")
	helper.Env = append(os.Environ(), "KILASFLOW_SQLNODE_LOCK_HELPER="+path)
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatalf("helper stdout: %v", err)
	}
	if err := helper.Start(); err != nil {
		t.Fatalf("start the lock helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	locked := false
	for scanner.Scan() {
		if scanner.Text() == "LOCKED" {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatal("the helper never reported holding the lock")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_ = sqlnode.Test(ctx, sqlnode.DriverSQLite, map[string]string{"path": "locked.db"}, guard)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("a test of a locked file took %s against a 2s deadline", elapsed)
	}
}
