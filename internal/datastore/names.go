package datastore

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// A data table's name identifies it within its tenant, the way n8n's do, and
// one rule decides when two names are the same: compared without regard to case
// or to surrounding spaces. The By-Name locator has always compared that way,
// so a catalogue that held "Leads" beside "leads" left the locator to act on
// whichever the list happened to return first — and an Update, Delete or Clear
// then wrote to a table nobody chose (BUG-e7dwpk). Creating and renaming refuse
// a name the rule says is taken, and ResolveByName refuses a name it cannot pin
// to one table, so the two can never disagree about what a name means.
//
// The rule is Go's strings.EqualFold rather than the database's lower(): SQLite
// folds ASCII only and PostgreSQL folds by its locale, so the database cannot be
// the authority on both drivers. The unique index on (tenant_id, lower(name))
// that migration 000021 adds is the backstop for two writers that pass the
// check at once, not the check itself.

// ErrNameTaken reports a create or rename onto a name another data table of the
// tenant already holds. The error that carries it is a *NameTakenError, which
// names that table.
var ErrNameTaken = errors.New("datastore: name is taken")

// NameTakenError is the taken-name refusal. Name is the name the existing table
// holds, which for a clash of case alone is not the name that was asked for:
// "leads" is refused because of "Leads", and saying so is what lets the caller
// find the table in the way.
type NameTakenError struct {
	Name string
}

func (taken *NameTakenError) Error() string {
	return fmt.Sprintf("datastore: a data table named %q already exists", taken.Name)
}

// Unwrap makes errors.Is(err, ErrNameTaken) hold for every taken-name refusal.
func (taken *NameTakenError) Unwrap() error { return ErrNameTaken }

// ErrAmbiguousName reports a By-Name lookup that more than one data table
// answers. The engine never writes such a pair, and the unique index stops one
// only as far as the database folds case: SQLite's lower(), and PostgreSQL's
// under the C locale, leave "Ä" and "ä" apart. A pair written around the engine
// that way is refused here rather than resolved, because picking one would be
// a guess.
var ErrAmbiguousName = errors.New("datastore: data table name is ambiguous")

// sameName is the one rule for when two data table names are the same.
func sameName(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

// ResolveByName returns the id of the one candidate a name identifies. A name
// no candidate holds is the unknown-datastore refusal every other lookup gives,
// so IsUnknown and the HTTP layer's 404 treat it alike; a name several hold is
// ErrAmbiguousName, listing their ids so the caller can choose one by id.
//
// It never falls back to the first match. The candidates are whatever the
// caller listed — the tenant's catalogue for a run, the grants of an embed
// session for a confinement check — and their order means nothing.
func ResolveByName(candidates []Datastore, name string) (string, error) {
	want := strings.TrimSpace(name)
	if want == "" {
		return "", fmt.Errorf("datastore: unknown datastore %q", name)
	}
	var matched []string
	for _, candidate := range candidates {
		if sameName(candidate.Name, want) {
			matched = append(matched, candidate.ID)
		}
	}
	switch len(matched) {
	case 0:
		return "", fmt.Errorf("datastore: unknown datastore %q", want)
	case 1:
		return matched[0], nil
	default:
		return "", fmt.Errorf("%w: %q matches %d data tables (%s); choose the table from the list or by id",
			ErrAmbiguousName, want, len(matched), strings.Join(matched, ", "))
	}
}

// nameHolder is the taken-name refusal for a name another data table of the
// tenant holds, or nil when the name is free. except is the table being
// renamed, which may keep its own name in another case.
//
// It reads the tenant's names and compares them in Go, by sameName, for the
// reason the rule is Go's. The read is id and name only: a tenant's catalogue
// is bounded by its datastore limit, and nothing else about the rows matters
// here.
func nameHolder(tx *gorm.DB, tenantID, except, name string) error {
	var rows []datastoreModel
	if err := tx.Select("id", "name").Where("tenant_id = ?", tenantID).Find(&rows).Error; err != nil {
		return fmt.Errorf("datastore: read the tenant's data table names: %w", err)
	}
	for _, row := range rows {
		if row.ID != except && sameName(row.Name, name) {
			return &NameTakenError{Name: row.Name}
		}
	}
	return nil
}

// indexedNameHolder asks the question the unique index asked when it refused a
// write — which of the tenant's tables, other than except, has the same
// lower(name) — and is the taken-name refusal naming it, or nil when none does,
// which leaves the id or the surrogate as the key that collided.
//
// It classifies a duplicate key after the fact; it is not the rule. The two
// can disagree: PostgreSQL's lower() folds by the database's locale, and under
// a libc locale folds "İ" to "i", which Go does not. A write the index refused
// stays refused whatever sameName says, so the table the database saw is the
// one to name — and asking sameName instead would find nothing and send the
// write round the retries it can never pass.
func indexedNameHolder(tx *gorm.DB, tenantID, except, name string) error {
	var rows []datastoreModel
	if err := tx.Model(&datastoreModel{}).Select("id", "name").
		Where("tenant_id = ? AND id <> ? AND lower(name) = lower(?)", tenantID, except, name).
		Limit(1).Find(&rows).Error; err != nil {
		return fmt.Errorf("datastore: read the tenant's data table names: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return &NameTakenError{Name: rows[0].Name}
}
