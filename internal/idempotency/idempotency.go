// Package idempotency makes a retried request run once.
//
// A caller that sends an Idempotency-Key may not know whether its first
// request arrived, so it sends it again. Without this layer the second request
// runs the work a second time: a workflow is queued twice, a row is inserted
// twice. With it, the first request's outcome is recorded against the key and
// the retry is answered from that record.
//
// The key is CLAIMED before the work runs and COMPLETED with the outcome
// afterwards, and the claim is a unique index rather than a lock, so two
// requests that arrive together cannot both proceed. The claim is never held
// inside a transaction that spans the work: SQLite runs this server on one
// connection, and the work itself writes to the same database, so a
// transaction held across it would deadlock against itself.
//
// Nothing here lives in a process. A retry may land on any replica and the
// record survives a restart, which is the whole reason the state is a table
// rather than a map.
//
// The in-flight case is answered, not waited on: a duplicate that arrives
// while the first request is still running gets a typed error carrying a
// Retry-After, and the server holds no goroutine or connection on a guess
// about how long the first request will take.
package idempotency

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// The bounds this package works to.
const (
	// MaxKeyLength is the longest key accepted, matching the column's width.
	MaxKeyLength = 255
	// DefaultInFlightWindow is how long a claim is held before it is treated
	// as abandoned. It is a lease, not a timeout: the work is not interrupted,
	// it only stops blocking a retry if its owner died.
	DefaultInFlightWindow = 2 * time.Minute
	// MaxOutcomeBytes is the largest outcome recorded for replay. An outcome
	// above it is stored in the handler's compact form, and one with no fitted
	// form at all frees the key rather than blocking it for ever.
	MaxOutcomeBytes = 1 << 20
	// DefaultSweepInterval is how often expired keys are deleted when the
	// composition root does not say otherwise.
	DefaultSweepInterval = 10 * time.Minute

	// maxRetryAfter bounds the delay a caller is asked to wait. The lease is
	// what actually frees the key, so a longer retry-after than this would
	// only make a client wait past the retry it is allowed to make.
	maxRetryAfter = 5 * time.Second
	// storeTimeout bounds this layer's own writes, which run on a context of
	// their own so a client giving up cannot corrupt the state it leaves.
	storeTimeout = 5 * time.Second
	// claimTokenBytes is the size of the random token that says which request
	// owns a claim.
	claimTokenBytes = 16
)

var (
	// ErrKeyReused reports a key sent with a different request than the one it
	// was first used for. Callers translate it into a 409.
	ErrKeyReused = errors.New("idempotency key was already used for a different request")
	// ErrInvalidKey reports a key that is not a key. Callers translate it into
	// a 422, the same status the row values this key guards are refused with.
	ErrInvalidKey = errors.New("idempotency key is invalid")
)

// InFlightError reports that a request with the same key is still running.
// RetryAfter is what the caller should put in its Retry-After header.
type InFlightError struct {
	RetryAfter time.Duration
}

func (e *InFlightError) Error() string {
	return fmt.Sprintf("another request with this idempotency key is still running; retry in %s", e.RetryAfter)
}

// StoreError wraps a failure of this layer rather than of the caller's work.
//
// The distinction is load-bearing at the edge: a handler answers the caller's
// own errors with the status it chose, while a failure it cannot explain is a
// 500. Without a type to test for, "the claim could not be written" would be
// reported as though the caller had sent something wrong.
type StoreError struct {
	// Op names what this layer was doing, for the log rather than the caller.
	Op string
	// Err is the underlying failure.
	Err error
}

func (e *StoreError) Error() string { return fmt.Sprintf("idempotency %s: %v", e.Op, e.Err) }

func (e *StoreError) Unwrap() error { return e.Err }

// Request is the request being made idempotent.
//
// Operation and Target are part of what the key means: the same key sent to
// two different workflows, or used for an insert instead of a run, is a
// conflict rather than a replay. Body is the parsed request body, canonicalised
// by Hash.
type Request struct {
	Operation string
	Target    string
	Body      any
}

// Response is what the work produced, and what may be recorded for a replay.
//
// Body is what this caller receives. Record is the form stored and replayed
// instead of Body, and Compact is the form stored instead when the Record form
// (or Body, when there is no Record) is larger than MaxOutcomeBytes. Both are
// optional: a handler whose outcomes are small and safe to store supplies
// neither.
//
// Record exists because Body is not always the right thing to keep. A run
// echoes its input, and storing that would put a copy of the caller's payload
// in a second table for as long as the key lives, outliving the settings that
// govern execution history. Compact exists because an oversized outcome must
// not mean "record nothing": the duplicate this feature exists to prevent
// would come back with no error to show for it.
type Response struct {
	Status  int
	Body    any
	Record  any
	Compact any
}

// Outcome is a recorded response as a replay receives it.
type Outcome struct {
	Status int
	Body   json.RawMessage
}

// Decode reads the recorded body into v.
func (o Outcome) Decode(v any) error {
	if len(o.Body) == 0 {
		return errors.New("the recorded outcome has no body")
	}
	return json.Unmarshal(o.Body, v)
}

// Result is one request's answer. Replayed is true when it came from the
// record rather than from the work, which is what the edge reports as
// Idempotent-Replayed.
type Result struct {
	Outcome  Outcome
	Replayed bool
}

// Options configures a Service. Only Retention is required.
type Options struct {
	// Retention is how long a completed key is remembered. A key past it
	// behaves as never seen.
	Retention time.Duration
	// InFlight is the claim's lease. Zero means DefaultInFlightWindow.
	InFlight time.Duration
	// Now is the clock, for tests. Nil means time.Now.
	Now func() time.Time
	// Log receives this layer's own warnings and failures, never a key or a
	// body. Nil discards them.
	Log *slog.Logger
}

// Service is request idempotency over a durable store.
type Service struct {
	repo      repository.IdempotencyRepository
	retention time.Duration
	inFlight  time.Duration
	now       func() time.Time
	log       *slog.Logger
}

// NewService builds the service over an idempotency store.
func NewService(repo repository.IdempotencyRepository, opts Options) (*Service, error) {
	if repo == nil {
		return nil, errors.New("idempotency: a store is required")
	}
	if opts.Retention <= 0 {
		return nil, errors.New("idempotency: a positive retention is required")
	}
	service := &Service{
		repo:      repo,
		retention: opts.Retention,
		inFlight:  opts.InFlight,
		now:       opts.Now,
		log:       opts.Log,
	}
	if service.inFlight <= 0 {
		service.inFlight = DefaultInFlightWindow
	}
	if service.now == nil {
		service.now = time.Now
	}
	if service.log == nil {
		service.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return service, nil
}

// Do runs fn once for this key and replays its outcome for every later request
// with the same key.
//
// A request whose key is already held by a running request is refused with
// *InFlightError; one whose key was used for a different request is refused
// with ErrKeyReused; a failed fn's error is returned as fn returned it, with
// the key released so the caller can retry. Every failure of this layer's own
// comes back as *StoreError.
func (s *Service) Do(
	ctx context.Context,
	tenantID, key string,
	req Request,
	fn func(context.Context) (Response, error),
) (Result, error) {
	if err := ValidKey(key); err != nil {
		return Result{}, err
	}
	if tenantID == "" {
		// A key with no tenant would be shared by every tenant of the
		// installation, and an unscoped key is a cross-tenant oracle.
		return Result{}, &StoreError{Op: "claim the key", Err: errors.New("a tenant is required")}
	}

	hash, err := s.Hash(tenantID, key, req)
	if err != nil {
		return Result{}, &StoreError{Op: "hash the request", Err: err}
	}
	token, err := newClaimToken()
	if err != nil {
		return Result{}, &StoreError{Op: "mint a claim token", Err: err}
	}
	now := s.now().UTC()
	tenant := repository.TenantScope{ID: tenantID}

	claim, err := s.repo.Claim(ctx, repository.IdempotencyClaim{
		Tenant:        tenant,
		Key:           key,
		Operation:     req.Operation,
		RequestHash:   hash,
		Token:         token,
		Now:           now,
		InFlightUntil: now.Add(s.inFlight),
	})
	if err != nil {
		return Result{}, &StoreError{Op: "claim the key", Err: err}
	}
	if !claim.Acquired {
		return s.held(claim.Existing, hash, now)
	}

	// Every path out of here that is not a recorded outcome gives the claim
	// back, including a panic inside fn and a cancelled request. A claim left
	// behind by a request that is over would refuse the client's retry for the
	// rest of its lease.
	settled := false
	defer func() {
		if settled {
			return
		}
		s.release(ctx, tenant, key, token)
	}()

	response, err := fn(ctx)
	if err != nil {
		return Result{}, err
	}

	// What this caller receives, marshalled before anything is recorded: a body
	// that cannot be serialised is this layer's failure and must not be
	// mistaken for an outcome.
	body, err := json.Marshal(response.Body)
	if err != nil {
		return Result{}, &StoreError{Op: "marshal the outcome", Err: err}
	}
	outcome := Outcome{Status: response.Status, Body: body}

	if response.Status < 200 || response.Status > 299 {
		// A refusal is not an outcome worth replaying: the client will correct
		// its request and send it again, and that request must be able to run.
		settled = true
		s.release(ctx, tenant, key, token)
		s.log.Warn("idempotency key released unrecorded: the request did not succeed",
			"operation", req.Operation, "status", response.Status)
		return Result{Outcome: outcome}, nil
	}

	stored, err := s.stored(response)
	if err != nil {
		return Result{}, err
	}
	if stored == nil {
		settled = true
		s.release(ctx, tenant, key, token)
		return Result{Outcome: outcome}, nil
	}

	// Recording runs on a context of its own: the caller may have given up,
	// and the next caller must still be answered from what this one did.
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	recorded, err := s.repo.Complete(completeCtx, tenant, key, token, response.Status, stored, s.now().UTC().Add(s.retention))
	switch {
	case err != nil:
		s.log.Error("recording the idempotency outcome", "operation", req.Operation, "error", err)
	case !recorded:
		// The lease expired and another request took the key. Its outcome is
		// the one that counts, so this caller is answered normally and the
		// record is left alone.
		s.log.Warn("the idempotency claim was lost before its outcome was recorded", "operation", req.Operation)
	}
	settled = true
	return Result{Outcome: outcome}, nil
}

// Sweep deletes keys whose retention has passed.
func (s *Service) Sweep(ctx context.Context) (int64, error) {
	// The batch size is the store's policy, not this layer's.
	swept, err := s.repo.Sweep(ctx, s.now().UTC(), 0)
	if err != nil {
		return swept, &StoreError{Op: "sweep expired keys", Err: err}
	}
	return swept, nil
}

// held answers a request whose key is already taken.
func (s *Service) held(record repository.IdempotencyRecord, hash string, now time.Time) (Result, error) {
	if record.RequestHash != hash {
		return Result{}, ErrKeyReused
	}
	if record.State == repository.IdempotencyCompleted {
		return Result{Outcome: Outcome{Status: record.Status, Body: record.Body}, Replayed: true}, nil
	}
	return Result{}, &InFlightError{RetryAfter: retryAfter(record.ExpiresAt, now)}
}

// stored picks the form of the outcome that is recorded, or reports nil when
// nothing this handler offers fits the cap. The warning is written here so it
// names the operation but never the key.
func (s *Service) stored(response Response) ([]byte, error) {
	form := response.Record
	if form == nil {
		form = response.Body
	}
	stored, err := json.Marshal(form)
	if err != nil {
		return nil, &StoreError{Op: "marshal the recorded outcome", Err: err}
	}
	if len(stored) <= MaxOutcomeBytes {
		return stored, nil
	}

	reason := "the handler supplies no compact form"
	if response.Compact != nil {
		compact, err := json.Marshal(response.Compact)
		if err != nil {
			return nil, &StoreError{Op: "marshal the compact outcome", Err: err}
		}
		if len(compact) <= MaxOutcomeBytes {
			return compact, nil
		}
		stored, reason = compact, "the compact form is above the cap too"
	}
	s.log.Warn("idempotency key released unrecorded: the outcome is above the cap and "+reason,
		"bytes", len(stored), "cap", MaxOutcomeBytes)
	return nil, nil
}

// release gives a claim back on a context of its own. The request's context
// may already be cancelled — that is one of the ways a request ends — and a
// release cancelled with it would leave the key held for the whole lease.
func (s *Service) release(ctx context.Context, tenant repository.TenantScope, key, token string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	if err := s.repo.Release(releaseCtx, tenant, key, token); err != nil {
		s.log.Error("releasing an idempotency claim", "error", err)
	}
}

// retryAfter is when the caller should come back, rounded up to whole seconds
// because that is the resolution of the header it goes in.
func retryAfter(expiresAt, now time.Time) time.Duration {
	delay := expiresAt.UTC().Sub(now.UTC())
	if delay < 0 {
		delay = 0
	}
	if remainder := delay % time.Second; remainder != 0 {
		delay += time.Second - remainder
	}
	if delay < time.Second {
		delay = time.Second
	}
	if delay > maxRetryAfter {
		delay = maxRetryAfter
	}
	return delay
}

// ValidKey reports whether a key can be recorded.
//
// Printable ASCII only, because the header travels through proxies, log lines
// and shell scripts, and a control character in it is a way to write into
// someone else's log. The length bound is the column's.
func ValidKey(key string) error {
	if len(key) == 0 {
		return fmt.Errorf("%w: it must not be empty", ErrInvalidKey)
	}
	if len(key) > MaxKeyLength {
		return fmt.Errorf("%w: it is %d bytes, the limit is %d", ErrInvalidKey, len(key), MaxKeyLength)
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return fmt.Errorf("%w: byte %d is not printable ASCII", ErrInvalidKey, i)
		}
	}
	return nil
}

func newClaimToken() (string, error) {
	buf := make([]byte, claimTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
