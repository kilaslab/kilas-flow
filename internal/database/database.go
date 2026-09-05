// Package database opens and configures kilasflow's internal persistence.
//
// This is kilasflow's own storage only. It is deliberately never exposed to
// workflows: the SQLite/PostgreSQL/MySQL workflow nodes connect to databases
// the user configures through credentials, never to this handle.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/kilaslabs/kilas-flow/internal/config"
)

// DB wraps the GORM handle.
//
// Everything above the repository layer depends on repository interfaces
// rather than on this type, so the ORM stays swappable.
type DB struct {
	*gorm.DB
}

// Open connects to the configured database and verifies it is reachable.
func Open(ctx context.Context, cfg config.Database, log *slog.Logger) (*DB, error) {
	dialector, err := dialectorFor(cfg)
	if err != nil {
		return nil, err
	}

	gormDB, err := gorm.Open(dialector, &gorm.Config{
		Logger:                 gormlogger.Discard,
		SkipDefaultTransaction: true,
		TranslateError:         true,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", cfg.Driver, err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("access underlying sql.DB: %w", err)
	}

	// SQLite tolerates only one writer. Serialising connections avoids
	// SQLITE_BUSY under concurrent workflow execution.
	if cfg.Driver == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		// Through PoolSize rather than straight from the struct, so a caller
		// that built a config.Database by hand — every test in the tree does —
		// gets a bounded pool instead of the unlimited one database/sql reads
		// a zero as. config.Load has already filled these in from
		// execution.max_concurrent for the server itself.
		open, idle := cfg.PoolSize(0)
		sqlDB.SetMaxOpenConns(open)
		sqlDB.SetMaxIdleConns(idle)
	}
	sqlDB.SetConnMaxLifetime(time.Hour)

	db := &DB{gormDB}

	if err := db.Ping(ctx); err != nil {
		return nil, err
	}

	log.Info("database connected", "driver", cfg.Driver)

	return db, nil
}

// Ping reports whether the database is reachable. It backs the /ready probe.
func (db *DB) Ping(ctx context.Context) error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return fmt.Errorf("access underlying sql.DB: %w", err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	return nil
}

// Close releases the connection pool.
func (db *DB) Close() error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}

func dialectorFor(cfg config.Database) (gorm.Dialector, error) {
	switch cfg.Driver {
	case "sqlite":
		dsn, err := sqliteDSN(cfg.DSN)
		if err != nil {
			return nil, err
		}
		return sqlite.Open(dsn), nil

	case "postgres":
		return postgres.Open(cfg.DSN), nil

	default:
		return nil, fmt.Errorf("unsupported database driver %q", cfg.Driver)
	}
}

// SQLitePath is the file a SQLite DSN names, resolved to an absolute path.
//
// Exported because two places have to agree about it and must never derive it
// separately: sqliteDSN opens this file, and the node guard refuses workflow
// credentials that point at it. When the guard's spelling and the opened
// spelling were derived independently, a DSN of `file:./data/kilasflow.db`
// gave the guard a path that filepath.Abs turned into `<cwd>/file:/data/…`,
// which matched nothing — and a credential naming the real file opened with no
// error at all. No ATTACH was needed; a plain SELECT read every credential in
// the installation.
//
// An error here means the DSN is a spelling this function does not understand.
// The caller must treat that as fatal rather than as an empty guard: a guard
// that cannot resolve its own path protects nothing, and the failure is
// invisible.
func SQLitePath(dsn string) (string, error) {
	raw := strings.TrimSpace(dsn)
	if raw == "" {
		return "", fmt.Errorf("the database DSN is empty")
	}
	raw = strings.TrimPrefix(raw, "file:")
	// A URI form carries its options after a ?; the file is what precedes it.
	if cut := strings.IndexByte(raw, '?'); cut >= 0 {
		raw = raw[:cut]
	}
	if raw == "" {
		return "", fmt.Errorf("the database DSN names no file")
	}
	if strings.EqualFold(raw, ":memory:") || strings.HasPrefix(raw, ":") {
		// An in-memory database is not a file, so there is nothing on disk for
		// a credential to reach and nothing to guard.
		return "", nil
	}
	resolved, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("the database DSN %q could not be resolved to a path: %w", dsn, err)
	}
	return resolved, nil
}

// sqliteDSN creates the parent directory and applies kilasflow's required pragmas.
//
// glebarez/sqlite is a pure-Go driver, so the binary builds with CGO_ENABLED=0
// and runs in a distroless image. It takes pragmas as _pragma query parameters
// rather than through a connection hook.
func sqliteDSN(dsn string) (string, error) {
	if strings.HasPrefix(dsn, "file:") {
		// Caller supplied a full URI; assume they know what they want.
		return dsn, nil
	}

	if dir := filepath.Dir(dsn); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("create database directory %s: %w", dir, err)
		}
	}

	pragmas := url.Values{}
	// WAL lets readers proceed during a write, which matters because the
	// scheduler, webhook server and API all touch the same file.
	pragmas.Add("_pragma", "journal_mode(WAL)")
	pragmas.Add("_pragma", "foreign_keys(1)")
	pragmas.Add("_pragma", "busy_timeout(5000)")
	pragmas.Add("_pragma", "synchronous(NORMAL)")

	return "file:" + dsn + "?" + pragmas.Encode(), nil
}
