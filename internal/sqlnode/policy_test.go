package sqlnode_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// loopbackFields is a credential pointing at the machine itself: the oldest
// SSRF target a database credential can name.
func loopbackFields(port string) map[string]string {
	return map[string]string{
		"host": "127.0.0.1", "port": port, "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	}
}

func openWithTimeout(t *testing.T, driver sqlnode.Driver, fields map[string]string, guard sqlnode.Guard) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := sqlnode.Open(ctx, driver, fields, guard)
	if err != nil {
		return err
	}
	return connection.Close()
}

func assertForbidden(t *testing.T, err error, wantReason string) {
	t.Helper()
	if err == nil {
		t.Fatal("a forbidden database target was accepted")
	}
	if !errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Fatalf("error = %v, want ErrForbiddenTarget", err)
	}
	if !strings.Contains(err.Error(), wantReason) {
		t.Errorf("error = %v, want reason %q", err, wantReason)
	}
	// The refusal names the host and the reason. It must never carry the DSN
	// the credential store just decrypted out of.
	for _, secret := range []string{"hunter2", "postgres://ada", "ada:hunter2"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("refusal leaked credential material %q: %v", secret, err)
		}
	}
}

// A credential naming a loopback address is refused before anything dials,
// for every network driver, with the reason the HTTP policy reports.
func TestNetworkPolicyRefusesLoopbackPerDriver(t *testing.T) {
	t.Parallel()

	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		err := openWithTimeout(t, driver, loopbackFields("1"), sqlnode.Guard{})
		assertForbidden(t, err, "loopback")
	}
}

// The cloud metadata address is link-local, which is the single most valuable
// SSRF target on a hosted install.
func TestNetworkPolicyRefusesTheMetadataService(t *testing.T) {
	t.Parallel()

	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		fields := loopbackFields("80")
		fields["host"] = "169.254.169.254"
		err := openWithTimeout(t, driver, fields, sqlnode.Guard{})
		assertForbidden(t, err, "link-local")
	}
}

// The refusal is made against the resolved address, not the credential's host
// text: a hostname that resolves to loopback is refused without needing DNS.
func TestNetworkPolicyChecksTheResolvedAddress(t *testing.T) {
	t.Parallel()

	guard := sqlnode.Guard{
		LookupIPAddr: func(_ context.Context, host string) ([]net.IP, error) {
			if host != "db.partner.test" {
				t.Errorf("resolver asked for %q, want db.partner.test", host)
			}
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	}
	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		fields := loopbackFields("5432")
		fields["host"] = "db.partner.test"
		err := openWithTimeout(t, driver, fields, guard)
		assertForbidden(t, err, "loopback")
	}
}

// A name that resolves to a public address when checked and to loopback when
// dialled is still refused: the dial-time check is the control, the pre-flight
// only the clearer message.
func TestNetworkPolicyRebindsAtDialTime(t *testing.T) {
	t.Parallel()

	var calls int
	guard := sqlnode.Guard{
		LookupIPAddr: func(_ context.Context, _ string) ([]net.IP, error) {
			calls++
			if calls == 1 {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	}
	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		calls = 0
		fields := loopbackFields("5432")
		fields["host"] = "db.partner.test"
		err := openWithTimeout(t, driver, fields, guard)
		assertForbidden(t, err, "loopback")
		if calls < 2 {
			t.Errorf("%s resolved the host %d time(s), want at least 2 (pre-flight and dial)", driver, calls)
		}
	}
}

// One endpoint can be admitted through the guard while it stays on for
// everything else — the alternative to opening the whole internal network.
func TestAllowedPrivateEndpointPermitsOneLoopback(t *testing.T) {
	t.Parallel()

	// Port 1 is reserved and refuses connections, so this exercises the dial
	// path without needing a server — and proves the exemption is what let it
	// through, because the same target without the entry is refused above.
	guard := sqlnode.Guard{
		Policy: safehttp.Policy{AllowedPrivateEndpoints: []string{"127.0.0.1:1"}},
	}
	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		err := openWithTimeout(t, driver, loopbackFields("1"), guard)
		if err == nil {
			t.Fatalf("%s connected to a closed port", driver)
		}
		if errors.Is(err, sqlnode.ErrForbiddenTarget) {
			t.Errorf("%s refused an explicitly admitted endpoint: %v", driver, err)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s connection error leaked the password: %v", driver, err)
		}
	}
}

// allow_private_networks governs database targets exactly as it governs HTTP:
// true dials a private database, false refuses it.
func TestAllowPrivateNetworksGovernsDatabaseTargets(t *testing.T) {
	t.Parallel()

	allow := sqlnode.Guard{Policy: safehttp.Policy{AllowPrivateNetworks: true}}
	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		err := openWithTimeout(t, driver, loopbackFields("1"), allow)
		if err == nil {
			t.Fatalf("%s connected to a closed port", driver)
		}
		if errors.Is(err, sqlnode.ErrForbiddenTarget) {
			t.Errorf("%s refused a private target the policy permits: %v", driver, err)
		}
		// This is the driver-echo path the default-deny tests no longer
		// reach: the connection was really attempted, so a DSN-echoing
		// failure would carry the password here.
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s connection error leaked the password: %v", driver, err)
		}
	}
}

// A non-empty process allowlist scopes database targets the way it scopes
// HTTP: any other host is refused, and a listed host still faces the address
// policy.
func TestProcessAllowedHostsScopesDatabaseTargets(t *testing.T) {
	t.Parallel()

	guard := sqlnode.Guard{Policy: safehttp.Policy{AllowedHosts: []string{"db.partner.test"}}}
	resolvesLoopback := sqlnode.Guard{
		Policy:       safehttp.Policy{AllowedHosts: []string{"db.partner.test"}},
		LookupIPAddr: func(_ context.Context, _ string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil },
	}
	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		fields := loopbackFields("5432")
		fields["host"] = "elsewhere.test"
		assertForbidden(t, openWithTimeout(t, driver, fields, guard), "not in the allowed list")

		fields["host"] = "db.partner.test"
		assertForbidden(t, openWithTimeout(t, driver, fields, resolvesLoopback), "loopback")
	}
}

// A credential scoped to one host cannot open any other, with the same
// wildcard rules the HTTP node enforces.
func TestCredentialAllowedDomainsScopesDatabaseTargets(t *testing.T) {
	t.Parallel()

	for _, driver := range []sqlnode.Driver{sqlnode.DriverPostgres, sqlnode.DriverMySQL} {
		guard := sqlnode.Guard{AllowedDomains: []string{"db.partner.test"}}
		fields := loopbackFields("5432")
		fields["host"] = "elsewhere.test"
		assertForbidden(t, openWithTimeout(t, driver, fields, guard), "allowed list")

		// The scoped host passes the scope and is then judged on its
		// address like any other target.
		scoped := sqlnode.Guard{
			AllowedDomains: []string{"*.partner.test"},
			LookupIPAddr:   func(_ context.Context, _ string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil },
		}
		fields["host"] = "api.partner.test"
		assertForbidden(t, openWithTimeout(t, driver, fields, scoped), "loopback")
	}
}

// A SQLite credential has no host, so a scope it carries would be silently
// ignored — refused instead.
func TestSQLiteRefusesAScopedCredential(t *testing.T) {
	t.Parallel()

	_, err := sqlnode.Open(context.Background(), sqlnode.DriverSQLite,
		map[string]string{"path": "/tmp/kilasflow-scoped.db"},
		sqlnode.Guard{AllowedDomains: []string{"db.partner.test"}})
	assertForbidden(t, err, "not a host")
}

// A port that does not parse is refused, not replaced with the default: the
// policy has to be told the truth about what will be dialled.
func TestAnUnparsablePortIsRefused(t *testing.T) {
	t.Parallel()

	for driver, port := range map[sqlnode.Driver]string{sqlnode.DriverPostgres: "5432x", sqlnode.DriverMySQL: "3306x"} {
		fields := loopbackFields(port)
		if err := openWithTimeout(t, driver, fields, sqlnode.Guard{}); err == nil {
			t.Errorf("%s accepted port %q", driver, port)
		} else if !strings.Contains(err.Error(), "not a port number") {
			t.Errorf("%s error = %v, want a port complaint", driver, err)
		}
	}
}
