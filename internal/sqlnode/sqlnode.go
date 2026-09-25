// Package sqlnode connects workflows to databases the user configures.
//
// These connections are deliberately built from scratch out of credential
// fields and opened through database/sql, never through the internal GORM
// handle. There is no default, inferred, or selectable connection: a workflow
// can only reach a database whose credential someone deliberately created, so
// the SQL nodes can never become a backdoor into KilasFlow's own storage.
package sqlnode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlguard"

	// Registered for its database/sql driver name only.
	_ "github.com/glebarez/go-sqlite"
)

// Driver names one supported external database.
type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverMySQL    Driver = "mysql"
	DriverSQLite   Driver = "sqlite"
)

// ErrForbiddenTarget reports a connection the policy refuses to open.
var ErrForbiddenTarget = errors.New("database target is not allowed")

// Limits bound one query.
type Limits struct {
	// Timeout bounds a single statement.
	Timeout time.Duration
	// MaxRows bounds how many rows are read into items.
	MaxRows int
}

// DefaultLimits are used when a node configures nothing.
func DefaultLimits() Limits {
	return Limits{Timeout: 30 * time.Second, MaxRows: 10_000}
}

// Ceiling bounds what a workflow document is allowed to ask for.
//
// A node's limits come from its parameters, and a parameter may be an
// expression over the incoming item — so `{{ $json.maxRows }}` behind a webhook
// lets whoever calls that webhook choose how much of the customer's database
// this server buffers into memory. Limits are what a workflow wants; the
// ceiling is what the deployment allows, and a document cannot raise it.
type Ceiling struct {
	// MaxRows bounds MaxRows. Zero means DefaultCeiling's value; there is no
	// spelling for "unbounded", because an unbounded row buffer is the defect.
	MaxRows int
	// MaxTimeout bounds Timeout, so a statement cannot hold a connection to
	// the user's database open indefinitely.
	MaxTimeout time.Duration
}

// DefaultCeiling is the bound applied when a deployment configures nothing.
//
// Both values sit well above the node defaults (10,000 rows, 30 seconds) on
// purpose: the ceiling exists to stop a document asking for something absurd,
// not to second-guess an author who knows their own data.
func DefaultCeiling() Ceiling {
	return Ceiling{MaxRows: 50_000, MaxTimeout: 5 * time.Minute}
}

// Apply clamps limits to the ceiling and reports what it clamped.
//
// It clamps rather than refusing. A workflow asking for more rows than the
// deployment allows still wants the rows it can have, and failing the run
// outright teaches the author nothing about where the boundary is. The
// returned map is the clamped-to values, so the node can say on its output
// that it did not read everything — silently returning fewer rows than were
// asked for is how a partial read gets mistaken for a complete one.
func (ceiling Ceiling) Apply(limits Limits) (Limits, map[string]any) {
	fallback := DefaultCeiling()
	if ceiling.MaxRows <= 0 {
		ceiling.MaxRows = fallback.MaxRows
	}
	if ceiling.MaxTimeout <= 0 {
		ceiling.MaxTimeout = fallback.MaxTimeout
	}

	var clamped map[string]any
	if limits.MaxRows > ceiling.MaxRows {
		limits.MaxRows = ceiling.MaxRows
		clamped = map[string]any{"maxRows": float64(ceiling.MaxRows)}
	}
	if limits.Timeout > ceiling.MaxTimeout {
		limits.Timeout = ceiling.MaxTimeout
		if clamped == nil {
			clamped = map[string]any{}
		}
		clamped["timeoutSeconds"] = ceiling.MaxTimeout.Seconds()
	}
	return limits, clamped
}

// Guard describes what a workflow database credential must never be able to reach.
type Guard struct {
	// InternalPaths are KilasFlow's own database files. A workflow that could
	// open one would be able to read every credential, workflow, and execution
	// in the installation.
	InternalPaths []string
	// Internal is the installation's own network database identity: driver,
	// host, port and database name, built once at startup from the
	// installation's own DSN (see ParseInternalTarget). A workflow credential
	// resolving to it is refused on every network driver before anything
	// dials, and again at dial time. Nil means the install holds no network
	// database of its own — a SQLite install, or an in-memory one — in which
	// case InternalPaths above is the whole of the internal-database guard.
	//
	// A table prefix does not scope this refusal. The prefix is a naming
	// convention, not a boundary: anything holding the connection can read
	// every table under it, so the whole database is refused however the
	// credential spells its target.
	Internal *InternalTarget
	// Policy is the process egress policy for network databases: the same
	// policy that governs workflow HTTP requests. A credential whose host
	// resolves to a loopback, private, link-local or otherwise internal
	// address is refused before anything dials, unless the policy explicitly
	// allows it. The zero value refuses every such address, so a Guard built
	// by hand gets the strict answer rather than an open one.
	Policy safehttp.Policy
	// AllowedDomains scopes one credential to the hosts it may be sent to. An
	// empty list means unrestricted; a non-empty list refuses any host the
	// credential is not scoped to, with the same wildcard rules the HTTP node
	// enforces. The caller fills this from the resolved credential — a SQLite
	// credential must never carry one, because a file path has no host for a
	// scope to match.
	AllowedDomains []string
	// LookupIPAddr resolves a database host to the addresses the policy is
	// checked against. Nil uses the system resolver; tests supply their own,
	// so a hostname resolving to 127.0.0.1 is refused without needing DNS or
	// a live server.
	LookupIPAddr func(ctx context.Context, host string) ([]net.IP, error)
	// SQLite confines SQLite credential files to one directory per tenant, or
	// disables them. The zero value disables them; see SQLiteFiles.
	SQLite SQLiteFiles
	// Tenant is the tenant the credential being opened belongs to, set per
	// call with ForTenant. A confined SQLite path is relative to this
	// tenant's directory, and without one there is no directory to confine to.
	Tenant string
}

// InternalTarget is the installation's own network database, normalised once
// at startup so every credential is compared against resolved identity rather
// than DSN text.
type InternalTarget struct {
	// Driver is the installation's own driver. Only a credential for the
	// same driver can resolve to this target; anything else is a different
	// server grammar and cannot name this database.
	Driver Driver
	// Host is the installation's own database host as spelled in its DSN.
	// Compared by resolved address, never by text: localhost, 127.0.0.1,
	// the Compose service name and the machine's own DNS name all reach the
	// same server, and a text check is the version of this guard that gets
	// bypassed by someone who is not even trying.
	Host string
	// Port is the installation's own database port, normalised to digits.
	Port string
	// Database is the installation's own database name. Compared exactly:
	// a neighbour database on the same server is a different target and
	// passes this guard (the egress policy still applies to it).
	Database string
}

// ParseInternalTarget normalises the installation's own database DSN into the
// identity workflow credentials are refused against. The DSN is the
// PostgreSQL URL the installation itself opens — userinfo, host, port and
// path — and none of its options matter to the comparison: sslmode changes
// how the connection is wrapped, not which database it reaches, so
// postgres://…/kilasflow and postgres://…/kilasflow?sslmode=disable are the
// same target and both are refused.
//
// SQLite installs have no network identity; their guard is the file list in
// Guard.InternalPaths, resolved through database.SQLitePath by the caller.
// An error means the DSN is a spelling this function does not understand,
// and the caller must treat that as fatal rather than as an empty guard: a
// guard that cannot resolve its own identity protects nothing, and the
// failure is invisible.
func ParseInternalTarget(driver Driver, dsn string) (*InternalTarget, error) {
	if driver != DriverPostgres {
		return nil, fmt.Errorf("the internal database guard understands the postgres DSN, not driver %q", driver)
	}
	raw := strings.TrimSpace(dsn)
	if raw == "" {
		return nil, fmt.Errorf("the internal database DSN is empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("the internal database DSN could not be parsed: %w", err)
	}
	switch parsed.Scheme {
	case "postgres", "postgresql":
	default:
		return nil, fmt.Errorf("the internal database DSN has scheme %q, want postgres", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("the internal database DSN names no host")
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("the internal database DSN names port %q, which is not a port number", parsed.Port())
	}
	return &InternalTarget{
		Driver:   driver,
		Host:     host,
		Port:     canonicalPort(port),
		Database: strings.TrimSpace(strings.TrimPrefix(parsed.Path, "/")),
	}, nil
}

// canonicalPort normalises a port to the digits splitHostPort produces, so
// "5432" and "05432" compare equal. A value that is not a number is returned
// as-is and never matches a normalised one.
func canonicalPort(port string) string {
	number, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || number < 1 || number > 65535 {
		return port
	}
	return strconv.Itoa(number)
}

// sameServerName reports whether two host spellings name the same host
// without consulting DNS: case-insensitive, a trailing dot ignored. This is
// the backstop for a name whose answers disagree between lookups — DNS
// round-robin can hand the credential one address and the installation
// another for the very same name — where an address comparison alone misses.
func sameServerName(left, right string) bool {
	normalise := func(host string) string {
		return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	}
	return normalise(left) == normalise(right)
}

// checkInternalDatabase refuses a credential that resolves to the
// installation's own database. The caller supplies the credential's
// normalised host, port and database name plus the addresses the host
// resolved to; the installation's own host is resolved through the same
// resolver, so two spellings of one server still compare equal. The message
// names the reason, never the DSN that carries the password.
func checkInternalDatabase(ctx context.Context, driver Driver, host, port, database string, addresses []net.IP, guard Guard) error {
	target := guard.Internal
	if target == nil || target.Driver != driver {
		return nil
	}
	if strings.TrimSpace(database) != target.Database {
		return nil
	}
	if canonicalPort(port) != canonicalPort(target.Port) {
		return nil
	}
	// The same spelling is the same server whatever DNS says this second.
	if sameServerName(host, target.Host) {
		return fmt.Errorf("%w: that %s database is KilasFlow's own internal database", ErrForbiddenTarget, driver)
	}
	internal, err := lookupIPs(ctx, guard, target.Host)
	if err != nil {
		// The installation's own host does not resolve right now. The
		// spellings already disagreed, so there is nothing proven to refuse
		// on — the egress policy above still applies to the credential.
		return nil
	}
	for _, candidate := range addresses {
		for _, own := range internal {
			if candidate.Equal(own) {
				return fmt.Errorf("%w: that %s database is KilasFlow's own internal database", ErrForbiddenTarget, driver)
			}
		}
	}
	return nil
}

// Result is one executed statement's outcome.
type Result struct {
	// Rows are the returned rows as item JSON, for a query.
	Rows []map[string]any
	// RowsAffected is set for a statement that returns no rows.
	RowsAffected int64
	// LastInsertID is the key the database generated for this statement, or
	// zero when it generated none.
	//
	// Read from the driver's own answer for *that statement*, never from
	// SELECT LAST_INSERT_ID(): that function is connection-scoped and keeps its
	// previous value when a statement generates no key, so a batch inserting
	// into a table without an auto-increment column would report the previous
	// item's id. Plausible, wrong, and silent.
	//
	// PostgreSQL's driver reports none — it has RETURNING instead — so this is
	// zero there and the row itself carries the key.
	LastInsertID int64
	// Truncated reports that MaxRows stopped the read.
	Truncated bool
	// ColumnTypes is each returned column's database type name, keyed by the
	// same name the row map uses.
	//
	// Carried because the scanned value alone cannot answer what the column
	// was: a PostgreSQL numeric and a text column both arrive as a Go string,
	// and only one of them is a number whose digits a caller may want to keep.
	// Empty where the driver does not report a type name.
	ColumnTypes map[string]string
}

// Connection is an open external database handle.
type Connection struct {
	db     *sql.DB
	driver Driver
	// dialect is the statement guard's grammar for this server, with the
	// backslash question already asked rather than guessed at.
	dialect sqlguard.Dialect
}

// Close releases the connection. Every Open must be paired with one, which is
// why Open returns a closer rather than caching handles: a workflow's database
// credential can change or be revoked between runs, so a cached pool would
// keep using access that was withdrawn.
func (connection *Connection) Close() error {
	if connection == nil || connection.db == nil {
		return nil
	}
	return connection.db.Close()
}

// Open builds a connection from credential fields.
func Open(ctx context.Context, driver Driver, fields map[string]string, guard Guard) (*Connection, error) {
	if driver == DriverSQLite {
		// Opened and pinged together under a deadline the driver cannot
		// ignore: see openSQLite.
		path, err := sqlitePath(fields, guard)
		if err != nil {
			return nil, err
		}
		db, err := openSQLite(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("connect to %s database: %w", driver, err)
		}
		return &Connection{db: db, driver: driver, dialect: guardDialect(driver)}, nil
	}
	db, err := openDatabase(ctx, driver, fields, guard)
	if err != nil {
		return nil, err
	}
	// One connection per node run keeps transaction scope obvious and avoids a
	// pool that outlives the credential it was built from.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to %s database: %w", driver, err)
	}
	connection := &Connection{db: db, driver: driver, dialect: guardDialect(driver)}
	connection.dialect = resolveBackslashRule(ctx, db, driver, connection.dialect)
	return connection, nil
}

// openDatabase turns network credential fields into a live handle. They go
// through the guard before anything dials, and then dial through a function
// that re-checks every resolved address: sql.Open is lazy and the pool
// reconnects on its own, so a check that runs once before the dial is not the
// control, only the clearer message. SQLite is opened by Open itself.
func openDatabase(ctx context.Context, driver Driver, fields map[string]string, guard Guard) (*sql.DB, error) {
	switch driver {
	case DriverPostgres:
		return openPostgres(ctx, fields, guard)
	case DriverMySQL:
		return openMySQL(ctx, fields, guard)
	default:
		return nil, fmt.Errorf("database driver %q is not supported", driver)
	}
}

// openPostgres checks the credential's host against the guard and opens
// through a dial function that enforces the same policy per connection.
func openPostgres(ctx context.Context, fields map[string]string, guard Guard) (*sql.DB, error) {
	host, port, err := checkTarget(ctx, DriverPostgres, fields, guard, "5432")
	if err != nil {
		return nil, err
	}
	database := strings.TrimSpace(fields["database"])
	cfg, err := pgx.ParseConfig(postgresDSN(fields, host, port))
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}
	// Through the guard's resolver rather than the system one: pgconn
	// resolves the host itself before it dials, so leaving LookupFunc at its
	// default would check one answer and dial another — and a test resolver
	// would never be consulted at all.
	cfg.LookupFunc = func(lookupCtx context.Context, _ string) ([]string, error) {
		ips, err := lookupIPs(lookupCtx, guard, host)
		if err != nil {
			return nil, err
		}
		addrs := make([]string, 0, len(ips))
		for _, ip := range ips {
			addrs = append(addrs, net.JoinHostPort(ip.String(), port))
		}
		return addrs, nil
	}
	cfg.DialFunc = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		return guardedDialAddr(dialCtx, network, addr, host, guard, DriverPostgres, database)
	}
	return stdlib.OpenDB(*cfg), nil
}

// openMySQL checks the credential's host against the guard and opens through
// a per-connection dial function. Per-connection rather than a registered
// network name on purpose: the DSN keeps its tcp(host:port) shape, which is
// what Sanitize looks for when it redacts the password from a driver error.
func openMySQL(ctx context.Context, fields map[string]string, guard Guard) (*sql.DB, error) {
	host, port, err := checkTarget(ctx, DriverMySQL, fields, guard, "3306")
	if err != nil {
		return nil, err
	}
	database := strings.TrimSpace(fields["database"])
	dsn, err := mysqlDSN(fields, host, port)
	if err != nil {
		return nil, err
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql connection: %w", err)
	}
	cfg.DialFunc = func(dialCtx context.Context, network, _ string) (net.Conn, error) {
		return guardedDial(dialCtx, network, host, port, guard, DriverMySQL, database)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("open mysql connection: %w", err)
	}
	return sql.OpenDB(connector), nil
}

// checkTarget refuses a network credential before it dials: first where the
// credential may be sent, then where the process may reach at all, then the
// addresses the host actually resolves to, and finally whether those resolve
// to the installation's own database. The message names the host and the
// reason, never the DSN that carries the password.
func checkTarget(ctx context.Context, driver Driver, fields map[string]string, guard Guard, fallbackPort string) (string, string, error) {
	host, port, err := splitHostPort(fields, fallbackPort)
	if err != nil {
		return "", "", err
	}
	if len(guard.AllowedDomains) > 0 {
		record := credentials.Record{AllowedDomains: guard.AllowedDomains}
		if !record.AllowsHost(host) {
			return "", "", fmt.Errorf("%w: %s host %q is not in the credential's allowed list", ErrForbiddenTarget, driver, host)
		}
	}
	if len(guard.Policy.AllowedHosts) > 0 {
		probe := &url.URL{Scheme: "https", Host: net.JoinHostPort(host, port)}
		if err := guard.Policy.CheckURL(probe); err != nil {
			return "", "", fmt.Errorf("%w: %s host %q is not allowed: %v", ErrForbiddenTarget, driver, host, err)
		}
	}
	addresses, err := lookupIPs(ctx, guard, host)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s host %q: %w", driver, host, err)
	}
	for _, address := range addresses {
		if err := guard.Policy.CheckEndpointAddress(host, port, address); err != nil {
			return "", "", fmt.Errorf("%w: %s host %q resolved to %s: %v", ErrForbiddenTarget, driver, host, address, err)
		}
	}
	if err := checkInternalDatabase(ctx, driver, host, port, fields["database"], addresses, guard); err != nil {
		return "", "", err
	}
	return host, port, nil
}

// guardedDial is the dial-time control checkTarget's pre-flight is not: it
// resolves the host again at the moment of dialling, refuses every address
// the policy forbids, and dials a checked address rather than the hostname,
// so a name that changes its answer between the two checks cannot slip a
// forbidden address through. The installation's own database is refused here
// too, for the same reason: a name that resolved elsewhere when checked can
// resolve at the installation when dialled.
func guardedDial(ctx context.Context, network, host, port string, guard Guard, driver Driver, database string) (net.Conn, error) {
	addresses, err := lookupIPs(ctx, guard, host)
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if err := guard.Policy.CheckEndpointAddress(host, port, address); err != nil {
			return nil, fmt.Errorf("%w: database host %q resolved to %s: %v", ErrForbiddenTarget, host, address, err)
		}
	}
	if err := checkInternalDatabase(ctx, driver, host, port, database, addresses, guard); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var firstErr error
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if err == nil {
			return connection, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("database host %q resolved to no addresses", host)
	}
	return nil, firstErr
}

// guardedDialAddr checks the exact address about to be dialled. pgconn hands
// the dial function the address its own lookup produced, so this verifies
// that address rather than resolving the hostname a second time: the check
// and the socket cannot disagree. A non-literal address means something
// bypassed the lookup above, and falls back to resolving and dialling a
// checked address instead of trusting it.
func guardedDialAddr(ctx context.Context, network, addr, host string, guard Guard, driver Driver, database string) (net.Conn, error) {
	dialHost, dialPort, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if address := net.ParseIP(dialHost); address != nil {
		if err := guard.Policy.CheckEndpointAddress(host, dialPort, address); err != nil {
			return nil, fmt.Errorf("%w: database host %q resolved to %s: %v", ErrForbiddenTarget, host, address, err)
		}
		if err := checkInternalDatabase(ctx, driver, host, dialPort, database, []net.IP{address}, guard); err != nil {
			return nil, err
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		return dialer.DialContext(ctx, network, addr)
	}
	return guardedDial(ctx, network, dialHost, dialPort, guard, driver, database)
}

// lookupIPs resolves a database host through the guard's resolver, or the
// system one when the guard names none.
func lookupIPs(ctx context.Context, guard Guard, host string) ([]net.IP, error) {
	if guard.LookupIPAddr != nil {
		return guard.LookupIPAddr(ctx, host)
	}
	records, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	addresses := make([]net.IP, 0, len(records))
	for _, record := range records {
		addresses = append(addresses, record.IP)
	}
	return addresses, nil
}

// guardDialect is the lexical grammar of the server a driver speaks.
//
// Chosen from the driver rather than passed in, so a caller cannot reach the
// database through a grammar more permissive than the server it is actually
// talking to.
func guardDialect(driver Driver) sqlguard.Dialect {
	switch driver {
	case DriverPostgres:
		return sqlguard.Postgres
	case DriverMySQL:
		return sqlguard.MySQL
	default:
		return sqlguard.SQLite
	}
}

// resolveBackslashRule asks the server whether a backslash escapes inside a
// string literal.
//
// Asked once, at connect, because the answer is a server setting and the guard
// otherwise has to hold every statement to both readings — which refuses
// `SELECT 'O\'Brien'`, valid under the default configuration of every MySQL
// and MariaDB, along with every other escaped apostrophe somebody types.
//
// A server that will not answer keeps the ambiguity rather than gaining a
// guess: the returned dialect is unchanged, both readings still apply, and the
// guard stays strict. Failing closed here costs a refused apostrophe; failing
// open would cost the whole two-reading defence.
func resolveBackslashRule(ctx context.Context, db *sql.DB, driver Driver, dialect sqlguard.Dialect) sqlguard.Dialect {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	switch driver {
	case DriverMySQL:
		var mode string
		if err := db.QueryRowContext(probeCtx, "SELECT @@sql_mode").Scan(&mode); err != nil {
			return dialect
		}
		// NO_BACKSLASH_ESCAPES makes a backslash an ordinary character.
		return dialect.WithKnownBackslashEscapes(!strings.Contains(strings.ToUpper(mode), "NO_BACKSLASH_ESCAPES"))
	case DriverPostgres:
		var conforming string
		if err := db.QueryRowContext(probeCtx, "SHOW standard_conforming_strings").Scan(&conforming); err != nil {
			return dialect
		}
		// Standard-conforming strings mean a backslash is literal, which has
		// been the default since 9.1; the escaping reading is the legacy one.
		return dialect.WithKnownBackslashEscapes(!strings.EqualFold(strings.TrimSpace(conforming), "on"))
	}
	return dialect
}

// postgresDSN assembles the driver's URL from credential fields and an
// already-checked host and port, so the dialler and the DSN can never
// disagree about the address.
func postgresDSN(fields map[string]string, host, port string) string {
	values := url.Values{}
	sslMode := strings.TrimSpace(fields["sslMode"])
	if sslMode == "" {
		// Defaulting to require rather than disable: a workflow database is
		// usually remote, and silently sending credentials in the clear is not
		// a default anyone would choose deliberately.
		sslMode = "require"
	}
	values.Set("sslmode", sslMode)

	target := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(fields["user"], fields["password"]),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + strings.TrimPrefix(strings.TrimSpace(fields["database"]), "/"),
		RawQuery: values.Encode(),
	}
	return target.String()
}

// mysqlDSN builds the driver's connection string from credential fields and
// an already-checked host and port.
//
// The database name is checked rather than interpolated, and this is not
// cosmetic. go-sql-driver splits its DSN at the first `?` after the last `/`,
// so a database named `app?multiStatements=true&` put that option into the
// connection — verified against the pinned driver — and multi-statement is
// exactly what the statement guard exists to prevent. The same trick reaches
// `sql_mode`, which decides whether a backslash escapes inside a string
// literal, and therefore what a statement even means.
//
// Refused rather than escaped: the driver's DSN grammar has no escape for
// these bytes, so there is nothing to escape them to. A real database name has
// none of them.
func mysqlDSN(fields map[string]string, host, port string) (string, error) {
	database := strings.TrimSpace(fields["database"])
	if strings.ContainsAny(database, "?&/@:") {
		return "", fmt.Errorf("%w: a MySQL database name must not contain any of ? & / @ :", ErrForbiddenTarget)
	}
	user := fields["user"]
	if strings.ContainsAny(user, "@/:") {
		// The same split, one field earlier: the driver takes the last `@`
		// before the address, so a user carrying one moves the host.
		return "", fmt.Errorf("%w: a MySQL user name must not contain any of @ / :", ErrForbiddenTarget)
	}
	if strings.ContainsAny(host, "()?&/@") {
		// The address sits inside tcp( … ), so a closing parenthesis ends it
		// early and everything after is read as the driver's own grammar.
		return "", fmt.Errorf("%w: a MySQL host must not contain any of ( ) ? & / @", ErrForbiddenTarget)
	}
	parameters := "?parseTime=true"
	if tls := strings.TrimSpace(fields["tls"]); tls != "" {
		parameters += "&tls=" + url.QueryEscape(tls)
	}
	return fmt.Sprintf("%s:%s@tcp(%s)/%s%s",
		user, fields["password"], net.JoinHostPort(host, port),
		database, parameters), nil
}

// splitHostPort reads the credential's host and port as the separate values
// the policy is checked against. A port that does not parse is refused rather
// than replaced with the default: silently connecting to 5432 when the
// credential said 5432x would tell the policy one thing and dial another.
func splitHostPort(fields map[string]string, fallbackPort string) (string, string, error) {
	host := strings.TrimSpace(fields["host"])
	if host == "" {
		host = "localhost"
	}
	raw := strings.TrimSpace(fields["port"])
	if raw == "" {
		raw = fallbackPort
	}
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 || number > 65535 {
		return "", "", fmt.Errorf("database port %q is not a port number", strings.TrimSpace(fields["port"]))
	}
	return host, strconv.Itoa(number), nil
}

// sqlitePath resolves and guards a SQLite credential's file.
//
// A SQLite credential names a file on the server's own disk, so it is the one
// driver where a mistake reaches KilasFlow's own data or another tenant's.
// The path must be given explicitly, is confined to the tenant's directory
// under the SQLite root (see SQLiteFiles), must name a regular file or one
// not yet created, and must not be an internal database file.
func sqlitePath(fields map[string]string, guard Guard) (string, error) {
	if len(guard.AllowedDomains) > 0 {
		// A file path has no host for a scope to match, so a scope carried
		// this far would be silently ignored — the defect domain scoping
		// exists to remove. Refused here; save-time rejection belongs to the
		// credential endpoint that accepts the scope.
		return "", fmt.Errorf("%w: a SQLite credential names a file, not a host, so an allowed-domains scope cannot apply to it", ErrForbiddenTarget)
	}
	if !guard.SQLite.Enabled() {
		return "", fmt.Errorf("%w: SQLite credentials are disabled on this server (sql.sqlite_root is empty)", ErrForbiddenTarget)
	}
	raw := strings.TrimSpace(fields["path"])
	if raw == "" {
		return "", fmt.Errorf("%w: a SQLite credential must name an explicit file path", ErrForbiddenTarget)
	}
	// A URI form could carry ?mode= or an attached database, so only plain
	// paths are accepted.
	if strings.Contains(raw, "?") || strings.HasPrefix(raw, "file:") {
		return "", fmt.Errorf("%w: SQLite path must be a plain file path", ErrForbiddenTarget)
	}
	if strings.EqualFold(raw, ":memory:") {
		return "", fmt.Errorf("%w: an in-memory SQLite database is not a durable target", ErrForbiddenTarget)
	}

	var resolved string
	if guard.SQLite.Unconfined {
		absolute, err := filepath.Abs(raw)
		if err != nil {
			return "", fmt.Errorf("%w: SQLite path could not be resolved", ErrForbiddenTarget)
		}
		resolved = canonical(absolute)
		if err := requireRegularFile(resolved); err != nil {
			return "", err
		}
	} else {
		confined, err := confinedSQLitePath(raw, guard)
		if err != nil {
			return "", err
		}
		resolved = confined
	}

	// Checked in both modes. Confinement keeps a tenant inside its own
	// directory, but an operator can still set the root so that directory
	// holds KilasFlow's own file.
	for _, internal := range guard.InternalPaths {
		if internal == "" {
			continue
		}
		candidate, err := filepath.Abs(internal)
		if err != nil {
			continue
		}
		candidate = canonical(candidate)
		if sameFile(resolved, candidate) {
			return "", fmt.Errorf("%w: that path is KilasFlow's own database", ErrForbiddenTarget)
		}
		// SQLite writes -wal and -shm siblings; naming one of those reaches the
		// same database.
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if sameFile(resolved, candidate+suffix) {
				return "", fmt.Errorf("%w: that path is part of KilasFlow's own database", ErrForbiddenTarget)
			}
		}
	}
	return resolved, nil
}

// canonical resolves a path to a single comparable form.
//
// Symlinks are resolved through the *directory* rather than the file, so a
// target that does not exist yet — a WAL sidecar, a database about to be
// created — still normalizes the same way as one that does. Resolving only the
// existing file would leave `/var/...` and `/private/var/...` looking like
// different paths on macOS, which is exactly the gap a guard must not have.
func canonical(path string) string {
	path = filepath.Clean(path)
	directory, base := filepath.Split(path)
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(directory)); err == nil {
		path = filepath.Join(resolved, base)
	}
	// A path that is itself a symlink to the guarded file must resolve to it.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// sameFile compares by inode when both exist, and by canonical path otherwise,
// so a hard link or a relative spelling cannot slip past.
func sameFile(left, right string) bool {
	if left == right {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

// Query runs one statement with bound parameters.
//
// Parameters are always bound, never interpolated: a workflow's SQL comes from
// a document a tenant may author, so string-building the statement would make
// every workflow an injection vector into the user's own database.
func (connection *Connection) Query(ctx context.Context, statement string, parameters []any, limits Limits) (Result, error) {
	if connection == nil || connection.db == nil {
		return Result{}, fmt.Errorf("database connection is not open")
	}
	if strings.TrimSpace(statement) == "" {
		return Result{}, fmt.Errorf("statement is required")
	}
	// Checked here rather than only in the node, because this is the last
	// place before the driver and every path — query, execute, batch,
	// transaction — has to be closed for any of them to mean anything. A
	// second statement smuggled past a node-level check would still run.
	if err := sqlguard.Check(connection.dialect, statement); err != nil {
		return Result{}, err
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}
	if limits.MaxRows <= 0 {
		limits.MaxRows = DefaultLimits().MaxRows
	}

	queryCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	rows, err := connection.db.QueryContext(queryCtx, statement, parameters...)
	if err != nil {
		return Result{}, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows, limits.MaxRows)
}

// scanRows reads a result set into item JSON, stopping at maxRows.
//
// Shared by Query and by a transaction statement declared as returning, so a
// row coming back out of a transaction is normalized exactly like one coming
// back out of a query — a driver value that became a string on one path and
// base64 on the other would be a difference nobody could explain.
func scanRows(rows *sql.Rows, maxRows int) (Result, error) {
	columns, err := rows.Columns()
	if err != nil {
		return Result{}, fmt.Errorf("read columns: %w", err)
	}

	result := Result{Rows: make([]map[string]any, 0, 16), ColumnTypes: columnTypeNames(rows, columns)}
	for rows.Next() {
		if len(result.Rows) >= maxRows {
			result.Truncated = true
			break
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return Result{}, fmt.Errorf("scan row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for index, column := range columns {
			row[column] = normalize(values[index])
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("read rows: %w", err)
	}
	return result, nil
}

// columnTypeNames reads each column's database type name, where there is one.
//
// A driver may report nothing at all, and database/sql documents the name as
// best-effort — so an absent entry means "unknown", never "not a number", and
// every caller has to have an answer for the unknown case.
func columnTypeNames(rows *sql.Rows, columns []string) map[string]string {
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(columns) {
		return nil
	}
	names := make(map[string]string, len(columns))
	for index, column := range columns {
		if name := types[index].DatabaseTypeName(); name != "" {
			names[column] = strings.ToUpper(name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

// Execute runs a statement that returns no rows.
//
// It is a separate operation from Query so a node's configured intent is
// explicit: "execute" reports rows affected, "query" returns items.
func (connection *Connection) Execute(ctx context.Context, statement string, parameters []any, limits Limits) (Result, error) {
	if connection == nil || connection.db == nil {
		return Result{}, fmt.Errorf("database connection is not open")
	}
	if strings.TrimSpace(statement) == "" {
		return Result{}, fmt.Errorf("statement is required")
	}
	if err := sqlguard.Check(connection.dialect, statement); err != nil {
		return Result{}, err
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}
	executeCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	outcome, err := connection.db.ExecContext(executeCtx, statement, parameters...)
	if err != nil {
		return Result{}, fmt.Errorf("statement failed: %w", err)
	}
	affected, err := outcome.RowsAffected()
	if err != nil {
		// Some drivers cannot report this. Zero with no error is honest; the
		// statement still ran.
		affected = 0
	}
	return Result{RowsAffected: affected, LastInsertID: lastInsertID(outcome), Rows: []map[string]any{}}, nil
}

// ExecuteBatch runs one statement once per bound parameter set, atomically.
//
// A node used to run a statement per input item on its own, so a hundred-row
// insert was a hundred parses and a hundred round trips — and, because Open
// pins the pool to one connection, a hundred serial ones. Here the statement
// is prepared once and every set goes through the prepared handle.
//
// The whole batch is one transaction. A bulk write that fails halfway used to
// leave the first half applied and report only the error, which is the worst
// of the three possible outcomes: nothing tells the author how far it got, and
// re-running duplicates whatever did land.
//
// The timeout bounds each statement rather than the batch, which is what the
// node's "statement timeout" says it does. The batch as a whole is bounded by
// ctx — the node's own timeout setting and the execution's.
//
// Statements are prepared per distinct SQL text and reused while it stays the
// same, which is one prepare for the ordinary case where every item runs the
// same statement with different parameters. The text can differ per item only
// because it may be built from an expression; when it does, the batch is still
// one transaction.
func (connection *Connection) ExecuteBatch(ctx context.Context, statements []Statement, limits Limits) ([]Result, error) {
	if connection == nil || connection.db == nil {
		return nil, fmt.Errorf("database connection is not open")
	}
	if len(statements) == 0 {
		return nil, nil
	}
	for index, statement := range statements {
		if strings.TrimSpace(statement.SQL) == "" {
			return nil, fmt.Errorf("item %d: statement is required", index+1)
		}
		// A prepared handle is not a single-statement guard. Verified against
		// the pinned SQLite driver: PrepareContext on a two-statement string
		// succeeds, executing it runs both, and an ATTACH done that way stays
		// on the connection afterwards. So this path needs the same check as
		// the unprepared one.
		if err := sqlguard.Check(connection.dialect, statement.SQL); err != nil {
			return nil, fmt.Errorf("item %d: %w", index+1, err)
		}
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}

	tx, err := connection.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}

	var (
		prepared    *sql.Stmt
		preparedSQL string
	)
	// Named so both the failure paths and the success path release it.
	closePrepared := func() {
		if prepared != nil {
			_ = prepared.Close()
			prepared = nil
		}
	}

	results := make([]Result, 0, len(statements))
	for index, statement := range statements {
		if prepared == nil || statement.SQL != preparedSQL {
			closePrepared()
			stmt, err := tx.PrepareContext(ctx, statement.SQL)
			if err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("prepare statement for item %d and the batch was rolled back: %w", index+1, err)
			}
			prepared, preparedSQL = stmt, statement.SQL
		}

		outcome, err := func() (sql.Result, error) {
			statementCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
			defer cancel()
			return prepared.ExecContext(statementCtx, statement.Parameters...)
		}()
		if err != nil {
			closePrepared()
			_ = tx.Rollback()
			// The item number is the point of the message: "statement failed"
			// over five hundred items says nothing about which row was wrong.
			return nil, fmt.Errorf("item %d failed and the batch was rolled back: %w", index+1, err)
		}
		affected, _ := outcome.RowsAffected()
		results = append(results, Result{
			RowsAffected: affected, LastInsertID: lastInsertID(outcome), Rows: []map[string]any{},
		})
	}

	closePrepared()
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}
	return results, nil
}

// Transaction runs several statements atomically.
//
// The transaction is scoped to one node execution: it commits when every
// statement succeeds and rolls back on the first failure, so a partially
// applied batch is never left behind.
func (connection *Connection) Transaction(ctx context.Context, statements []Statement, limits Limits) ([]Result, error) {
	if connection == nil || connection.db == nil {
		return nil, fmt.Errorf("database connection is not open")
	}
	if limits.Timeout <= 0 {
		limits.Timeout = DefaultLimits().Timeout
	}
	// Checked before BEGIN rather than per statement inside it, so a refusal
	// does not leave a transaction open that has to be rolled back to say no.
	for index, statement := range statements {
		if err := sqlguard.Check(connection.dialect, statement.SQL); err != nil {
			return nil, fmt.Errorf("statement %d: %w", index+1, err)
		}
	}

	txCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	tx, err := connection.db.BeginTx(txCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	if limits.MaxRows <= 0 {
		limits.MaxRows = DefaultLimits().MaxRows
	}

	results := make([]Result, 0, len(statements))
	for index, statement := range statements {
		// Only a statement that declared itself returning goes through Query.
		// Routing every statement that way looks correct and is not: a
		// non-returning statement run through Query comes back as a result set
		// with no columns and no reachable RowsAffected, so every transaction
		// in the installation would quietly start reporting zero rows affected
		// while still saying it committed. Nothing errors, and the first report
		// is somebody reconciling counts weeks later.
		if statement.Returning {
			result, err := returningStatement(txCtx, tx, statement, limits.MaxRows)
			if err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("statement %d failed and the transaction was rolled back: %w", index+1, err)
			}
			results = append(results, result)
			continue
		}
		outcome, err := tx.ExecContext(txCtx, statement.SQL, statement.Parameters...)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("statement failed and the transaction was rolled back: %w", err)
		}
		affected, _ := outcome.RowsAffected()
		results = append(results, Result{
			RowsAffected: affected, LastInsertID: lastInsertID(outcome), Rows: []map[string]any{},
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return results, nil
}

// Statement is one SQL statement with its bound parameters.
type Statement struct {
	SQL        string
	Parameters []any
	// Returning marks a statement whose rows the caller wants back, so an
	// `INSERT … RETURNING id` inside a transaction hands the generated id on
	// instead of discarding it.
	//
	// It is declared, never sniffed from the SQL text. Looking for a leading
	// SELECT or a trailing RETURNING is defeated by a comment, a CTE, or a
	// `WITH … RETURNING`, and the right answer differs per driver — so the
	// heuristic would be wrong on exactly the statements worth writing.
	Returning bool
}

// returningStatement runs one declared-returning statement inside a transaction
// and reads its rows.
//
// RowsAffected is set from the row count rather than left at zero: a driver
// does not report it for a statement run through Query, and the summary a node
// builds from these results has to keep adding up. It therefore counts the rows
// that were read, which is the same thing unless MaxRows stopped the read — and
// that case sets Truncated, so the partial count is never presented as a whole
// one.
func returningStatement(ctx context.Context, tx *sql.Tx, statement Statement, maxRows int) (Result, error) {
	rows, err := tx.QueryContext(ctx, statement.SQL, statement.Parameters...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()

	result, err := scanRows(rows, maxRows)
	if err != nil {
		return Result{}, err
	}
	result.RowsAffected = int64(len(result.Rows))
	return result, nil
}

// lastInsertID reads a generated key, tolerating a driver that has none.
//
// pgx's stdlib driver returns "not supported by this driver" rather than a
// value, and that is not a failure of the statement — so the error is dropped
// and zero stands for "the driver reported none".
func lastInsertID(outcome sql.Result) int64 {
	if outcome == nil {
		return 0
	}
	id, err := outcome.LastInsertId()
	if err != nil {
		return 0
	}
	return id
}

// normalize converts driver values into JSON-safe item data.
func normalize(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		// Drivers return text columns as bytes; JSON would otherwise encode
		// them as base64 and make a plain string unrecognizable.
		return string(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return typed
	}
}

// Sanitize strips credential material a driver may have embedded in its error.
//
// PostgreSQL and MySQL both echo the DSN on a connection failure, and that DSN
// carries the password the credential store just decrypted. It lives here
// rather than in the node package because the credential test endpoint reports
// the same driver errors to a browser: a second copy would guarantee the next
// tuning landed in one of them only.
func Sanitize(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, scheme := range []string{"postgres://", "postgresql://", "mysql://"} {
		message = redactURLCredentials(message, scheme)
	}
	// MySQL DSNs are user:password@tcp(...), which carries no scheme.
	if at := strings.Index(message, "@tcp("); at >= 0 {
		if start := strings.LastIndexAny(message[:at], " \t\"'"); start >= 0 {
			message = message[:start+1] + "[redacted]" + message[at:]
		} else {
			message = "[redacted]" + message[at:]
		}
	}
	return fmt.Errorf("%s", message)
}

// redactURLCredentials replaces the userinfo of every URL of one scheme.
//
// It walks forward, never rescanning what it has already redacted. Rewriting
// the message in place and looping from the start does not terminate: the
// replacement contains no space and is followed by the same `@`, so the next
// pass matches it again and produces the identical string forever. That was a
// latent hang for as long as this lived in the node package — no driver there
// ever echoed a URL-form DSN — and it stops being latent the moment an API
// endpoint reports a driver's message to a browser.
func redactURLCredentials(message, scheme string) string {
	var redacted strings.Builder
	for {
		start := strings.Index(message, scheme)
		if start < 0 {
			redacted.WriteString(message)
			return redacted.String()
		}
		rest := message[start+len(scheme):]
		at := strings.Index(rest, "@")
		// No `@`, or whitespace before it: this is the scheme appearing in
		// prose rather than a DSN, and eating the rest of the sentence would
		// destroy the diagnosis it belongs to.
		if at < 0 || strings.IndexAny(rest[:at], " \t") >= 0 {
			redacted.WriteString(message)
			return redacted.String()
		}
		redacted.WriteString(message[:start+len(scheme)])
		redacted.WriteString("[redacted]")
		message = rest[at:]
	}
}

// credentialDrivers maps a credential type to the driver it opens.
//
// It lives here rather than in the node package so the API can test a database
// credential without importing nodes, which would drag the AI runtime, the code
// compiler and the HTTP policy in behind it.
var credentialDrivers = map[string]Driver{
	"postgres": DriverPostgres,
	"mysql":    DriverMySQL,
	"sqlite":   DriverSQLite,
}

// DriverForCredential reports which driver a credential type opens, if any.
//
// A type with no driver is not a database credential, which is a different
// answer from one whose connection failed.
func DriverForCredential(credentialType string) (Driver, bool) {
	driver, found := credentialDrivers[strings.TrimSpace(credentialType)]
	return driver, found
}

// Test opens a connection from credential fields and closes it again.
//
// This is the whole of a connection test: Open builds the DSN, applies the
// SQLite guard and pings, so a credential that gets this far is one a workflow
// could actually use. Nothing is read and no statement is run — a probe that
// could run SQL would be a query endpoint wearing a different name.
func Test(ctx context.Context, driver Driver, fields map[string]string, guard Guard) error {
	connection, err := Open(ctx, driver, fields, guard)
	if err != nil {
		return Sanitize(err)
	}
	return Sanitize(connection.Close())
}
