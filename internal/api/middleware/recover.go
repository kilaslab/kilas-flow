package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover turns a panic in a handler into a 500 instead of tearing down the
// process, so one bad node or handler cannot stop an instance serving every
// other tenant.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}

				// A client disconnecting mid-write is normal, not a fault.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}

				log.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("stack", string(debug.Stack())),
				)

				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"title":"Internal Server Error","status":500}`))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
