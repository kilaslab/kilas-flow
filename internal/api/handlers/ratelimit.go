package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// RateLimiter decides whether one more request from a tenant may proceed,
// and when it may not, how long the caller should wait. The per-tenant
// implementation lives in internal/ratelimit; this is the slice of it the
// HTTP handlers hold, so a handler can be built and tested with a stub.
type RateLimiter interface {
	Allow(ctx context.Context, tenantID, bucket string) (bool, time.Duration, error)
}

// RateLimitedCode is the named problem code a refused request carries, so a
// client can tell a throttle from any other 429 without parsing the message.
// The webhook surface carries the same word as a `code` member in its plain
// problem body.
const RateLimitedCode = "rate_limited"

// rateLimitedIssue is the typed value attached to a refusal's error detail.
type rateLimitedIssue struct {
	Code string `json:"code"`
}

// rateLimitedProblem is the 429 every rate-limited surface answers with: the
// Retry-After header, rounded to whole seconds, and the named code. Sent
// before any execution or delivery claim exists, so a refused request costs
// the queue nothing.
func rateLimitedProblem(retryAfter time.Duration) error {
	seconds := int(retryAfter / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	const detail = "this tenant has reached its rate limit for this operation; retry after the named delay"
	model := &huma.ErrorModel{
		Status: http.StatusTooManyRequests,
		Title:  "Too Many Requests",
		Detail: detail,
		Errors: []*huma.ErrorDetail{{
			Message: detail,
			Value:   rateLimitedIssue{Code: RateLimitedCode},
		}},
	}
	return huma.ErrorWithHeaders(model, http.Header{"Retry-After": {strconv.Itoa(seconds)}})
}
