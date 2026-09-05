package sqlnode

import "context"

// OpenForTest opens a connection with a background context.
//
// Exported for tests that assert a credential is refused while its DSN is
// being built — before any dial — so they need no server and no timeout.
func OpenForTest(driver Driver, fields map[string]string, guard Guard) (*Connection, error) {
	return Open(context.Background(), driver, fields, guard)
}
