package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// OneTimeKey spells the key for something that may be used once, such as the
// state of a Google Connect popup.
//
// One-time keys live in the same table as Idempotency-Key headers, because
// that table already is a durable, replica-safe "has this happened" record
// with expiry, a sweeper and the tenant purge. They must never collide with a
// header, or a client could claim one in advance and block it, so the space
// between the purpose and the value is deliberate: ValidKey refuses a space in
// a header, and no header can ever be spelled like this.
func OneTimeKey(purpose, value string) string {
	return purpose + " " + value
}

// ConsumeOnce records that key has been used and reports whether this call was
// the first. It stays used until until passes, after which the sweeper removes
// it; a key whose until has already passed is never admitted, because what it
// stands for has expired with it.
//
// It is a claim that is never completed or released: the row's lease is the
// window the key is valid for. Nothing is held in a process, so a replay that
// lands on another replica is refused as surely as one on this replica.
func (s *Service) ConsumeOnce(ctx context.Context, tenantID, key string, until time.Time) (bool, error) {
	if tenantID == "" {
		return false, &StoreError{Op: "consume a one-time key", Err: errors.New("a tenant is required")}
	}
	now := s.now().UTC()
	if !until.After(now) {
		return false, nil
	}
	token, err := newClaimToken()
	if err != nil {
		return false, &StoreError{Op: "mint a claim token", Err: err}
	}
	sum := sha256.Sum256([]byte(key))
	claim, err := s.repo.Claim(ctx, repository.IdempotencyClaim{
		Tenant:        repository.TenantScope{ID: tenantID},
		Key:           key,
		Operation:     oneTimeOperation,
		RequestHash:   hex.EncodeToString(sum[:]),
		Token:         token,
		Now:           now,
		InFlightUntil: until.UTC(),
	})
	if err != nil {
		return false, &StoreError{Op: "consume a one-time key", Err: err}
	}
	return claim.Acquired, nil
}

// oneTimeOperation marks a row as a one-time key rather than a request's
// recorded outcome, for anyone reading the table.
const oneTimeOperation = "one-time"
