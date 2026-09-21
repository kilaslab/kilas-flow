package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// idempotencyKeyModel is one Idempotency-Key a tenant has sent.
//
// tenant_id is a plain column on purpose: the tenant purge finds tenant-scoped
// tables by that name, and a recorded outcome names the tenant's execution ids
// and datastore rows, so it has to be deleted with them. There is no foreign
// key to executions either — keys and executions are retained independently.
type idempotencyKeyModel struct {
	ID             uint   `gorm:"primaryKey;autoIncrement"`
	TenantID       string `gorm:"not null;size:64;uniqueIndex:uidx_idempotency_keys,priority:1"`
	IdempotencyKey string `gorm:"not null;size:255;uniqueIndex:uidx_idempotency_keys,priority:2"`
	Operation      string `gorm:"not null;size:64"`
	RequestHash    string `gorm:"not null;size:64"`
	State          string `gorm:"not null;size:16"`
	ClaimToken     string `gorm:"not null;size:32"`
	StatusCode     int    `gorm:"not null;default:0"`
	Response       []byte
	CreatedAt      time.Time `gorm:"not null"`
	ExpiresAt      time.Time `gorm:"not null;index"`
}

func (idempotencyKeyModel) TableName(namer schema.Namer) string {
	return namer.TableName("idempotency_keys")
}

// IdempotencyState is where a claimed key is in its life.
type IdempotencyState string

const (
	// IdempotencyInProgress means a request claimed the key and has not
	// recorded an outcome yet.
	IdempotencyInProgress IdempotencyState = "in_progress"
	// IdempotencyCompleted means the first request finished and its outcome is
	// recorded for replay.
	IdempotencyCompleted IdempotencyState = "completed"
)

// DefaultIdempotencySweepBatch is how many expired keys one sweep statement
// removes.
const DefaultIdempotencySweepBatch = 500

// IdempotencyClaim is one request's attempt to claim a key.
type IdempotencyClaim struct {
	Tenant TenantScope
	// Key is the caller's Idempotency-Key, 1 to 255 bytes.
	Key string
	// Operation names what the request does, so a key reused on a different
	// operation is recognisable.
	Operation string
	// RequestHash is the SHA-256 hex digest of the operation, target and
	// canonical body. It is what a reuse with a different request is compared
	// by.
	RequestHash string
	// Token identifies this attempt. Complete and Release act only on a row
	// that still carries it, so a request that lost its claim to a lease
	// takeover cannot overwrite the new owner's row.
	Token string
	// Now is the caller's clock reading. Every stored and compared time is
	// normalised to UTC here, whatever zone the caller's clock is in.
	Now time.Time
	// InFlightUntil is when this claim stops blocking other requests if it is
	// never completed.
	InFlightUntil time.Time
}

// IdempotencyRecord is what a key currently holds.
type IdempotencyRecord struct {
	Operation   string
	RequestHash string
	State       IdempotencyState
	// Status and Body are the recorded outcome and are meaningful only when
	// State is IdempotencyCompleted.
	Status    int
	Body      []byte
	CreatedAt time.Time
	ExpiresAt time.Time
}

// IdempotencyClaimResult reports whether the caller now owns the key.
type IdempotencyClaimResult struct {
	// Acquired is true when this attempt inserted the row and may run the side
	// effect. When false, Existing is the row that is holding the key.
	Acquired bool
	Existing IdempotencyRecord
}

// IdempotencyRepository is the durable half of request idempotency.
type IdempotencyRepository interface {
	// Claim takes a key for one request, or reports who holds it.
	Claim(context.Context, IdempotencyClaim) (IdempotencyClaimResult, error)
	// Complete records the first request's outcome and moves the key's expiry
	// to the retention deadline. It reports false when the claim is no longer
	// this token's.
	Complete(ctx context.Context, tenant TenantScope, key, token string, status int, body []byte, expiresAt time.Time) (bool, error)
	// Release gives up a claim so the key can be used again. It is not an
	// error when the claim is already gone.
	Release(ctx context.Context, tenant TenantScope, key, token string) error
	// Sweep deletes keys whose expiry has passed, batch rows at a time, and
	// returns how many went.
	Sweep(ctx context.Context, now time.Time, batch int) (int64, error)
	// PurgeTenant deletes every key one tenant owns.
	PurgeTenant(ctx context.Context, tenant TenantScope) (int64, error)
}

// GORMIdempotencyStore is the database implementation of IdempotencyRepository.
type GORMIdempotencyStore struct {
	db *gorm.DB
}

// NewIdempotencyStore builds the store over an open database.
func NewIdempotencyStore(db *gorm.DB) *GORMIdempotencyStore {
	return &GORMIdempotencyStore{db: db}
}

var _ IdempotencyRepository = (*GORMIdempotencyStore)(nil)

// idempotencyClaimAttempts bounds Claim's delete, insert, read loop. A pass
// only repeats when the row it expected to read was released or swept in the
// instant between its insert and its read, so a second miss is already
// unusual and a third means something is churning this one key.
const idempotencyClaimAttempts = 3

// Column bounds the migration declares. Checked here as well so SQLite, which
// stores any length, refuses what PostgreSQL would.
const (
	idempotencyMaxKey       = 255
	idempotencyHashLength   = 64
	idempotencyMaxToken     = 32
	idempotencyMaxOperation = 64
)

func (claim IdempotencyClaim) validate() error {
	if err := claim.Tenant.validate(); err != nil {
		return err
	}
	if n := len(claim.Key); n < 1 || n > idempotencyMaxKey {
		return fmt.Errorf("idempotency key must be 1 to %d bytes, got %d", idempotencyMaxKey, n)
	}
	if n := len(claim.RequestHash); n != idempotencyHashLength {
		return fmt.Errorf("idempotency request hash must be %d characters, got %d", idempotencyHashLength, n)
	}
	if n := len(claim.Token); n < 1 || n > idempotencyMaxToken {
		return fmt.Errorf("idempotency claim token must be 1 to %d bytes, got %d", idempotencyMaxToken, n)
	}
	if n := len(claim.Operation); n < 1 || n > idempotencyMaxOperation {
		return fmt.Errorf("idempotency operation must be 1 to %d bytes, got %d", idempotencyMaxOperation, n)
	}
	return nil
}

// Claim takes a key for one request, or reports the row that is holding it.
//
// No statement here runs inside a transaction. SQLite runs on one connection,
// so a transaction held open across the caller's side effect would deadlock
// the moment that side effect touched the database, and the engine's own
// writes have no transaction seam to join. Every statement is autocommit, and
// the unique index is what decides between two requests that arrive together:
// the insert of the loser conflicts, and it reads the winner's row.
//
// Every time bound here is converted to UTC. The SQLite driver writes a
// time.Time as text in the value's own zone and SQLite compares that text, so
// a clock reading taken in UTC+7 would sort hours away from the same instant
// in UTC and silently mis-order every lease and every retention deadline.
func (store *GORMIdempotencyStore) Claim(ctx context.Context, claim IdempotencyClaim) (IdempotencyClaimResult, error) {
	if err := claim.validate(); err != nil {
		return IdempotencyClaimResult{}, err
	}
	now := claim.Now.UTC()
	inFlightUntil := claim.InFlightUntil.UTC()

	for range idempotencyClaimAttempts {
		// An expired row is not a duplicate: a stale in-flight claim whose owner
		// died, or a completed key past its retention. Clearing it here rather
		// than waiting for the sweeper makes retention exact even when the sweep
		// is late.
		if err := store.db.WithContext(ctx).
			Where("tenant_id = ? AND idempotency_key = ? AND expires_at <= ?", claim.Tenant.ID, claim.Key, now).
			Delete(&idempotencyKeyModel{}).Error; err != nil {
			return IdempotencyClaimResult{}, fmt.Errorf("clear expired idempotency key: %w", err)
		}

		// DoNothing rather than an error on conflict, and neither RowsAffected
		// nor the row's ID is read back: RETURNING behaves differently per
		// driver when the insert is skipped. The row is read instead, and the
		// claim token says whose it is.
		row := idempotencyKeyModel{
			TenantID:       claim.Tenant.ID,
			IdempotencyKey: claim.Key,
			Operation:      claim.Operation,
			RequestHash:    claim.RequestHash,
			State:          string(IdempotencyInProgress),
			ClaimToken:     claim.Token,
			CreatedAt:      now,
			ExpiresAt:      inFlightUntil,
		}
		if err := store.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "idempotency_key"}},
				DoNothing: true,
			}).
			Create(&row).Error; err != nil {
			return IdempotencyClaimResult{}, fmt.Errorf("claim idempotency key: %w", err)
		}

		var held idempotencyKeyModel
		err := store.db.WithContext(ctx).
			Where("tenant_id = ? AND idempotency_key = ?", claim.Tenant.ID, claim.Key).
			First(&held).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Released or swept between the insert and the read. Go round again.
			continue
		}
		if err != nil {
			return IdempotencyClaimResult{}, fmt.Errorf("read idempotency key: %w", err)
		}
		if held.ClaimToken == claim.Token {
			return IdempotencyClaimResult{Acquired: true}, nil
		}
		return IdempotencyClaimResult{Existing: recordFromIdempotencyModel(held)}, nil
	}
	return IdempotencyClaimResult{}, errors.New("idempotency key contention: the row kept disappearing between insert and read")
}

func recordFromIdempotencyModel(model idempotencyKeyModel) IdempotencyRecord {
	return IdempotencyRecord{
		Operation:   model.Operation,
		RequestHash: model.RequestHash,
		State:       IdempotencyState(model.State),
		Status:      model.StatusCode,
		Body:        model.Response,
		CreatedAt:   model.CreatedAt.UTC(),
		ExpiresAt:   model.ExpiresAt.UTC(),
	}
}

// Complete records the outcome of the request that owns the claim and moves the
// key's expiry from the lease to the retention deadline.
//
// The token and the in_progress state are both in the predicate, so a request
// that lost its claim to a lease takeover cannot overwrite the new owner's row,
// and an outcome, once recorded, is never replaced.
func (store *GORMIdempotencyStore) Complete(ctx context.Context, tenant TenantScope, key, token string, status int, body []byte, expiresAt time.Time) (bool, error) {
	if err := tenant.validate(); err != nil {
		return false, err
	}
	result := store.db.WithContext(ctx).Model(&idempotencyKeyModel{}).
		Where("tenant_id = ? AND idempotency_key = ? AND claim_token = ? AND state = ?",
			tenant.ID, key, token, string(IdempotencyInProgress)).
		Updates(map[string]any{
			"state":       string(IdempotencyCompleted),
			"status_code": status,
			"response":    body,
			"expires_at":  expiresAt.UTC(),
		})
	if result.Error != nil {
		return false, fmt.Errorf("complete idempotency key: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// Release gives up a claim that never produced an outcome. It is not an error
// when the claim is already gone, and it never removes a completed key: the
// state is in the predicate for the same reason it is in Complete's.
func (store *GORMIdempotencyStore) Release(ctx context.Context, tenant TenantScope, key, token string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND idempotency_key = ? AND claim_token = ? AND state = ?",
			tenant.ID, key, token, string(IdempotencyInProgress)).
		Delete(&idempotencyKeyModel{}).Error; err != nil {
		return fmt.Errorf("release idempotency key: %w", err)
	}
	return nil
}

// Sweep deletes keys whose expiry has passed, batch rows at a time.
//
// The shape is PruneExpired's: read one batch of ids, delete exactly those with
// the age predicate repeated, and stop between batches when the context ends,
// so a shutdown lands on a statement boundary rather than inside one. batch
// bounds one delete statement, which on SQLite is the whole time the single
// writer is unavailable to anything else.
func (store *GORMIdempotencyStore) Sweep(ctx context.Context, now time.Time, batch int) (int64, error) {
	if batch <= 0 {
		batch = DefaultIdempotencySweepBatch
	}
	cutoff := now.UTC()

	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		var ids []uint
		if err := store.db.WithContext(ctx).Model(&idempotencyKeyModel{}).
			Where("expires_at < ?", cutoff).
			Order("expires_at ASC, id ASC").
			Limit(batch).
			Pluck("id", &ids).Error; err != nil {
			return total, fmt.Errorf("read expired idempotency keys: %w", err)
		}
		if len(ids) == 0 {
			return total, nil
		}

		// The age predicate is repeated rather than trusting the ids: a row
		// may have been completed with a later expiry since the read, and this
		// statement must not be able to delete one that is no longer expired.
		result := store.db.WithContext(ctx).
			Where("id IN ? AND expires_at < ?", ids, cutoff).
			Delete(&idempotencyKeyModel{})
		if result.Error != nil {
			return total, fmt.Errorf("delete expired idempotency keys: %w", result.Error)
		}
		total += result.RowsAffected

		// A batch that removed nothing means another process got there first,
		// and looping would re-read the same rows for ever.
		if result.RowsAffected == 0 || len(ids) < batch {
			return total, nil
		}
	}
}

// PurgeTenant deletes every key one tenant owns.
func (store *GORMIdempotencyStore) PurgeTenant(ctx context.Context, tenant TenantScope) (int64, error) {
	return purgeIdempotencyKeys(store.db.WithContext(ctx), tenant)
}

// purgeIdempotencyKeys is the one implementation of the tenant delete. The
// store's PurgeTenant and the execution store's tenant purge both call it, the
// second inside its transaction, so what "a tenant's idempotency keys" means
// cannot drift between them.
//
// An empty tenant is refused: it matches nothing, and a purge that matches
// nothing by accident is one typo away from a purge that matches everything.
func purgeIdempotencyKeys(db *gorm.DB, tenant TenantScope) (int64, error) {
	if err := tenant.validate(); err != nil {
		return 0, err
	}
	result := db.Where("tenant_id = ?", tenant.ID).Delete(&idempotencyKeyModel{})
	if result.Error != nil {
		return 0, fmt.Errorf("purge tenant idempotency keys: %w", result.Error)
	}
	return result.RowsAffected, nil
}
