package sqlnode

import (
	"context"
	"database/sql"
	"time"
)

// OpenForTest opens a connection with a background context.
//
// Exported for tests that assert a credential is refused while its DSN is
// being built — before any dial — so they need no server and no timeout.
func OpenForTest(driver Driver, fields map[string]string, guard Guard) (*Connection, error) {
	return Open(context.Background(), driver, fields, guard)
}

// SetSQLiteConnectForTest replaces the driver connect a SQLite open runs in
// its bounded goroutine, and returns the function that restores it. Tests use
// it to stand in for a driver that blocks where no context reaches.
func SetSQLiteConnectForTest(connect func(path string) (*sql.DB, error)) func() {
	previous := sqliteConnect
	sqliteConnect = connect
	return func() { sqliteConnect = previous }
}

// SetSQLiteOpenTimeoutForTest shortens the bound a SQLite open gets when the
// caller's context carries no deadline.
func SetSQLiteOpenTimeoutForTest(timeout time.Duration) func() {
	previous := sqliteOpenTimeout
	sqliteOpenTimeout = timeout
	return func() { sqliteOpenTimeout = previous }
}

// AbandonedSQLiteOpensForTest reports how many opens returned at their
// deadline and are still waiting on the driver.
func AbandonedSQLiteOpensForTest() int {
	return sqliteOpens.abandonedCount()
}

// MaxAbandonedSQLiteOpens is the cap on opens left waiting on the driver.
const MaxAbandonedSQLiteOpens = maxAbandonedSQLiteOpens
