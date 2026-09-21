package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/idempotency"
)

// idempotencyKeyDoc is the operator-facing description of the header, shared
// by the operation descriptions that honour it. A struct tag cannot reference a
// constant, so the three IdempotencyKey fields carry this same sentence
// literally; keep them in step.
const idempotencyKeyDoc = "1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency."

// IdempotencyIssue is attached to a problem detail's value so a client can
// branch on the conflict without parsing the message.
type IdempotencyIssue struct {
	Code string `json:"code"`
}

const (
	// IdempotencyKeyReusedCode marks a key that was first used for a different
	// request.
	IdempotencyKeyReusedCode = "idempotency_key_reused"
	// IdempotencyKeyInFlightCode marks a key whose first request is still
	// running. The response carries Retry-After.
	IdempotencyKeyInFlightCode = "idempotency_key_in_flight"
)

// idempotencyProblem maps the idempotency layer's own failures onto the
// problem a caller sees.
//
// It reports false for everything else, so a handler falls through to its own
// mapping for the errors its work produced: a workflow that does not exist is
// still a 404, and a value the row store refuses is still the row store's 422.
// The distinction matters most for *idempotency.StoreError: without this arm a
// failed claim would reach the datastore handler's generic mapping and be
// presented as the caller's mistake, with the driver's own text in the body.
func idempotencyProblem(ctx context.Context, err error) (error, bool) {
	switch {
	case errors.Is(err, idempotency.ErrKeyReused):
		const detail = "this Idempotency-Key was already used for a different request; send a new key for a new request"
		return &huma.ErrorModel{
			Status: http.StatusConflict,
			Title:  "Conflict",
			Detail: detail,
			Errors: []*huma.ErrorDetail{{
				Message:  detail,
				Location: "header.Idempotency-Key",
				Value:    IdempotencyIssue{Code: IdempotencyKeyReusedCode},
			}},
		}, true
	case errors.Is(err, idempotency.ErrInvalidKey):
		const detail = "Idempotency-Key must be 1-255 printable ASCII characters"
		return &huma.ErrorModel{
			Status: http.StatusUnprocessableEntity,
			Title:  "Unprocessable Entity",
			Detail: detail,
			Errors: []*huma.ErrorDetail{{
				Message:  detail,
				Location: "header.Idempotency-Key",
			}},
		}, true
	}

	var inFlight *idempotency.InFlightError
	if errors.As(err, &inFlight) {
		const detail = "a request with this Idempotency-Key is still being processed; retry shortly"
		seconds := int(inFlight.RetryAfter / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		model := &huma.ErrorModel{
			Status: http.StatusConflict,
			Title:  "Conflict",
			Detail: detail,
			Errors: []*huma.ErrorDetail{{
				Message:  detail,
				Location: "header.Idempotency-Key",
				Value:    IdempotencyIssue{Code: IdempotencyKeyInFlightCode},
			}},
		}
		return huma.ErrorWithHeaders(model, http.Header{"Retry-After": {strconv.Itoa(seconds)}}), true
	}

	var storeErr *idempotency.StoreError
	if errors.As(err, &storeErr) {
		return serverProblem(ctx, "idempotency store failed", err), true
	}
	return nil, false
}

// idempotencyUnavailable is the answer to a request that carries a key on an
// instance with no idempotency service. It is a refusal rather than a silent
// pass: the caller sent the key believing its retry was safe, and answering
// normally would run the side effect a second time while looking protected.
func idempotencyUnavailable() error {
	return huma.Error503ServiceUnavailable("idempotency is not available on this instance")
}

// replayedHeader renders the Idempotent-Replayed response header. An empty
// string is not written at all, so a first response carries no marker rather
// than a false one.
func replayedHeader(replayed bool) string {
	if replayed {
		return "true"
	}
	return ""
}
