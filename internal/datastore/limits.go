package datastore

import (
	"context"
	"fmt"
	"sort"
)

// Limits bounds how much one tenant's datastores may hold. A datastore is a
// real table created by runtime DDL inside the operator's own database, so
// an unbounded one hands a host application's end users the ability to grow
// unbounded tables inside a production database. Every bound refuses the
// write and evicts nothing: a datastore holding reference data must never
// silently shed rows to make room for new ones.
//
// There is no automatic row expiry in this slice. What reopens it is a host
// running a bounded-size log through a datastore; the answer then is an
// opt-in FIFO cap declared per datastore at creation, not a global policy.
type Limits struct {
	// MaxDatastoresPerTenant caps the catalogued datastores one tenant owns.
	MaxDatastoresPerTenant int
	// MaxColumnsPerDatastore caps the user columns of one datastore.
	MaxColumnsPerDatastore int
	// MaxRowsPerDatastore caps the rows of one physical table.
	MaxRowsPerDatastore int
	// MaxValueBytes caps one unbounded value: a string or raw bytes. Numbers,
	// booleans and dates bind fixed-width by construction and are exempt.
	MaxValueBytes int
}

// DefaultLimits is what an Engine enforces until SetLimits says otherwise.
// The numbers are starting points, not measurements: a hundred tables a
// tenant cannot surprise anyone, a hundred columns keeps DDL sane, a hundred
// thousand rows keeps the single SQLite connection moving, and a megabyte
// matches the webhook body's idea of one large value.
func DefaultLimits() Limits {
	return Limits{
		MaxDatastoresPerTenant: 100,
		MaxColumnsPerDatastore: 100,
		MaxRowsPerDatastore:    100_000,
		MaxValueBytes:          1 << 20,
	}
}

// SetLimits replaces the Engine's bounds. Every bound must be positive: zero
// or negative would refuse every write, which is an outage shaped like a
// configuration, so it is an error here rather than a quiet refusal later.
func (e *Engine) SetLimits(limits Limits) error {
	if err := limits.validate(); err != nil {
		return err
	}
	e.limits = limits
	return nil
}

// CurrentLimits reports the bounds the Engine enforces.
func (e *Engine) CurrentLimits() Limits {
	return e.limits
}

func (l Limits) validate() error {
	switch {
	case l.MaxDatastoresPerTenant <= 0:
		return fmt.Errorf("datastore: max datastores per tenant %d must be positive", l.MaxDatastoresPerTenant)
	case l.MaxColumnsPerDatastore <= 0:
		return fmt.Errorf("datastore: max columns per datastore %d must be positive", l.MaxColumnsPerDatastore)
	case l.MaxRowsPerDatastore <= 0:
		return fmt.Errorf("datastore: max rows per datastore %d must be positive", l.MaxRowsPerDatastore)
	case l.MaxValueBytes <= 0:
		return fmt.Errorf("datastore: max value bytes %d must be positive", l.MaxValueBytes)
	default:
		return nil
	}
}

// checkDatastoreLimit refuses a creation past the per-tenant ceiling. It
// names the limit and the current count, the way the webhook body refusal
// does, so the message is actionable rather than a bare "quota exceeded".
func (e *Engine) checkDatastoreLimit(ctx context.Context, tenantID string) error {
	maximum := e.limits.MaxDatastoresPerTenant
	var count int64
	if err := e.db.WithContext(ctx).Table(e.prefix+"datastores").
		Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil {
		return fmt.Errorf("datastore: count datastores: %w", err)
	}
	if count >= int64(maximum) {
		return fmt.Errorf("datastore: tenant %q holds %d datastores, at the maximum %d", tenantID, count, maximum)
	}
	return nil
}

// checkColumnLimit refuses a definition past the per-datastore column
// ceiling before any catalogue row or DDL is composed.
func (e *Engine) checkColumnLimit(datastoreID string, columns int) error {
	maximum := e.limits.MaxColumnsPerDatastore
	if columns > maximum {
		return fmt.Errorf("datastore: datastore %q holds %d columns, past the maximum %d", datastoreID, columns, maximum)
	}
	return nil
}

// valueBytes reports the stored size of an unbounded value. Only strings and
// raw bytes qualify: numbers, booleans and dates bind fixed-width whatever
// they say, so measuring them would be theatre.
func valueBytes(value any) (int, bool) {
	switch typed := value.(type) {
	case string:
		return len(typed), true
	case []byte:
		return len(typed), true
	default:
		return 0, false
	}
}

// checkValueSizes refuses one write whose single value exceeds the byte
// bound, before any SQL is bound. Names are sorted so the refusal is
// deterministic when several values breach at once.
func checkValueSizes(values map[string]any, maximum int) error {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if size, bounded := valueBytes(values[name]); bounded && size > maximum {
			return fmt.Errorf("datastore: value for column %q is %d bytes, past the maximum %d", name, size, maximum)
		}
	}
	return nil
}

// checkRowLimit refuses an insert into a table sitting at its row maximum.
// It is a bounded existence probe — stop at the limit-th row — never a
// COUNT(*) over the whole table, because on SQLite every datastore operation
// shares one connection and scanning a full table to answer "are there too
// many rows" blocks the API, the scheduler and every running workflow for
// its duration. The check is advisory under races: two inserts may both pass
// it, and the single-statement atomicity in concurrency.go is what still
// holds then.
func (e *Engine) checkRowLimit(ctx context.Context, table string) error {
	maximum := e.limits.MaxRowsPerDatastore
	statement := "SELECT 1 FROM " + quoteIdent(e.dialect(), table) + " LIMIT 1 OFFSET ?"
	e.emit(statement)
	var probe []int
	if err := e.db.WithContext(ctx).Raw(statement, maximum-1).Scan(&probe).Error; err != nil {
		return fmt.Errorf("datastore: probe row limit: %w", err)
	}
	if len(probe) > 0 {
		return fmt.Errorf("datastore: table holds %d rows, at the maximum %d", maximum, maximum)
	}
	return nil
}

// Usage is the observable size of one datastore: the exact row count and,
// where the driver can answer it, the exact on-disk bytes. SizeKnown is
// false on SQLite, where no per-table byte figure exists; SizeBytes is then
// zero and must be rendered as unavailable — never as "0 B" beside a table
// holding rows, which would read as empty rather than unmeasured.
type Usage struct {
	DatastoreID string
	Rows        int64
	SizeBytes   int64
	SizeKnown   bool
}

// Usage reports one datastore's size to its owning tenant. Unknown and
// foreign ids refuse alike through the catalogue lookup, so usage reveals
// nothing about another tenant's tables.
func (e *Engine) Usage(ctx context.Context, tenantID, datastoreID string) (Usage, error) {
	_, _, table, err := e.gatedLookup(ctx, tenantID, datastoreID)
	if err != nil {
		return Usage{}, err
	}
	var rows int64
	count := "SELECT COUNT(*) FROM " + quoteIdent(e.dialect(), table)
	e.emit(count)
	if err := e.db.WithContext(ctx).Raw(count).Scan(&rows).Error; err != nil {
		return Usage{}, fmt.Errorf("datastore: count rows: %w", err)
	}
	usage := Usage{DatastoreID: datastoreID, Rows: rows}
	if e.dialect() != "postgres" {
		return usage, nil
	}
	sized := "SELECT pg_total_relation_size(CAST(? AS regclass))"
	e.emit(sized)
	if err := e.db.WithContext(ctx).Raw(sized, table).Scan(&usage.SizeBytes).Error; err != nil {
		return Usage{}, fmt.Errorf("datastore: size table: %w", err)
	}
	usage.SizeKnown = true
	return usage, nil
}
