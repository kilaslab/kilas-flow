package sqlnode_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// internalTargetFor builds the installation's own database identity the way
// startup does: ParseInternalTarget over the internal DSN. The installation
// itself runs on postgres; the MySQL target is the same shape with MySQL's
// driver and port, proving the guard compares per driver rather than
// harbouring a postgres-only check.
func internalTargetFor(t *testing.T, driver sqlnode.Driver) *sqlnode.InternalTarget {
	t.Helper()

	if driver == sqlnode.DriverMySQL {
		return &sqlnode.InternalTarget{Driver: driver, Host: "pg.internal", Port: "3306", Database: "kilasflow"}
	}
	internal, err := sqlnode.ParseInternalTarget(
		driver,
		"postgres://kflow:s3cret@pg.internal:5432/kilasflow?sslmode=require",
	)
	if err != nil {
		t.Fatalf("ParseInternalTarget() error = %v", err)
	}
	return internal
}

// internalPortFor is the installation's own port per driver.
func internalPortFor(driver sqlnode.Driver) string {
	if driver == sqlnode.DriverMySQL {
		return "3306"
	}
	return "5432"
}

// internalLoopbackGuard threads the installation's own database identity the
// way startup does, carried on the guard alongside the process policy. The
// stub resolver gives the internal hostname and its alias the same loopback
// address, so a different spelling of one server still compares equal
// without needing DNS.
func internalLoopbackGuard(t *testing.T, driver sqlnode.Driver) sqlnode.Guard {
	t.Helper()

	loopback := net.ParseIP("127.0.0.1")
	return sqlnode.Guard{
		Internal: internalTargetFor(t, driver),
		Policy:   safehttp.Policy{AllowPrivateNetworks: true},
		LookupIPAddr: func(_ context.Context, host string) ([]net.IP, error) {
			switch strings.ToLower(host) {
			case "pg.internal", "db.alias.test":
				return []net.IP{loopback}, nil
			default:
				return nil, fmt.Errorf("unknown test host %q", host)
			}
		},
	}
}

func internalCredential(host, port, database string) map[string]string {
	return map[string]string{
		"host": host, "port": port, "database": database,
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	}
}

// A credential that resolves to the installation's own database is refused
// before anything dials, however the operator spelled the same target: a
// different hostname for the same address, a different port notation, a
// different case, or different connection options are all the same database.
// Proven for every network driver, because the installation's own driver is
// the one an operator is most likely to point a credential at — and MySQL
// needs the same refusal for the day it is.
func TestInternalDatabaseRefusedHoweverItIsSpelled(t *testing.T) {
	t.Parallel()

	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		guard := internalLoopbackGuard(t, driver)
		port := internalPortFor(driver)
		notations := map[string]string{"5432": "05432", "3306": "03306"}
		for _, fields := range []map[string]string{
			internalCredential("pg.internal", port, "kilasflow"),
			internalCredential("db.alias.test", port, "kilasflow"),
			internalCredential("pg.internal", notations[port], "kilasflow"),
			internalCredential("PG.INTERNAL", port, "kilasflow"),
		} {
			_, err := sqlnode.OpenForTest(driver, fields, guard)
			assertForbidden(t, err, "own internal database")
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("refusal leaked the internal password: %v", err)
			}
		}
	}
}

// A neighbour on the same server is a different target and passes the
// internal-database guard: a different database name, a different port, or
// an unnamed database. The egress policy still applies — these pass the
// guard and fail later, at the closed port, which is the observable
// difference.
func TestNeighbourDatabasePassesTheInternalGuard(t *testing.T) {
	t.Parallel()

	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		guard := internalLoopbackGuard(t, driver)
		port := internalPortFor(driver)
		otherPort := "5433"
		if driver == sqlnode.DriverMySQL {
			otherPort = "3307"
		}
		for _, fields := range []map[string]string{
			internalCredential("pg.internal", port, "tenant_app"),
			internalCredential("pg.internal", otherPort, "kilasflow"),
			internalCredential("pg.internal", port, ""),
		} {
			connection, err := sqlnode.Open(context.Background(), driver, fields, guard)
			if err == nil {
				// Something actually listens there; the guard passed, which
				// is the assertion, so close what it opened.
				_ = connection.Close()
				continue
			}
			if errors.Is(err, sqlnode.ErrForbiddenTarget) {
				t.Errorf("%s neighbour database was refused as internal: %v", driver, err)
			}
		}
	}
	// A credential for another driver cannot resolve to this installation's
	// database at all: a MySQL credential naming the postgres internal's
	// host, port and database passes the guard (driver mismatch) and fails
	// at the dial.
	postgresGuard := internalLoopbackGuard(t, sqlnode.DriverPostgres)
	mysqlFields := internalCredential("pg.internal", "5432", "kilasflow")
	if _, err := sqlnode.Open(context.Background(), sqlnode.DriverMySQL, mysqlFields, postgresGuard); errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Errorf("a MySQL credential was refused as the postgres internal: %v", err)
	}
}

// A name that resolves elsewhere when checked and at the installation when
// dialled is still refused: the dial-time check re-resolves and re-compares,
// so the pre-flight is the clearer message but never the control.
func TestInternalDatabaseRebindsAtDialTime(t *testing.T) {
	t.Parallel()

	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		internal := internalTargetFor(t, driver)
		var calls int
		guard := sqlnode.Guard{
			Internal: internal,
			Policy:   safehttp.Policy{AllowPrivateNetworks: true},
			LookupIPAddr: func(_ context.Context, host string) ([]net.IP, error) {
				calls++
				if strings.EqualFold(host, "pg.internal") {
					return []net.IP{net.ParseIP("127.0.0.1")}, nil
				}
				if calls == 1 {
					return []net.IP{net.ParseIP("93.184.216.34")}, nil
				}
				return []net.IP{net.ParseIP("127.0.0.1")}, nil
			},
		}
		fields := internalCredential("db.evil.test", internalPortFor(driver), "kilasflow")
		err := openWithTimeout(t, driver, fields, guard)
		assertForbidden(t, err, "own internal database")
		if calls < 3 {
			t.Errorf("%s resolved the host %d time(s), want at least 3 (pre-flight, dial, internal)", driver, calls)
		}
	}
}

// The startup normalisation is what the guard compares, so it is proven on
// its own: defaults, both URL schemes, IPv6, and every DSN shape that must
// refuse to boot rather than guard nothing.
func TestParseInternalTarget(t *testing.T) {
	t.Parallel()

	for _, setup := range []struct {
		name     string
		driver   sqlnode.Driver
		dsn      string
		host     string
		port     string
		database string
	}{
		{"full DSN", sqlnode.DriverPostgres, "postgres://kflow:pw@db.internal:5432/kilasflow?sslmode=require", "db.internal", "5432", "kilasflow"},
		{"default port", sqlnode.DriverPostgres, "postgres://kflow:pw@db.internal/kilasflow", "db.internal", "5432", "kilasflow"},
		{"postgresql scheme", sqlnode.DriverPostgres, "postgresql://kflow:pw@db.internal:5433/other?sslmode=disable", "db.internal", "5433", "other"},
		{"ipv6 host", sqlnode.DriverPostgres, "postgres://kflow:pw@[::1]:5432/kilasflow", "::1", "5432", "kilasflow"},
		{"options do not move the target", sqlnode.DriverPostgres, "postgres://kflow:pw@db.internal:5432/kilasflow?sslmode=disable&connect_timeout=10", "db.internal", "5432", "kilasflow"},
	} {
		t.Run(setup.name, func(t *testing.T) {
			t.Parallel()

			target, err := sqlnode.ParseInternalTarget(setup.driver, setup.dsn)
			if err != nil {
				t.Fatalf("ParseInternalTarget(%q) error = %v", setup.dsn, err)
			}
			if target.Driver != setup.driver || target.Host != setup.host || target.Port != setup.port || target.Database != setup.database {
				t.Errorf("ParseInternalTarget(%q) = %+v, want host %q port %q database %q",
					setup.dsn, target, setup.host, setup.port, setup.database)
			}
		})
	}

	for _, setup := range []struct {
		name   string
		driver sqlnode.Driver
		dsn    string
	}{
		{"empty DSN", sqlnode.DriverPostgres, ""},
		{"not a URL", sqlnode.DriverPostgres, "://bad url\\\\"},
		{"wrong scheme", sqlnode.DriverPostgres, "mysql://kflow:pw@db.internal/kilasflow"},
		{"no host", sqlnode.DriverPostgres, "postgres:///kilasflow"},
		{"bad port", sqlnode.DriverPostgres, "postgres://kflow:pw@db.internal:notaport/kilasflow"},
		{"sqlite has no network identity", sqlnode.DriverSQLite, "./data/kilasflow.db"},
		{"mysql is never the install driver", sqlnode.DriverMySQL, "kflow:pw@tcp(db.internal:3306)/kilasflow"},
	} {
		t.Run(setup.name, func(t *testing.T) {
			t.Parallel()

			if _, err := sqlnode.ParseInternalTarget(setup.driver, setup.dsn); err == nil {
				t.Errorf("ParseInternalTarget(%q) = nil, want an error the caller must treat as fatal", setup.dsn)
			}
		})
	}
}

// The file guard stands beside the network one: a guard carrying both still
// refuses a SQLite credential naming the installation's own file.
func TestSQLiteGuardStandsBesideTheNetworkGuard(t *testing.T) {
	t.Parallel()

	internal := filepath.Join(t.TempDir(), "kilasflow.db")
	if err := os.WriteFile(internal, []byte("db"), 0o600); err != nil {
		t.Fatalf("write the internal database: %v", err)
	}
	guard := internalLoopbackGuard(t, sqlnode.DriverPostgres)
	guard.InternalPaths = []string{internal}

	_, err := sqlnode.OpenForTest(sqlnode.DriverSQLite, map[string]string{"path": internal}, guard)
	assertForbidden(t, err, "KilasFlow's own database")
}
