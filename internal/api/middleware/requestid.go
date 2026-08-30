// Package middleware holds cross-cutting HTTP concerns: request identity,
// structured access logging, and panic recovery.
package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey struct{ name string }

var requestIDKey = &contextKey{"request-id"}

// HeaderRequestID is the header a request ID is read from and echoed on.
const HeaderRequestID = "X-Request-ID"

// RequestID attaches an ID to every request so a log line, an execution record
// and an error response can be correlated.
//
// An inbound X-Request-ID is preserved, letting a host SaaS trace a call that
// crosses into kilasflow.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}

		w.Header().Set(HeaderRequestID, id)

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom returns the request ID carried by ctx, or "" if there is none.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
