package handlers

import (
	"context"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
)

// serverProblem logs the cause of an internal failure and answers with the
// generic problem a caller sees.
//
// A handler that returns 500 without logging err leaves an operator with a
// status code and no cause: the access log names the path and the status but
// never the reason, so a transient database error is undiagnosable after the
// fact. Every internal failure therefore goes through here, which puts the
// reason in the log beside the request ID that ties it to the access line
// while the response body keeps carrying nothing but the handler's own detail.
//
// detail is written for the caller and never includes err: a driver message
// can name a table, a host, or a tenant's identifier.
func serverProblem(ctx context.Context, detail string, err error) error {
	slog.ErrorContext(ctx, "request failed",
		slog.String("detail", detail),
		slog.String("request_id", middleware.RequestIDFrom(ctx)),
		slog.String("error", err.Error()),
	)
	return huma.Error500InternalServerError(detail)
}
