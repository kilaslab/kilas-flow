package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// executionWakeBaseName is the unprefixed LISTEN/NOTIFY channel executions
// are queued on. The channel a listener subscribes to always carries the
// table prefix (see ExecutionWakeChannel); the base name never goes on the
// wire by itself.
const executionWakeBaseName = "execution_wake"

// ExecutionWakeChannel derives the PostgreSQL LISTEN/NOTIFY channel for
// queued executions from the configured table prefix, so two prefixed
// installs sharing one database do not wake each other's workers.
//
// Advisory locks are per-database rather than per-schema, so any future
// single-scheduler election must key its pg_advisory_lock the same way —
// from this prefix — for the same reason.
func ExecutionWakeChannel(tablePrefix string) string {
	return tablePrefix + executionWakeBaseName
}

// ExecutionWake is one queued-execution notification: identifiers only, never
// the row. PostgreSQL caps a NOTIFY payload at roughly 8000 bytes; a tenant
// and an execution ID are tens of bytes. A woken worker must re-read the row
// through ClaimNext rather than trusting the payload — the execution may
// already be claimed, cancelled, or gone by the time the notification lands.
type ExecutionWake struct {
	TenantID    string `json:"tenant_id"`
	ExecutionID string `json:"execution_id"`
}

// executionWakePayload encodes a queued execution as a NOTIFY payload.
func executionWakePayload(tenantID, executionID string) (string, error) {
	out, err := json.Marshal(ExecutionWake{TenantID: tenantID, ExecutionID: executionID})
	if err != nil {
		return "", fmt.Errorf("encode execution wake payload: %w", err)
	}
	return string(out), nil
}

// parseExecutionWake decodes a NOTIFY payload. A malformed payload drops the
// notification, never the listener: the sender is always this package, so a
// payload that does not decode is a bug to report, not a reason to stop
// waking workers.
func parseExecutionWake(payload string) (ExecutionWake, error) {
	var wake ExecutionWake
	if err := json.Unmarshal([]byte(payload), &wake); err != nil {
		return ExecutionWake{}, fmt.Errorf("decode execution wake payload: %w", err)
	}
	if wake.TenantID == "" || wake.ExecutionID == "" {
		return ExecutionWake{}, fmt.Errorf("decode execution wake payload: tenant and execution IDs are required")
	}
	return wake, nil
}

// notifyExecutionQueued emits pg_notify for one queued execution from inside
// the enqueue transaction, so the notification is delivered exactly when the
// queued row commits and never when the transaction rolls back.
//
// It is a no-op unless tx runs on the PostgreSQL dialector: SQLite has no
// LISTEN/NOTIFY, and the engine's 100 ms poll remains the wake path there.
// An error fails the enqueue — pg_notify on a connection that just inserted
// the row does not fail on its own, and a notification this path dropped
// silently would be a worker that sleeps through its work until the tick.
func notifyExecutionQueued(tx *gorm.DB, tenantID, executionID string) error {
	if !isPostgres(tx) {
		return nil
	}
	payload, err := executionWakePayload(tenantID, executionID)
	if err != nil {
		return err
	}
	// The channel travels as a pg_notify argument rather than as SQL text, so
	// no identifier quoting is needed here; the prefix cannot change what
	// this statement parses as.
	if err := tx.Exec("SELECT pg_notify(?, ?)", ExecutionWakeChannel(tablePrefix(tx)), payload).Error; err != nil {
		return fmt.Errorf("notify queued execution: %w", err)
	}
	return nil
}

// tablePrefix reads the configured table prefix back off a GORM handle, the
// same value database.Open installed as the NamingStrategy. A handle built
// without one reads as no prefix, which is what every existing install has.
func tablePrefix(db *gorm.DB) string {
	if db == nil || db.Config == nil {
		return ""
	}
	if namer, ok := db.Config.NamingStrategy.(schema.NamingStrategy); ok {
		return namer.TablePrefix
	}
	return ""
}

// WatchExecutions LISTENs for queued executions and calls onWake for each
// notification, until ctx is done. It owns its own pgx.Conn built from the
// same DSN: a pooled connection cannot be parked on LISTEN, and taking one
// out of a pool sized for workers would quietly cost a worker a connection.
//
// The engine wires onWake to its existing Wake, so the worker loop keeps its
// current shape and the poll tick keeps working unchanged as the fallback: a
// dropped notification costs latency, never a stuck execution. Exactly one
// worker wakes per notification — the wake channel holds one send — which is
// right for a single queued execution; a burst beyond that waits at most one
// tick, which is cheaper than waking every worker to contend over the same
// rows SKIP LOCKED was added to separate.
//
// A lost listener connection reconnects with backoff and reports each drop
// to onError, so losing it is neither silent nor fatal. onError may be nil,
// in which case drops are retried without reporting; production wiring passes
// one. A malformed payload reports to onError and drops the notification,
// never the listener.
func WatchExecutions(ctx context.Context, dsn, prefix string, onWake func(ExecutionWake), onError func(error)) error {
	channel := ExecutionWakeChannel(prefix)
	// Validated by config, but a listener built by hand should not turn an
	// odd prefix into SQL text: pgx quotes the identifier.
	listen := "LISTEN " + pgx.Identifier{channel}.Sanitize()
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if err := watchExecutionsOnce(ctx, dsn, listen, channel, onWake, onError); err == nil {
			return nil
		} else if ctx.Err() != nil {
			return nil
		} else {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// watchExecutionsOnce holds one listener connection until it drops. A nil
// error means ctx ended and the caller should stop, not reconnect.
func watchExecutionsOnce(ctx context.Context, dsn, listen, channel string, onWake func(ExecutionWake), onError func(error)) error {
	report := func(err error) {
		if onError != nil {
			onError(err)
		}
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		report(fmt.Errorf("execution wake listener connect: %w", err))
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, listen); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		report(fmt.Errorf("execution wake listener listen: %w", err))
		return err
	}
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			report(fmt.Errorf("execution wake listener wait: %w", err))
			return err
		}
		if notification.Channel != channel {
			continue
		}
		wake, err := parseExecutionWake(notification.Payload)
		if err != nil {
			report(err)
			continue
		}
		onWake(wake)
	}
}

// isPostgres reports whether db runs on the PostgreSQL dialector. Exported
// for the rare caller that must branch on driver behaviour — for example a
// test asserting skip-locked SQL — rather than re-deriving the name check.
func isPostgres(db *gorm.DB) bool {
	return db != nil && db.Dialector != nil && strings.EqualFold(db.Dialector.Name(), "postgres")
}
