// Package outbound delivers the durable outbox's events to the endpoints a
// tenant subscribed, signed, at least once.
//
// The rows are written by the repository in the same transaction as the state
// change they report (see repository.OutboxRouter), so this package never
// decides what happened, only whether the receiver has heard about it yet. A
// dispatcher claims one due row at a time under a lease, posts its payload,
// and records the outcome on the row: delivered on a 2xx, otherwise due again
// after a capped exponential backoff with jitter, and dead once the attempts
// run out. A dispatcher that dies mid-delivery leaves the row leased; when the
// lease expires another dispatcher delivers it, so a receiver can see one
// event twice and deduplicates on the event id every attempt carries.
package outbound

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// The headers every delivery carries. They are the Standard Webhooks names
// the inbound `standardWebhooks` HMAC preset reads (internal/nodepack), so a
// KilasFlow webhook trigger with that preset verifies a KilasFlow delivery.
const (
	HeaderID        = "webhook-id"
	HeaderTimestamp = "webhook-timestamp"
	HeaderSignature = "webhook-signature"
	// signaturePrefix is the scheme version marker the preset expects before
	// the base64 digest.
	signaturePrefix = "v1,"
)

// Defaults for a dispatcher whose options leave them unset.
const (
	DefaultMaxAttempts = 8
	DefaultBaseDelay   = 30 * time.Second
	DefaultMaxDelay    = time.Hour
	DefaultInterval    = 2 * time.Second
	// leaseMargin is how much longer than one request a lease lasts, so a
	// dispatcher still waiting on a slow endpoint is not overtaken by another
	// that delivers the same row in parallel.
	leaseMargin = 30 * time.Second
	// maxResponseRead bounds how much of a receiver's response is read. The
	// body is drained so the connection can be reused, never interpreted.
	maxResponseRead = 4 << 10
)

// SecretSource returns the signing secret for one tenant's deliveries. An
// empty secret is refused rather than sent unsigned.
type SecretSource func(tenantID string) (string, error)

// Options configures a Dispatcher.
type Options struct {
	Store  repository.OutboxStore
	Secret SecretSource
	// Policy is the egress policy every delivery goes through: the URL is
	// checked before the request is built and every dialled address again,
	// exactly as a workflow's own HTTP request is.
	Policy safehttp.Policy
	// WorkerID names this dispatcher on the leases it takes.
	WorkerID string
	// MaxAttempts is how many attempts a row gets before it is dead.
	MaxAttempts int
	// BaseDelay and MaxDelay shape the backoff: BaseDelay doubles per failed
	// attempt up to MaxDelay, and the wait is jittered within its upper half.
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// Lease is how long one claim fences a row. It defaults to the policy's
	// request timeout plus a margin and is never shorter than that.
	Lease time.Duration
	// Interval is how long the loop sleeps when nothing is due.
	Interval time.Duration
	Clock    func() time.Time
	// Jitter returns a value in [0, 1); tests pin it.
	Jitter func() float64
	Logger *slog.Logger
	// Client overrides the HTTP client built from Policy. Tests only.
	Client *http.Client
}

// Dispatcher delivers due outbox rows.
type Dispatcher struct {
	store       repository.OutboxStore
	secret      SecretSource
	policy      safehttp.Policy
	client      *http.Client
	workerID    string
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	lease       time.Duration
	interval    time.Duration
	clock       func() time.Time
	jitter      func() float64
	logger      *slog.Logger
}

// New constructs a dispatcher.
func New(options Options) (*Dispatcher, error) {
	if options.Store == nil || options.Secret == nil {
		return nil, fmt.Errorf("the outbound dispatcher requires an outbox store and a secret source")
	}
	if options.WorkerID == "" {
		return nil, fmt.Errorf("the outbound dispatcher requires a worker ID")
	}
	if options.Policy.Timeout <= 0 {
		options.Policy.Timeout = safehttp.DefaultPolicy().Timeout
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = DefaultMaxAttempts
	}
	if options.BaseDelay <= 0 {
		options.BaseDelay = DefaultBaseDelay
	}
	if options.MaxDelay <= 0 {
		options.MaxDelay = DefaultMaxDelay
	}
	if options.MaxDelay < options.BaseDelay {
		options.MaxDelay = options.BaseDelay
	}
	if floor := options.Policy.Timeout + leaseMargin; options.Lease < floor {
		options.Lease = floor
	}
	if options.Interval <= 0 {
		options.Interval = DefaultInterval
	}
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	if options.Jitter == nil {
		options.Jitter = rand.Float64
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	client := options.Client
	if client == nil {
		client = safehttp.NewClient(options.Policy)
	}
	// A copy, so the redirect rule below never leaks into a client somebody
	// else holds. A delivery is not followed through a redirect: Go turns a
	// redirected POST into a bodiless GET, which would be reported as
	// delivered without the receiver ever seeing the event. A 3xx is a
	// failed attempt like any other non-2xx, and the log shows its status.
	noRedirects := *client
	noRedirects.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Dispatcher{
		store: options.Store, secret: options.Secret, policy: options.Policy, client: &noRedirects,
		workerID: options.WorkerID, maxAttempts: options.MaxAttempts,
		baseDelay: options.BaseDelay, maxDelay: options.MaxDelay, lease: options.Lease,
		interval: options.Interval, clock: options.Clock, jitter: options.Jitter, logger: options.Logger,
	}, nil
}

// Start runs the dispatch loop until ctx ends.
func (dispatcher *Dispatcher) Start(ctx context.Context) {
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			delivered, err := dispatcher.Drain(ctx)
			if err != nil && ctx.Err() == nil {
				dispatcher.logger.Error("outbound dispatch", "error", err)
			}
			wait := dispatcher.interval
			if delivered > 0 {
				// More may be due: come straight back rather than sleeping
				// through a backlog.
				wait = 0
			}
			timer.Reset(wait)
		}
	}()
}

// Drain attempts every row due now, one claim at a time, and reports how many
// attempts it made. One claim per transaction is what lets several
// dispatchers share a backlog instead of locking all of it.
func (dispatcher *Dispatcher) Drain(ctx context.Context) (int, error) {
	attempted := 0
	for ctx.Err() == nil {
		done, err := dispatcher.DispatchOne(ctx)
		if err != nil {
			return attempted, err
		}
		if !done {
			return attempted, nil
		}
		attempted++
	}
	return attempted, ctx.Err()
}

// DispatchOne claims one due row, attempts it and records the outcome. It
// reports false when nothing was due.
func (dispatcher *Dispatcher) DispatchOne(ctx context.Context) (bool, error) {
	now := dispatcher.clock()
	event, found, err := dispatcher.store.ClaimDue(ctx, now, dispatcher.workerID, dispatcher.lease)
	if err != nil || !found {
		return false, err
	}
	status, attemptErr := dispatcher.deliver(ctx, event)
	attempt := repository.OutboundAttempt{
		ID: event.ID, WorkerID: dispatcher.workerID, StatusCode: status, At: dispatcher.clock(),
	}
	switch {
	case attemptErr == nil:
		attempt.Delivered = true
	case event.Attempts+1 >= dispatcher.maxAttempts:
		attempt.Dead = true
		attempt.Error = attemptErr.Error()
	default:
		attempt.Error = attemptErr.Error()
		attempt.NextAttemptAt = attempt.At.Add(dispatcher.Backoff(event.Attempts + 1))
	}
	if err := dispatcher.store.RecordAttempt(ctx, attempt); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// The lease ran out under this attempt and another dispatcher
			// owns the row now (or retention removed it). Its outcome wins;
			// this attempt may have been a duplicate, which the event id
			// lets the receiver absorb.
			dispatcher.logger.Warn("outbound delivery outlived its lease", "event", event.ID, "tenant", event.TenantID)
			return true, nil
		}
		return true, err
	}
	if attempt.Dead {
		dispatcher.logger.Warn("outbound event dead-lettered", "event", event.ID, "tenant", event.TenantID,
			"type", event.EventType, "attempts", event.Attempts+1, "status", status, "error", attempt.Error)
	}
	return true, nil
}

// Backoff is the wait after the attempt-th failed attempt: BaseDelay doubled
// per earlier failure, capped at MaxDelay, then jittered to a point in its
// upper half so a receiver that comes back is not hit by every retry at once.
func (dispatcher *Dispatcher) Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := dispatcher.maxDelay
	if shift := attempt - 1; shift < 32 {
		if scaled := dispatcher.baseDelay << shift; scaled > 0 && scaled < dispatcher.maxDelay {
			delay = scaled
		}
	}
	half := delay / 2
	return half + time.Duration(dispatcher.jitter()*float64(delay-half))
}

// deliver posts one event and returns the response status (0 when no response
// arrived) and why the attempt failed, or nil for a 2xx.
func (dispatcher *Dispatcher) deliver(ctx context.Context, event repository.OutboundEvent) (int, error) {
	target, err := url.Parse(event.Endpoint)
	if err != nil {
		return 0, fmt.Errorf("the endpoint is not a URL")
	}
	if err := dispatcher.policy.CheckURL(target); err != nil {
		return 0, err
	}
	secret, err := dispatcher.secret(event.TenantID)
	if err != nil {
		return 0, fmt.Errorf("the signing secret could not be read")
	}
	if secret == "" {
		return 0, fmt.Errorf("no signing secret is configured, and events are never sent unsigned")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(event.Payload))
	if err != nil {
		return 0, fmt.Errorf("build the request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "KilasFlow-Outbound/1")
	for name, value := range Sign(secret, event.ID, dispatcher.clock(), event.Payload) {
		request.Header[name] = value
	}
	response, err := dispatcher.client.Do(request)
	if err != nil {
		return 0, safehttp.RedactError(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseRead))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response.StatusCode, fmt.Errorf("the endpoint answered %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

// Sign returns the headers that sign one delivery: the event id, the unix
// timestamp, and a base64 HMAC-SHA256 over `timestamp.body` behind `v1,`.
//
// It is the inbound `standardWebhooks` preset's scheme, byte for byte, so the
// verifier that checks a delivery arriving at a KilasFlow webhook checks this
// one too. The timestamp is signed so a receiver can refuse a replay of an old
// delivery; the event id is sent beside it so a receiver can refuse a
// redelivery of one it already handled.
func Sign(secret, eventID string, at time.Time, body []byte) http.Header {
	timestamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	headers := http.Header{}
	headers.Set(HeaderID, eventID)
	headers.Set(HeaderTimestamp, timestamp)
	headers.Set(HeaderSignature, signaturePrefix+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return headers
}
