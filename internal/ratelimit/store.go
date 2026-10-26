package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// GORMStore is the multi-process store: buckets in the shared database, each
// spend inside a row lock, so every process of a deployment draws from one
// allowance instead of granting the full rate each.
//
// The rows are one per (tenant, bucket) — a bounded set, the bucket
// vocabulary is closed and tenants are rows themselves — so the table needs
// no sweeper: an idle bucket refills to capacity in the query that reads it
// and costs nothing while nobody asks.
type GORMStore struct {
	db *gorm.DB
}

// NewGORMStore builds the database-backed store.
func NewGORMStore(db *gorm.DB) *GORMStore {
	return &GORMStore{db: db}
}

// BucketModel is one (tenant, bucket) token bucket.
//
// Exported for the repository's tenant purge, which owns deleting every row a
// tenant has and resolves the table through the model so the configured
// prefix applies. Double precision, not a float column trick: SQLite keeps
// the value as-is and PostgreSQL's float8 matches, so the arithmetic a spend
// does reads identically on both.
type BucketModel struct {
	TenantID  string    `gorm:"not null;size:64;primaryKey"`
	Bucket    string    `gorm:"not null;size:32;primaryKey"`
	Tokens    float64   `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (BucketModel) TableName(namer schema.Namer) string {
	return namer.TableName("rate_limit_buckets")
}

// Spend refills and spends one bucket inside a transaction that holds the
// row, the same locking discipline the execution queue claims with: two
// processes spending concurrently serialise on the row rather than both
// reading the same tokens.
//
// A bucket's first spend inserts it already spent; the insert is the claim,
// so a race between two firsts is settled by the primary key and the loser
// re-reads under the lock.
func (store *GORMStore) Spend(ctx context.Context, tenantID, bucketName string, capacity, refillPerSecond float64, now time.Time) (bool, time.Duration, error) {
	if tenantID == "" || bucketName == "" {
		return false, 0, fmt.Errorf("a rate limit bucket needs a tenant and a name")
	}
	var spent bool
	var retryAfter time.Duration
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for attempt := 0; attempt < 2; attempt++ {
			var held BucketModel
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("tenant_id = ? AND bucket = ?", tenantID, bucketName).
				First(&held).Error
			switch {
			case err == nil:
				spent, retryAfter = settle(&held, capacity, refillPerSecond, now)
				return tx.Model(&BucketModel{}).
					Where("tenant_id = ? AND bucket = ?", tenantID, bucketName).
					Updates(map[string]any{"tokens": held.Tokens, "updated_at": held.UpdatedAt}).Error
			case errors.Is(err, gorm.ErrRecordNotFound):
				// The first spend of a bucket arrives already spent: the
				// insert is the claim, so two processes racing to be first
				// cannot both win it.
				fresh := BucketModel{
					TenantID: tenantID, Bucket: bucketName,
					Tokens: capacity - 1, UpdatedAt: now,
				}
				if err := tx.Create(&fresh).Error; err == nil {
					spent, retryAfter = true, 0
					return nil
				} else if !errors.Is(err, gorm.ErrDuplicatedKey) {
					return err
				}
				// The other first won the insert; fall through and re-read
				// under the lock on the next attempt.
			default:
				return err
			}
		}
		return fmt.Errorf("the rate limit bucket could not be claimed after a race")
	})
	if err != nil {
		return false, 0, err
	}
	return spent, retryAfter, nil
}

// settle refills and spends one held bucket in memory, writing the new
// state back onto it. The twin of MemoryStore's spend, so the two stores
// answer the same question the same way.
func settle(held *BucketModel, capacity, refillPerSecond float64, now time.Time) (bool, time.Duration) {
	if elapsed := now.Sub(held.UpdatedAt).Seconds(); elapsed > 0 {
		held.Tokens += elapsed * refillPerSecond
		if held.Tokens > capacity {
			held.Tokens = capacity
		}
	}
	held.UpdatedAt = now
	if held.Tokens >= 1 {
		held.Tokens--
		return true, 0
	}
	return false, waitFor(1-held.Tokens, refillPerSecond)
}
