package handlers

import (
	"context"
	"log/slog"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
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

// modulePath is this project's own import prefix, so a type it defines is told
// apart from one a dependency defines.
const modulePath = "github.com/kilaslab/kilas-flow"

// internalFailure reports whether err came from underneath this process — a
// database driver, a network stack, another library — rather than from the
// request or from this project's own validation.
//
// The handlers that store things answer 422 with err.Error() for a payload they
// could not accept, which is the right answer for a caller mistake and the
// wrong one for a failure underneath: a driver message can name a table, a
// column, or a host, and presenting a server fault as the caller's sends an
// integrator to their own editor for a problem they cannot fix.
//
// The two are told apart by provenance, not by text. The chain is walked, and
// an error whose type is defined outside the standard library and outside this
// module came from something this process does not control. That is the same
// rule the go command uses to tell a module path from a standard-library one:
// a standard-library package path never has a dot in its first element, so
// "strconv", "errors", and "database/sql" read as text this process raised
// while "modernc.org/sqlite", "github.com/jackc/pgx/v5/pgconn" and
// "github.com/go-sql-driver/mysql" read as the database's own answer.
//
// The named type is what carries the package path, so a pointer receiver is
// dereferenced first: reflect's PkgPath is empty for a pointer, which would
// otherwise read every `*pgconn.PgError` (the shape a driver actually returns)
// as a mistake the caller made.
//
// The rule is one-sided on purpose. Anything unrecognised reads as the caller's
// — the reading every one of these handlers already had — so a validation path
// added later cannot start answering 500 by being written.
func internalFailure(err error) bool {
	for _, current := range errorChain(err) {
		path := errorTypePath(current)
		first, _, _ := strings.Cut(path, "/")
		if !strings.Contains(first, ".") || strings.HasPrefix(path, modulePath) {
			continue
		}
		return true
	}
	return false
}

// errorTypePath is the package path of the type behind an error, following
// pointers to the named type that declares it.
func errorTypePath(err error) string {
	declared := reflect.TypeOf(err)
	for declared != nil && declared.Kind() == reflect.Pointer {
		declared = declared.Elem()
	}
	if declared == nil {
		return ""
	}
	return declared.PkgPath()
}

// errorChain flattens an error and everything it wraps, including the multiple
// errors errors.Join carries.
func errorChain(err error) []error {
	var chain []error
	var walk func(error)
	walk = func(current error) {
		if current == nil {
			return
		}
		chain = append(chain, current)
		switch wrapped := current.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range wrapped.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap())
		}
	}
	walk(err)
	return chain
}
