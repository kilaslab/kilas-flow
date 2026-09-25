package sqlnode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SQLiteFiles decides where a SQLite credential's file may live.
//
// A SQLite credential names a file on the server's own disk, and a path read
// as the credential spells it reaches every file the process can open: on a
// multi-tenant install that is every other tenant's databases, and a new file
// anywhere the process can write. Confinement gives each tenant one directory
// under Root and reads the credential's path relative to it.
//
// The zero value refuses every SQLite credential, so a guard built by hand
// gets the strict answer rather than an open one.
type SQLiteFiles struct {
	// Root holds one directory per tenant; a credential's path is relative to
	// its tenant's directory. Empty, with Unconfined unset, disables SQLite
	// credentials.
	Root string
	// Unconfined reads a credential's path as the process would — absolute,
	// or relative to the working directory — which is how SQLite credentials
	// behaved before confinement. It is for single-tenant installs that
	// already point credentials at files elsewhere on disk; every tenant on
	// an unconfined install can open every file the process can.
	Unconfined bool
}

// Enabled reports whether SQLite credentials may be opened at all.
func (files SQLiteFiles) Enabled() bool {
	return files.Unconfined || strings.TrimSpace(files.Root) != ""
}

// ForTenant narrows the guard to the tenant a credential belongs to.
//
// The guard is built once for the process, so each call site copies it with
// the tenant it is acting for: a confined SQLite path means nothing until it
// is known whose directory it is relative to.
func (guard Guard) ForTenant(tenantID string) Guard {
	guard.Tenant = tenantID
	return guard
}

// tenantDirectoryPattern is the tenant ID grammar the tenant API accepts.
// Held to it here as well, because a tenant ID becomes a directory name: one
// with a separator or a parent segment would pick a directory other than its
// own, and on a case-insensitive filesystem "Acme" and "acme" are one
// directory for two tenants.
var tenantDirectoryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// confinedSQLitePath resolves a credential's path inside its tenant's
// directory, refusing anything that could reach outside it.
//
// Every component below the tenant's directory is checked with Lstat rather
// than resolved: a symlink there is refused whatever it points at. A tenant
// cannot make one through a workflow, so finding one means someone else put it
// there, and following it would hand the tenant whatever it names.
func confinedSQLitePath(raw string, guard Guard) (string, error) {
	tenant := guard.Tenant
	if !tenantDirectoryPattern.MatchString(tenant) {
		return "", fmt.Errorf("%w: a SQLite credential is opened for a tenant, and %q is not a tenant ID a directory can be named after", ErrForbiddenTarget, tenant)
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || filepath.VolumeName(raw) != "" {
		return "", fmt.Errorf("%w: a SQLite path is relative to this tenant's SQLite directory; an absolute path is not accepted", ErrForbiddenTarget)
	}
	cleaned := filepath.Clean(raw)
	if !filepath.IsLocal(cleaned) {
		return "", fmt.Errorf("%w: a SQLite path must stay inside this tenant's SQLite directory", ErrForbiddenTarget)
	}
	if cleaned == "." || strings.HasSuffix(raw, "/") {
		return "", fmt.Errorf("%w: a SQLite path must name a regular file, not a directory", ErrForbiddenTarget)
	}

	root, err := filepath.Abs(guard.SQLite.Root)
	if err != nil {
		return "", fmt.Errorf("the SQLite root could not be resolved: %w", err)
	}
	tenantDir := filepath.Join(root, tenant)
	// Created on first use, owner-only: a fresh tenant has no directory yet,
	// and nothing but this process has any business reading it.
	if err := os.MkdirAll(tenantDir, 0o700); err != nil {
		return "", fmt.Errorf("this tenant's SQLite directory could not be created: %w", pathless(err))
	}
	// The root is the operator's, so its own spelling is resolved rather than
	// refused: /var and /private/var on macOS are the same directory.
	base, err := filepath.EvalSymlinks(tenantDir)
	if err != nil {
		return "", fmt.Errorf("this tenant's SQLite directory could not be resolved: %w", pathless(err))
	}

	current := base
	parts := strings.Split(cleaned, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		last := index == len(parts)-1
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if last {
				// SQLite creates it, inside the directory just checked.
				return current, nil
			}
			return "", fmt.Errorf("SQLite path %q goes through a directory that does not exist in this tenant's SQLite directory", raw)
		}
		if err != nil {
			return "", fmt.Errorf("SQLite path %q could not be checked: %w", raw, pathless(err))
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: SQLite path %q goes through a symbolic link, which is not followed", ErrForbiddenTarget, raw)
		}
		if !last && !info.IsDir() {
			return "", fmt.Errorf("SQLite path %q goes through something that is not a directory", raw)
		}
		if last && !info.Mode().IsRegular() {
			return "", fmt.Errorf("%w: SQLite path %q must name a regular file, not a directory, device, FIFO or socket", ErrForbiddenTarget, raw)
		}
	}
	return current, nil
}

// requireRegularFile refuses an existing path that is not a regular file. A
// path that does not exist yet passes: SQLite creates it.
//
// Checked on the unconfined path too. A directory is never a database, and a
// device or a FIFO can block the driver inside open(2), where no context
// reaches it.
func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("SQLite path could not be checked: %w", pathless(err))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: a SQLite path must name a regular file, not a directory, device, FIFO or socket", ErrForbiddenTarget)
	}
	return nil
}

// pathless drops the path an *fs.PathError carries, so an error about the
// tenant's directory does not print where the operator's root is.
func pathless(err error) error {
	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}

// sqliteOpenTimeout bounds a SQLite open when the caller's context carries no
// deadline. A node run's context may have none, and a local file that has not
// opened in this long is not going to.
var sqliteOpenTimeout = 30 * time.Second

// sqliteConnect opens and pings one SQLite file. It is a variable so tests can
// stand in for a driver that blocks.
//
// The driver cannot be interrupted here: it opens the file without a context,
// and only a running statement answers sqlite3_interrupt. That is why the
// caller runs this in a goroutine it is prepared to abandon.
var sqliteConnect = func(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection per node run, as for the network drivers; see Open.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// maxAbandonedSQLiteOpens caps the goroutines left waiting on a driver that
// did not return. Confinement means a tenant cannot name a file that blocks,
// so reaching this at all is a fault; the cap keeps a fault from growing
// without bound.
const maxAbandonedSQLiteOpens = 8

// abandonedOpens tracks opens that returned at their deadline and whose
// goroutine is still inside the driver.
type abandonedOpens struct {
	mu     sync.Mutex
	byPath map[string]int
	total  int
}

var sqliteOpens = &abandonedOpens{byPath: map[string]int{}}

// admit refuses an open that would only join a pile behind one already stuck.
// Opens that return normally are never counted, so two node runs opening the
// same file at once are unaffected.
func (opens *abandonedOpens) admit(path string) error {
	opens.mu.Lock()
	defer opens.mu.Unlock()
	if opens.byPath[path] > 0 {
		return fmt.Errorf("a previous open of this SQLite file has not returned; it is refused until that one does")
	}
	if opens.total >= maxAbandonedSQLiteOpens {
		return fmt.Errorf("%d SQLite opens are stuck in the driver; new SQLite opens are refused until they return", opens.total)
	}
	return nil
}

func (opens *abandonedOpens) abandon(path string) {
	opens.mu.Lock()
	defer opens.mu.Unlock()
	opens.byPath[path]++
	opens.total++
}

func (opens *abandonedOpens) returned(path string) {
	opens.mu.Lock()
	defer opens.mu.Unlock()
	if opens.byPath[path]--; opens.byPath[path] <= 0 {
		delete(opens.byPath, path)
	}
	opens.total--
}

func (opens *abandonedOpens) abandonedCount() int {
	opens.mu.Lock()
	defer opens.mu.Unlock()
	return opens.total
}

// openSQLite opens and pings a resolved SQLite file, returning when ctx ends
// even if the driver has not.
//
// Live, a credential test of a file the driver blocked on never returned: the
// test's in-flight claim was never released, every later test of that
// credential answered 409, and each attempt left a goroutine behind. A node
// run would hold an engine worker the same way. The open runs in a goroutine
// the caller can walk away from; if it later finishes, the handle it produced
// is closed, since nobody is left to use it.
func openSQLite(ctx context.Context, path string) (*sql.DB, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, sqliteOpenTimeout)
		defer cancel()
	}
	if err := sqliteOpens.admit(path); err != nil {
		return nil, err
	}

	type outcome struct {
		db  *sql.DB
		err error
	}
	var (
		mu        sync.Mutex
		abandoned bool
		delivered bool
	)
	done := make(chan outcome, 1)
	go func() {
		db, err := sqliteConnect(path)
		mu.Lock()
		defer mu.Unlock()
		if abandoned {
			if db != nil {
				_ = db.Close()
			}
			sqliteOpens.returned(path)
			return
		}
		delivered = true
		done <- outcome{db: db, err: err}
	}()

	select {
	case result := <-done:
		return result.db, result.err
	case <-ctx.Done():
		mu.Lock()
		if delivered {
			// The open finished while the deadline fired; the result is in
			// the buffered channel and nobody else will close it.
			mu.Unlock()
			result := <-done
			return result.db, result.err
		}
		abandoned = true
		sqliteOpens.abandon(path)
		mu.Unlock()
		return nil, fmt.Errorf("the SQLite file did not open before the deadline: %w", ctx.Err())
	}
}
